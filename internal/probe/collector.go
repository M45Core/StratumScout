package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"net"
	"net/netip"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

const (
	blockWindow         = 30 * time.Second
	activeBlockLimit    = 32
	completedBlockLimit = 256
	// Periodic session replacement keeps setup telemetry comfortably inside
	// StratumStats's rolling 24-hour protocol window. The process-level jitter
	// prevents every regional Scout from reconnecting at the same time.
	connectionRefreshMin  = 105 * time.Minute
	connectionRefreshSpan = 30 * time.Minute
	maxStratumMessageSize = 256 << 10
	largeMessageCapacity  = 64 << 10
	stratumReaderSize     = maxStratumMessageSize
)

// ErrConnectionRefresh asks the long-lived Scout runner to recreate its pool
// sessions after the current block sample has been uploaded.
var ErrConnectionRefresh = errors.New("refresh Stratum connections")

var (
	errPoolRejected           = errors.New("pool rejected probe")
	errStratumMessageTooLarge = errors.New("stratum message exceeds size limit")
	requestTimeout            = 30 * time.Second
	dialEndpoint              = dialPublicEndpoint
	sharedAddressRange        = netip.MustParsePrefix("100.64.0.0/10")
)

type event struct {
	poolID, prevHash string
	connectionID     string
	at               time.Time
	coinbase         *model.CoinbaseSource
	protocolMethod   string
	protocol         *model.ProtocolSample
}

type endpointTarget struct {
	poolID  string
	address string
	tls     bool
}

type activeBlock struct {
	id        string
	started   time.Time
	eligible  map[string]endpointTarget
	arrivals  map[string]time.Time
	coinbases map[string]model.CoinbaseSource
}

// Collect connects to every configured endpoint and emits exactly one nested
// sample for each completed Bitcoin block. Setup timings are retained in
// memory, folded into the next block sample, and never uploaded independently.
// It submits no shares and never stores randomized credentials.
func Collect(ctx context.Context, pools []model.Pool, emit func(model.BlockSample) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	refreshAge, err := randomizedDuration(connectionRefreshMin, connectionRefreshSpan)
	if err != nil {
		return err
	}
	refreshEligibleAt := time.Now().Add(refreshAge)
	events := make(chan event, 256)
	configured := make(map[string]endpointTarget)
	var wg sync.WaitGroup
	for _, p := range pools {
		for _, endpoint := range p.Endpoints {
			p, endpoint := p, endpoint
			address := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
			configured[endpointConnectionID(p.ID, address, endpoint.TLS)] = endpointTarget{poolID: p.ID, address: address, tls: endpoint.TLS}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := watch(ctx, p.ID, endpoint, events); err != nil && ctx.Err() == nil {
					log.Printf("probe pool=%s endpoint=%s stopped category=%s", p.ID, net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)), connectionErrorCategory(err))
				}
			}()
		}
	}
	go func() { wg.Wait(); close(events) }()

	blocks := map[string]*activeBlock{}
	completedBlocks := map[string]bool{}
	completedBlockOrder := make([]string, 0, completedBlockLimit)
	pendingSetup := make(map[string]model.EndpointSetup)
	var closeTimer *time.Timer
	var closeTimerC <-chan time.Time
	defer func() {
		if closeTimer != nil {
			closeTimer.Stop()
		}
	}()
	scheduleClose := func() {
		deadline, ok := nextBlockDeadline(blocks)
		if !ok {
			if closeTimer != nil && !closeTimer.Stop() {
				select {
				case <-closeTimer.C:
				default:
				}
			}
			closeTimerC = nil
			return
		}
		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		if closeTimer == nil {
			closeTimer = time.NewTimer(delay)
		} else {
			if !closeTimer.Stop() {
				select {
				case <-closeTimer.C:
				default:
				}
			}
			closeTimer.Reset(delay)
		}
		closeTimerC = closeTimer.C
	}
	handleEvent := func(e event) {
		if e.protocol != nil {
			recordProtocolSample(pendingSetup, e)
			return
		}
		r := activeBlockForEvent(blocks, completedBlocks, configured, e)
		if r != nil {
			recordBlockEvent(r, e)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-events:
			if !ok {
				return nil
			}
			handleEvent(e)
			scheduleClose()
		case now := <-closeTimerC:
			// Consume everything already buffered before deciding which block
			// windows have closed. Otherwise the timer can win the select while
			// an arrival for the closing window is still queued.
			closed := drainBufferedEvents(events, handleEvent)
			if closed {
				return nil
			}
			closing := make([]*activeBlock, 0, len(blocks))
			for _, r := range blocks {
				if now.Sub(r.started) >= blockWindow {
					closing = append(closing, r)
				}
			}
			sort.Slice(closing, func(i, j int) bool {
				if closing[i].started.Equal(closing[j].started) {
					return closing[i].id < closing[j].id
				}
				return closing[i].started.Before(closing[j].started)
			})
			for _, r := range closing {
				sample, ok := blockSample(r, pendingSetup)
				if ok {
					if err := emit(sample); err != nil {
						return err
					}
					clear(pendingSetup)
				}
				completedBlockOrder = rememberCompletedBlock(completedBlocks, completedBlockOrder, r.id)
				delete(blocks, r.id)
			}
			if shouldRefreshConnections(now, refreshEligibleAt, len(closing) > 0, len(blocks)) {
				cancel()
				wg.Wait()
				return ErrConnectionRefresh
			}
			scheduleClose()
		}
	}
}

func shouldRefreshConnections(now, eligibleAt time.Time, completedBlock bool, activeBlocks int) bool {
	return completedBlock && !now.Before(eligibleAt) && activeBlocks == 0
}

func nextBlockDeadline(blocks map[string]*activeBlock) (time.Time, bool) {
	var next time.Time
	for _, block := range blocks {
		deadline := block.started.Add(blockWindow)
		if next.IsZero() || deadline.Before(next) {
			next = deadline
		}
	}
	return next, !next.IsZero()
}

func drainBufferedEvents(events <-chan event, handle func(event)) bool {
	// Bound the drain to the snapshot that was buffered when the timer won the
	// select. Producers remain free to enqueue newer events without starving
	// block-window completion.
	for range len(events) {
		e, ok := <-events
		if !ok {
			return true
		}
		handle(e)
	}
	return false
}

// rememberCompletedBlock bounds process-lifetime deduplication state. The
// retained window covers roughly 42 hours at Bitcoin's target block interval,
// while preventing a permanent Scout process from accumulating every block ID.
func rememberCompletedBlock(completed map[string]bool, order []string, id string) []string {
	if completed[id] {
		return order
	}
	if len(order) == completedBlockLimit {
		delete(completed, order[0])
		copy(order, order[1:])
		order = order[:len(order)-1]
	}
	completed[id] = true
	return append(order, id)
}

func blockSample(block *activeBlock, pendingSetup map[string]model.EndpointSetup) (model.BlockSample, bool) {
	if len(block.arrivals) == 0 {
		return model.BlockSample{}, false
	}
	ids := make([]string, 0, len(block.arrivals)+len(pendingSetup))
	included := make(map[string]bool, cap(ids))
	for id := range block.arrivals {
		included[id] = true
		ids = append(ids, id)
	}
	for id := range pendingSetup {
		if !included[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	endpointSamples := make([]model.ForwardedEndpointSample, 0, len(ids))
	for _, id := range ids {
		target, configured := block.eligible[id]
		if !configured {
			continue
		}
		at, arrived := block.arrivals[id]
		endpointSample := model.ForwardedEndpointSample{PoolID: target.poolID, Endpoint: target.address, TLS: target.tls}
		if arrived {
			receivedAt := at.UTC()
			endpointSample.ReceivedAt = &receivedAt
			if coinbase, ok := block.coinbases[id]; ok {
				coinbaseCopy := coinbase
				endpointSample.Coinbase = &coinbaseCopy
			}
		}
		if setup, ok := pendingSetup[id]; ok {
			setupCopy := setup
			endpointSample.Setup = &setupCopy
		}
		endpointSamples = append(endpointSamples, endpointSample)
	}
	return model.BlockSample{
		BlockID:         block.id,
		EndpointSamples: endpointSamples,
	}, true
}

func recordProtocolSample(pending map[string]model.EndpointSetup, e event) {
	if e.protocol == nil || e.connectionID == "" {
		return
	}
	setup := pending[e.connectionID]
	// A new connect attempt supersedes the setup path from the preceding
	// session. Later stages then fill this same compact result in place.
	if e.protocolMethod == model.ProtocolConnect {
		setup = model.EndpointSetup{}
	}
	sample := *e.protocol
	switch e.protocolMethod {
	case model.ProtocolConnect:
		setup.Connect = &sample
	case model.ProtocolTLSHandshake:
		setup.TLS = &sample
	case model.ProtocolSubscribe:
		setup.Subscribe = &sample
	case model.ProtocolAuthorize:
		setup.Authorize = &sample
	default:
		return
	}
	pending[e.connectionID] = setup
}

// activeBlockForEvent prevents late jobs from reopening a measurement window
// after that Bitcoin block has already been finalized.
func activeBlockForEvent(blocks map[string]*activeBlock, completed map[string]bool, configured map[string]endpointTarget, e event) *activeBlock {
	if completed[e.prevHash] {
		return nil
	}
	if block := blocks[e.prevHash]; block != nil {
		return block
	}
	if e.prevHash == "" || len(blocks) >= activeBlockLimit {
		return nil
	}
	block := &activeBlock{
		id: e.prevHash, started: e.at,
		eligible: map[string]endpointTarget{}, arrivals: map[string]time.Time{}, coinbases: map[string]model.CoinbaseSource{},
	}
	for id, target := range configured {
		block.eligible[id] = target
	}
	blocks[e.prevHash] = block
	return block
}

func watchSession(ctx context.Context, poolID string, endpoint model.Endpoint, out chan<- event) error {
	return watchSessionWithReady(ctx, poolID, endpoint, out, nil)
}

func watchSessionWithReady(ctx context.Context, poolID string, endpoint model.Endpoint, out chan<- event, ready func()) error {
	identity, err := RandomIdentity()
	if err != nil {
		return err
	}
	address := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))

	connectStarted := time.Now()
	rawConn, err := dialEndpoint(ctx, "tcp", address)
	connectFinished := time.Now()
	if err != nil {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolConnect, connectStarted, connectFinished, protocolErrorStatus(err), "connect_failed")
		return err
	}
	stopContextClose := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stopContextClose()
	if err := publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolConnect, connectStarted, connectFinished, model.ProtocolStatusOK, ""); err != nil {
		_ = rawConn.Close()
		return err
	}
	defer rawConn.Close()

	var conn net.Conn = rawConn
	if endpoint.TLS {
		tlsConn := tls.Client(rawConn, &tls.Config{ServerName: endpoint.Host, MinVersion: tls.VersionTLS12})
		tlsStarted := time.Now()
		handshakeCtx, cancelHandshake := context.WithTimeout(ctx, requestTimeout)
		err := tlsConn.HandshakeContext(handshakeCtx)
		cancelHandshake()
		tlsFinished := time.Now()
		if err != nil {
			_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolTLSHandshake, tlsStarted, tlsFinished, protocolErrorStatus(err), tlsErrorCategory(err))
			return err
		}
		if err := publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolTLSHandshake, tlsStarted, tlsFinished, model.ProtocolStatusOK, ""); err != nil {
			return err
		}
		conn = tlsConn
	}

	// Keep every accepted mining.notify message in reusable connection memory.
	// One hundred connections consume about 25 MiB in exchange for avoiding
	// growth, copying, and garbage collection on the read path.
	r := bufio.NewReaderSize(conn, stratumReaderSize)
	w := bufio.NewWriter(conn)
	subscribeStarted := time.Now()
	if err := request(w, 1, "mining.subscribe", []string{identity.Agent}, identity.wireStyle); err != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, model.ProtocolStatusError, "subscribe_write_failed")
		return err
	}
	subscribeResult, remoteErr, subscribeFinished, err := awaitResponse(ctx, conn, r, w, identity.Agent, 1, requestTimeout)
	if err != nil {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, subscribeFinished, protocolErrorStatus(err), "subscribe_response_failed")
		return fmt.Errorf("subscribe: %w", err)
	}
	if remoteErr != nil {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, subscribeFinished, model.ProtocolStatusRejected, "subscribe_rejected")
		return fmt.Errorf("%w: subscription rejected", errPoolRejected)
	}
	var subscribe []json.RawMessage
	var extraNonce1 string
	var extraNonce2Size int
	if json.Unmarshal(subscribeResult, &subscribe) != nil || len(subscribe) < 3 {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, subscribeFinished, model.ProtocolStatusError, "subscribe_invalid_response")
		return fmt.Errorf("subscribe: invalid response")
	}
	if json.Unmarshal(subscribe[1], &extraNonce1) != nil || json.Unmarshal(subscribe[2], &extraNonce2Size) != nil || extraNonce1 == "" || extraNonce2Size <= 0 {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, subscribeFinished, model.ProtocolStatusError, "subscribe_invalid_extranonce")
		return fmt.Errorf("subscribe: invalid extranonce")
	}
	if err := publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, subscribeFinished, model.ProtocolStatusOK, ""); err != nil {
		return err
	}

	authorizeStarted := time.Now()
	if err := request(w, 2, "mining.authorize", []string{identity.Username, "x"}, identity.wireStyle); err != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, model.ProtocolStatusError, "authorize_write_failed")
		return err
	}
	authorizeResult, remoteErr, authorizeFinished, err := awaitResponse(ctx, conn, r, w, identity.Agent, 2, requestTimeout)
	if err != nil {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, authorizeFinished, protocolErrorStatus(err), "authorize_response_failed")
		return fmt.Errorf("authorize: %w", err)
	}
	if remoteErr != nil {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, authorizeFinished, model.ProtocolStatusRejected, "authorize_rejected")
		return fmt.Errorf("%w: authorization rejected", errPoolRejected)
	}
	var authorized bool
	if json.Unmarshal(authorizeResult, &authorized) != nil || !authorized {
		_ = publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, authorizeFinished, model.ProtocolStatusRejected, "authorize_rejected")
		return fmt.Errorf("%w: authorization rejected", errPoolRejected)
	}
	if err := publishProtocolAt(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, authorizeFinished, model.ProtocolStatusOK, ""); err != nil {
		return err
	}
	if ready != nil {
		ready()
	}
	// awaitResponse bounds setup operations with a read deadline. Once the
	// long-lived session is authorized, clear it and let the context-driven
	// connection close interrupt an otherwise idle pool. Scout sends no pings.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return err
	}

	connectionID := endpointConnectionID(poolID, address, endpoint.TLS)
	var window notifyWindow

	for {
		line, receivedAt, err := readStratumMessage(r)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}

		msg, err := decodeStratumNotification(line)
		if err != nil {
			continue
		}
		if msg.Method == "client.get_version" {
			if err := response(w, msg.ID, identity.Agent); err != nil {
				return err
			}
			continue
		}
		if msg.Method != "mining.notify" || msg.Params.count < 9 {
			continue
		}
		prev := msg.Params.previousHash
		if !validBlockHash(prev) {
			continue
		}
		if !window.accept(prev, msg.Params.clean) {
			continue
		}
		e := event{
			poolID: poolID, connectionID: connectionID, prevHash: prev, at: receivedAt,
			coinbase: msg.Params.coinbaseSource(extraNonce1, extraNonce2Size, identity.WorkerScriptSHA256),
		}
		select {
		case out <- e:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type stratumNotification struct {
	ID     any          `json:"id"`
	Method string       `json:"method"`
	Params notifyParams `json:"params"`
}

// decodeStratumNotification avoids the generic encoding/json object walk on
// the receive path. json.Valid still rejects malformed network input, then the
// narrow walk below stops as soon as the fields needed for the recognized
// method have been found. In the usual id/method/params order this means a
// mining.notify params array is traversed only by the narrow params parser.
func decodeStratumNotification(data []byte) (stratumNotification, error) {
	var notification stratumNotification
	data = bytes.TrimSpace(data)
	if !json.Valid(data) || len(data) < 2 || data[0] != '{' {
		return notification, errors.New("invalid Stratum notification")
	}

	var idJSON, paramsJSON []byte
	for offset := 1; offset < len(data); {
		offset = skipJSONSpace(data, offset)
		if offset >= len(data) || data[offset] == '}' {
			break
		}
		keyEnd := jsonStringEnd(data, offset)
		if keyEnd < 0 {
			return notification, errors.New("invalid Stratum notification key")
		}
		key := data[offset:keyEnd]
		offset = skipJSONSpace(data, keyEnd)
		if offset >= len(data) || data[offset] != ':' {
			return notification, errors.New("invalid Stratum notification field")
		}
		valueStart := skipJSONSpace(data, offset+1)
		if string(key) == `"params"` && notification.Method == "mining.notify" {
			params, _, err := parseNotifyParams(data[valueStart:])
			if err != nil {
				return stratumNotification{}, err
			}
			notification.Params = params
			return notification, nil
		}
		valueEnd := jsonValueEnd(data, valueStart)
		if valueEnd < 0 {
			return notification, errors.New("invalid Stratum notification value")
		}

		switch string(key) {
		case `"id"`:
			idJSON = data[valueStart:valueEnd]
		case `"method"`:
			switch string(data[valueStart:valueEnd]) {
			case `"mining.notify"`:
				notification.Method = "mining.notify"
			case `"client.get_version"`:
				notification.Method = "client.get_version"
			default:
				method, err := decodeJSONString(data[valueStart:valueEnd])
				if err != nil {
					return stratumNotification{}, err
				}
				notification.Method = method
			}
		case `"params"`:
			paramsJSON = data[valueStart:valueEnd]
		}

		if notification.Method == "mining.notify" && paramsJSON != nil {
			params, consumed, err := parseNotifyParams(paramsJSON)
			if err != nil || len(bytes.TrimSpace(paramsJSON[consumed:])) != 0 {
				if err == nil {
					err = errors.New("trailing Stratum parameter data")
				}
				return stratumNotification{}, err
			}
			notification.Params = params
			return notification, nil
		}
		if notification.Method == "client.get_version" && idJSON != nil {
			id, err := decodeJSONAny(idJSON)
			if err != nil {
				return stratumNotification{}, err
			}
			notification.ID = id
			return notification, nil
		}

		offset = skipJSONSpace(data, valueEnd)
		if offset < len(data) && data[offset] == ',' {
			offset++
		}
	}
	return notification, nil
}

func decodeJSONString(data []byte) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	return value, nil
}

func decodeJSONAny(data []byte) (any, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func skipJSONSpace(data []byte, offset int) int {
	for offset < len(data) {
		switch data[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
		default:
			return offset
		}
	}
	return offset
}

func jsonStringEnd(data []byte, start int) int {
	if start >= len(data) || data[start] != '"' {
		return -1
	}
	escaped := false
	for offset := start + 1; offset < len(data); offset++ {
		if escaped {
			escaped = false
			continue
		}
		switch data[offset] {
		case '\\':
			escaped = true
		case '"':
			return offset + 1
		}
	}
	return -1
}

func jsonValueEnd(data []byte, start int) int {
	if start >= len(data) {
		return -1
	}
	if data[start] == '"' {
		return jsonStringEnd(data, start)
	}
	depth := 0
	inString := false
	escaped := false
	for offset := start; offset < len(data); offset++ {
		character := data[offset]
		if inString {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '[', '{':
			depth++
		case ']', '}':
			if depth == 0 {
				return offset
			}
			depth--
			if depth == 0 {
				return offset + 1
			}
		case ',':
			if depth == 0 {
				return offset
			}
		}
	}
	return -1
}

// notifyParams extracts the previous-block hash and clean-jobs flag while
// retaining zero-copy slices for the two coinbase strings. Those strings are
// decoded only after a new block transition is accepted, so same-block job
// updates do not allocate or copy their potentially large coinbases.
type notifyParams struct {
	previousHash  string
	clean         bool
	count         int
	coinbase1JSON []byte
	coinbase2JSON []byte
}

func (params *notifyParams) UnmarshalJSON(data []byte) error {
	parsed, consumed, err := parseNotifyParams(data)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data[consumed:])) != 0 {
		return errors.New("trailing Stratum parameter data")
	}
	*params = parsed
	return nil
}

func parseNotifyParams(data []byte) (notifyParams, int, error) {
	var params notifyParams
	data = bytes.TrimSpace(data)
	if len(data) < 2 || data[0] != '[' {
		return params, 0, errors.New("invalid Stratum parameter array")
	}
	start := 1
	depth := 0
	inString := false
	escaped := false
	for index := 1; index < len(data); index++ {
		character := data[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '[', '{':
			depth++
		case '}':
			if depth == 0 {
				return params, 0, errors.New("invalid Stratum parameter nesting")
			}
			depth--
		case ']':
			if depth > 0 {
				depth--
				continue
			}
			if len(bytes.TrimSpace(data[start:index])) > 0 {
				if err := consumeNotifyParam(&params, data[start:index]); err != nil {
					return params, 0, err
				}
			}
			return params, index + 1, nil
		case ',':
			if depth == 0 {
				if err := consumeNotifyParam(&params, data[start:index]); err != nil {
					return params, 0, err
				}
				start = index + 1
			}
		}
	}
	return params, 0, errors.New("unterminated Stratum parameter array")
}

func consumeNotifyParam(params *notifyParams, value []byte) error {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return errors.New("empty Stratum parameter")
	}
	switch params.count {
	case 1:
		previousHash, err := decodeNotifyHash(value)
		if err != nil {
			return err
		}
		params.previousHash = previousHash
	case 2:
		params.coinbase1JSON = value
	case 3:
		params.coinbase2JSON = value
	case 8:
		clean, err := decodeNotifyClean(value)
		if err != nil {
			return err
		}
		params.clean = clean
	}
	params.count++
	return nil
}

func decodeNotifyHash(value []byte) (string, error) {
	if len(value) == 66 && value[0] == '"' && value[len(value)-1] == '"' && !bytes.ContainsRune(value, '\\') {
		return string(value[1 : len(value)-1]), nil
	}
	var hash string
	if err := json.Unmarshal(value, &hash); err != nil {
		return "", err
	}
	return hash, nil
}

func decodeNotifyClean(value []byte) (bool, error) {
	switch string(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		var clean bool
		if err := json.Unmarshal(value, &clean); err != nil {
			return false, err
		}
		return clean, nil
	}
}

func (params notifyParams) coinbaseSource(extraNonce1 string, extraNonce2Size int, workerScriptSHA256 string) *model.CoinbaseSource {
	if len(params.coinbase1JSON) == 0 || len(params.coinbase2JSON) == 0 || extraNonce1 == "" || extraNonce2Size <= 0 || workerScriptSHA256 == "" {
		return nil
	}
	var coinbase1, coinbase2 string
	if json.Unmarshal(params.coinbase1JSON, &coinbase1) != nil || json.Unmarshal(params.coinbase2JSON, &coinbase2) != nil || coinbase1 == "" || coinbase2 == "" {
		return nil
	}
	return &model.CoinbaseSource{
		Coinbase1: coinbase1, Coinbase2: coinbase2,
		ExtraNonce1: extraNonce1, ExtraNonce2Size: extraNonce2Size,
		WorkerScriptSHA256: workerScriptSHA256,
	}
}

func validBlockHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') && (character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

type notifyWindow struct {
	previous string
}

// accept rejects the initial current-block job and every update for it. A
// measurement window opens only after this connection observes a clean
// previous-hash transition, so startup timing cannot masquerade as block
// propagation latency.
func (window *notifyWindow) accept(previousHash string, clean bool) bool {
	if window.previous == "" {
		window.previous = previousHash
		return false
	}
	if previousHash == window.previous || !clean {
		return false
	}
	window.previous = previousHash
	return true
}

// recordBlockEvent keeps only the earliest timestamped transition for an
// endpoint. Later jobs for the same block never reach this point.
func recordBlockEvent(block *activeBlock, e event) {
	id := e.connectionID
	if id == "" {
		id = e.poolID
	}
	if e.prevHash == "" {
		return
	}
	if block.started.IsZero() || e.at.Before(block.started) {
		block.started = e.at
	}
	if !block.started.IsZero() && e.at.After(block.started.Add(blockWindow)) {
		return
	}
	if old, exists := block.arrivals[id]; !exists || e.at.Before(old) {
		block.arrivals[id] = e.at
		if e.coinbase != nil {
			if block.coinbases == nil {
				block.coinbases = make(map[string]model.CoinbaseSource)
			}
			block.coinbases[id] = *e.coinbase
		} else {
			delete(block.coinbases, id)
		}
	}
}

func endpointConnectionID(poolID, address string, tls bool) string {
	return poolID + "/" + address + "/tls=" + strconv.FormatBool(tls)
}

func publishProtocol(ctx context.Context, out chan<- event, poolID string, endpoint model.Endpoint, method string, started time.Time, status, errorCategory string) error {
	return publishProtocolAt(ctx, out, poolID, endpoint, method, started, time.Now(), status, errorCategory)
}

func publishProtocolAt(ctx context.Context, out chan<- event, poolID string, endpoint model.Endpoint, method string, started, finished time.Time, status, errorCategory string) error {
	duration := float64(finished.Sub(started).Nanoseconds()) / float64(time.Millisecond)
	record := model.ProtocolSample{
		ObservedAt:     finished.UTC(),
		DurationMS:     duration,
		ResponseStatus: status,
		ErrorCategory:  errorCategory,
	}
	address := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
	select {
	case out <- event{
		poolID: poolID, connectionID: endpointConnectionID(poolID, address, endpoint.TLS),
		protocolMethod: method, protocol: &record,
	}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func protocolErrorStatus(err error) string {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return model.ProtocolStatusTimeout
	}
	return model.ProtocolStatusError
}

func tlsErrorCategory(err error) string {
	var verificationError *tls.CertificateVerificationError
	if errors.As(err, &verificationError) {
		return model.ProtocolErrorTLSCertificateInvalid
	}
	return "tls_handshake_failed"
}

func request(w *bufio.Writer, id int, method string, params any, style stratumWireStyle) error {
	methodJSON, err := json.Marshal(method)
	if err != nil {
		return err
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var b []byte
	if style == stratumWireSpaced {
		b = fmt.Appendf(nil, `{"id": %d, "method": %s, "params": %s}`, id, methodJSON, paramsJSON)
	} else {
		b = fmt.Appendf(nil, `{"id":%d,"method":%s,"params":%s}`, id, methodJSON, paramsJSON)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}

func response(w *bufio.Writer, id any, result any) error {
	b, err := json.Marshal(map[string]any{"id": id, "result": result, "error": nil})
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}

func awaitResponse(ctx context.Context, conn net.Conn, r *bufio.Reader, w *bufio.Writer, agent string, id int, timeout time.Duration) (json.RawMessage, any, time.Time, error) {
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, nil, time.Now(), err
		}
		line, receivedAt, err := readStratumMessage(r)
		if err != nil {
			return nil, nil, receivedAt, err
		}
		var msg struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  any             `json:"error"`
		}
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if msg.Method == "client.get_version" {
			if err := response(w, msg.ID, agent); err != nil {
				return nil, nil, time.Now(), err
			}
			continue
		}
		if responseID(msg.ID) != id {
			continue
		}
		return msg.Result, msg.Error, receivedAt, nil
	}
}

func readStratumMessage(reader *bufio.Reader) ([]byte, time.Time, error) {
	// Peek blocks only until the first byte is readable. Capture the timestamp
	// there instead of after ReadSlice has waited for a potentially large
	// newline-delimited notification to finish arriving.
	if _, err := reader.Peek(1); err != nil {
		return nil, time.Now(), err
	}
	receivedAt := time.Now()
	// A shared-CPU Machine can execute only one endpoint goroutine at a time.
	// Yield after recording the first-byte boundary so other ready connections
	// can record their arrivals before this goroutine reads and parses its full
	// notification.
	runtime.Gosched()
	fragment, err := reader.ReadSlice('\n')
	if len(fragment) > maxStratumMessageSize {
		return nil, receivedAt, errStratumMessageTooLarge
	}
	if err == nil {
		return fragment, receivedAt, nil
	}
	if !errors.Is(err, bufio.ErrBufferFull) {
		return nil, receivedAt, err
	}
	capacity := largeMessageCapacity
	if len(fragment) > capacity {
		capacity = len(fragment)
	}
	message := make([]byte, len(fragment), capacity)
	copy(message, fragment)
	for {
		fragment, err = reader.ReadSlice('\n')
		if len(message)+len(fragment) > maxStratumMessageSize {
			return nil, receivedAt, errStratumMessageTooLarge
		}
		message = append(message, fragment...)
		if err == nil {
			return message, receivedAt, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, receivedAt, err
		}
	}
}

func randomizedDuration(minimum, jitter time.Duration) (time.Duration, error) {
	if jitter <= 0 {
		return minimum, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(jitter)+1))
	if err != nil {
		return 0, err
	}
	return minimum + time.Duration(n.Int64()), nil
}

func dialPublicEndpoint(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid endpoint address")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(dialCtx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve endpoint: %w", err)
	}
	dialer := net.Dialer{}
	var lastErr error
	for _, candidate := range addresses {
		if !isPublicEndpointAddress(candidate) {
			continue
		}
		connection, err := dialer.DialContext(dialCtx, network, net.JoinHostPort(candidate.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, fmt.Errorf("connect to endpoint: %w", lastErr)
	}
	return nil, errors.New("endpoint has no public network address")
}

func isPublicEndpointAddress(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate() &&
		!address.IsLoopback() && !address.IsUnspecified() && !address.IsLinkLocalUnicast() &&
		!address.IsLinkLocalMulticast() && !sharedAddressRange.Contains(address)
}

func responseID(value any) int {
	switch v := value.(type) {
	case float64:
		if v < 0 || math.Trunc(v) != v || v >= float64(^uint(0)>>1) {
			return -1
		}
		return int(v)
	case string:
		id, err := strconv.Atoi(v)
		if err != nil || id < 0 {
			return -1
		}
		return id
	default:
		return -1
	}
}
