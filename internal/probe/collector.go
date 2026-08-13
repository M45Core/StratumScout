package probe

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

const (
	blockWindow           = 30 * time.Second
	activeBlockLimit      = 32
	completedBlockLimit   = 256
	connectionRefreshMin  = 105 * time.Minute
	connectionRefreshSpan = 30 * time.Minute
	maxStratumMessageSize = 256 << 10
)

// ErrConnectionRefresh asks the long-lived Scout runner to recreate its pool
// sessions without terminating the process or its current reporting cohort.
var ErrConnectionRefresh = errors.New("refresh Stratum connections")

var (
	errPoolRejected           = errors.New("pool rejected probe")
	errStratumMessageTooLarge = errors.New("stratum message exceeds size limit")
	requestTimeout            = 30 * time.Second
	pingInitialDelayMin       = 15 * time.Second
	pingInitialDelayJitter    = 30 * time.Second
	pingIntervalMin           = 45 * time.Second
	pingIntervalJitter        = 30 * time.Second
	pingResponseWindow        = 10 * time.Second
	sessionReadTimeout        = 90 * time.Second
	dialEndpoint              = dialPublicEndpoint
	sharedAddressRange        = netip.MustParsePrefix("100.64.0.0/10")
)

type event struct {
	poolID, prevHash         string
	connectionID             string
	at                       time.Time
	hasTransactions, tls     bool
	verified                 bool
	coinbaseAnalyzed         bool
	blockHeight              uint64
	workerWalletSeen         bool
	coinbaseTotalSats        uint64
	workerPayoutSats         uint64
	coinbaseOutputs          []model.CoinbaseOutput
	coinbaseOutputCount      int
	coinbaseOutputsTruncated bool
	coinbaseOmittedSats      uint64
	estimatedPoolFeePct      *float64
	connected                *bool
	protocol                 *model.Observation
}

type endpointTarget struct {
	poolID  string
	address string
	tls     bool
}

type activeBlock struct {
	id       string
	started  time.Time
	eligible map[string]endpointTarget
	arrivals map[string]time.Time
	empty    map[string]bool
	tls      map[string]bool
	invalid  map[string]bool
	payout   map[string]event
}

// Collect connects to every configured endpoint and emits block and protocol
// observations. It submits no shares and never stores randomized credentials.
func Collect(ctx context.Context, pools []model.Pool, vantage string, emit func([]model.Observation) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	refreshAge, err := randomizedDuration(connectionRefreshMin, connectionRefreshSpan)
	if err != nil {
		return err
	}
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
	refreshEligibleAt := time.Now().Add(refreshAge)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	finish := func(r *activeBlock) error {
		observations := blockObservations(r, vantage)
		if len(observations) == 0 {
			return nil
		}
		return emit(observations)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-events:
			if !ok {
				return nil
			}
			if e.protocol != nil {
				record := *e.protocol
				record.Vantage = vantage
				if err := emit([]model.Observation{record}); err != nil {
					return err
				}
				continue
			}
			if e.connected != nil {
				continue
			}
			r := activeBlockForEvent(blocks, completedBlocks, configured, e)
			if r == nil {
				continue
			}
			recordBlockEvent(r, e)
		case now := <-ticker.C:
			completedBlock := false
			for id, r := range blocks {
				if now.Sub(r.started) >= blockWindow {
					if err := finish(r); err != nil {
						return err
					}
					completedBlockOrder = rememberCompletedBlock(completedBlocks, completedBlockOrder, id)
					delete(blocks, id)
					completedBlock = true
				}
			}
			if shouldRefreshConnections(now, refreshEligibleAt, completedBlock, len(blocks)) {
				return ErrConnectionRefresh
			}
		}
	}
}

func shouldRefreshConnections(now, eligibleAt time.Time, completedBlock bool, activeBlocks int) bool {
	return completedBlock && !now.Before(eligibleAt) && activeBlocks == 0
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

func blockObservations(block *activeBlock, vantage string) []model.Observation {
	if len(block.arrivals) < 2 {
		return nil
	}
	var first time.Time
	for _, at := range block.arrivals {
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	ids := make([]string, 0, len(block.eligible))
	for id := range block.eligible {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]model.Observation, 0, len(ids))
	for _, id := range ids {
		target := block.eligible[id]
		at, arrived := block.arrivals[id]
		payout := block.payout[id]
		observation := model.Observation{Version: model.ObservationVersion, ObservedAt: block.started.UTC(), Vantage: vantage, BlockID: block.id, BlockHeight: payout.blockHeight, PoolID: target.poolID, Endpoint: target.address, Eligible: true, Arrived: arrived, EmptyFirst: block.empty[id], TLS: target.tls}
		observation.CoinbaseAnalyzed = payout.coinbaseAnalyzed
		observation.WorkerWalletInCoinbase = payout.workerWalletSeen
		observation.CoinbaseTotalSats = payout.coinbaseTotalSats
		observation.WorkerPayoutSats = payout.workerPayoutSats
		observation.CoinbaseOutputs = payout.coinbaseOutputs
		observation.CoinbaseOutputCount = payout.coinbaseOutputCount
		observation.CoinbaseOutputsTruncated = payout.coinbaseOutputsTruncated
		observation.CoinbaseOmittedSats = payout.coinbaseOmittedSats
		observation.EstimatedPoolFeePct = payout.estimatedPoolFeePct
		if block.invalid[id] {
			observation.ErrorCategory = "invalid_job"
		}
		if arrived {
			observation.OffsetMS = float64(at.Sub(first).Microseconds()) / 1000
		}
		out = append(out, observation)
	}
	return out
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
	// A new window needs at least one structurally valid template. Bound the
	// number of simultaneous windows so hostile prev-hash churn cannot retain
	// unbounded per-endpoint evidence during the 30-second collection period.
	if !e.verified || len(blocks) >= activeBlockLimit {
		return nil
	}
	block := &activeBlock{id: e.prevHash, started: e.at, eligible: map[string]endpointTarget{}, arrivals: map[string]time.Time{}, empty: map[string]bool{}, tls: map[string]bool{}, invalid: map[string]bool{}, payout: map[string]event{}}
	for id, target := range configured {
		block.eligible[id] = target
	}
	blocks[e.prevHash] = block
	return block
}

func watchSession(ctx context.Context, poolID string, endpoint model.Endpoint, out chan<- event) error {
	identity, err := RandomIdentity()
	if err != nil {
		return err
	}
	address := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))

	connectStarted := time.Now()
	rawConn, err := dialEndpoint(ctx, "tcp", address)
	if err != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolConnect, connectStarted, protocolErrorStatus(err), "connect_failed")
		return err
	}
	stopContextClose := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stopContextClose()
	if err := publishProtocol(ctx, out, poolID, endpoint, model.ProtocolConnect, connectStarted, model.ProtocolStatusOK, ""); err != nil {
		_ = rawConn.Close()
		return err
	}
	defer rawConn.Close()

	var conn net.Conn = rawConn
	if endpoint.TLS {
		tlsConn := tls.Client(rawConn, &tls.Config{ServerName: endpoint.Host, MinVersion: tls.VersionTLS12})
		tlsStarted := time.Now()
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolTLSHandshake, tlsStarted, protocolErrorStatus(err), tlsErrorCategory(err))
			return err
		}
		if err := publishProtocol(ctx, out, poolID, endpoint, model.ProtocolTLSHandshake, tlsStarted, model.ProtocolStatusOK, ""); err != nil {
			return err
		}
		conn = tlsConn
	}

	closeOnCancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closeOnCancelDone:
		}
	}()
	defer close(closeOnCancelDone)

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	subscribeStarted := time.Now()
	if err := request(w, 1, "mining.subscribe", []string{identity.Agent}, identity.wireStyle); err != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, model.ProtocolStatusError, "subscribe_write_failed")
		return err
	}
	subscribeResult, remoteErr, err := awaitResponse(ctx, conn, r, w, identity.Agent, 1, requestTimeout)
	if err != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, protocolErrorStatus(err), "subscribe_response_failed")
		return fmt.Errorf("subscribe: %w", err)
	}
	if remoteErr != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, model.ProtocolStatusRejected, "subscribe_rejected")
		return fmt.Errorf("%w: subscription rejected", errPoolRejected)
	}
	var subscribe []json.RawMessage
	var extraNonce1 string
	var extraNonce2Size int
	if json.Unmarshal(subscribeResult, &subscribe) != nil || len(subscribe) < 3 {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, model.ProtocolStatusError, "subscribe_invalid_response")
		return fmt.Errorf("subscribe: invalid response")
	}
	if json.Unmarshal(subscribe[1], &extraNonce1) != nil || json.Unmarshal(subscribe[2], &extraNonce2Size) != nil || extraNonce1 == "" || extraNonce2Size <= 0 {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, model.ProtocolStatusError, "subscribe_invalid_extranonce")
		return fmt.Errorf("subscribe: invalid extranonce")
	}
	if err := publishProtocol(ctx, out, poolID, endpoint, model.ProtocolSubscribe, subscribeStarted, model.ProtocolStatusOK, ""); err != nil {
		return err
	}

	authorizeStarted := time.Now()
	if err := request(w, 2, "mining.authorize", []string{identity.Username, "x"}, identity.wireStyle); err != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, model.ProtocolStatusError, "authorize_write_failed")
		return err
	}
	authorizeResult, remoteErr, err := awaitResponse(ctx, conn, r, w, identity.Agent, 2, requestTimeout)
	if err != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, protocolErrorStatus(err), "authorize_response_failed")
		return fmt.Errorf("authorize: %w", err)
	}
	if remoteErr != nil {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, model.ProtocolStatusRejected, "authorize_rejected")
		return fmt.Errorf("%w: authorization rejected", errPoolRejected)
	}
	var authorized bool
	if json.Unmarshal(authorizeResult, &authorized) != nil || !authorized {
		_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, model.ProtocolStatusRejected, "authorize_rejected")
		return fmt.Errorf("%w: authorization rejected", errPoolRejected)
	}
	if err := publishProtocol(ctx, out, poolID, endpoint, model.ProtocolAuthorize, authorizeStarted, model.ProtocolStatusOK, ""); err != nil {
		return err
	}

	online := true
	connectionID := endpointConnectionID(poolID, address, endpoint.TLS)
	select {
	case out <- event{poolID: poolID, connectionID: connectionID, connected: &online}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		offline := false
		select {
		case out <- event{poolID: poolID, connectionID: connectionID, connected: &offline}:
		case <-ctx.Done():
		}
	}()

	var window notifyWindow
	pingID := 2
	pingPending := false
	pingDisabled := false
	pingStarted := time.Time{}
	pingDeadline := time.Time{}
	initialPingDelay, err := randomizedDuration(pingInitialDelayMin, pingInitialDelayJitter)
	if err != nil {
		return err
	}
	nextPing := time.Now().Add(initialPingDelay)

	for {
		now := time.Now()
		if !pingDisabled && !pingPending && !nextPing.IsZero() && !now.Before(nextPing) {
			pingID++
			pingStarted = now
			if err := request(w, pingID, model.ProtocolPing, []any{}, identity.wireStyle); err != nil {
				_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolPing, pingStarted, model.ProtocolStatusError, "ping_write_failed")
				return err
			}
			pingPending = true
			pingDeadline = now.Add(pingResponseWindow)
		}

		readDeadline := now.Add(sessionReadTimeout)
		if pingPending && pingDeadline.Before(readDeadline) {
			readDeadline = pingDeadline
		}
		if !pingDisabled && !pingPending && !nextPing.IsZero() && nextPing.Before(readDeadline) {
			readDeadline = nextPing
		}
		if err := conn.SetReadDeadline(readDeadline); err != nil {
			return err
		}
		line, err := readStratumMessage(r)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			now = time.Now()
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if pingPending && !now.Before(pingDeadline) {
					if publishErr := publishProtocol(ctx, out, poolID, endpoint, model.ProtocolPing, pingStarted, model.ProtocolStatusTimeout, "ping_timeout"); publishErr != nil {
						return publishErr
					}
					pingPending, pingDisabled = false, true
					continue
				}
				if !pingDisabled && !pingPending && !nextPing.IsZero() && !now.Before(nextPing) {
					continue
				}
			}
			if pingPending {
				_ = publishProtocol(ctx, out, poolID, endpoint, model.ProtocolPing, pingStarted, model.ProtocolStatusError, "ping_connection_closed")
			}
			return err
		}

		var msg struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params []any           `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  any             `json:"error"`
		}
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if pingPending && responseID(msg.ID) == pingID {
			status, category := model.ProtocolStatusOK, ""
			if msg.Error != nil {
				status, category = model.ProtocolStatusUnsupported, "ping_unsupported"
				pingDisabled = true
			} else {
				var pong string
				if json.Unmarshal(msg.Result, &pong) != nil || !strings.EqualFold(pong, "pong") {
					status, category = model.ProtocolStatusError, "ping_invalid_response"
					pingDisabled = true
				}
			}
			if err := publishProtocol(ctx, out, poolID, endpoint, model.ProtocolPing, pingStarted, status, category); err != nil {
				return err
			}
			pingPending = false
			if !pingDisabled {
				interval, err := randomizedDuration(pingIntervalMin, pingIntervalJitter)
				if err != nil {
					return err
				}
				nextPing = time.Now().Add(interval)
			}
			continue
		}
		if msg.Method == "client.get_version" {
			if err := response(w, msg.ID, identity.Agent); err != nil {
				return err
			}
			continue
		}
		if msg.Method != "mining.notify" || len(msg.Params) < 9 {
			continue
		}
		prev, ok := msg.Params[1].(string)
		if !ok || prev == "" {
			continue
		}
		clean, _ := msg.Params[8].(bool)
		branches, _ := msg.Params[4].([]any)
		branchStrings := make([]string, 0, len(branches))
		for _, branch := range branches {
			if value, ok := branch.(string); ok {
				branchStrings = append(branchStrings, value)
			}
		}
		if !window.accept(prev, clean, len(branches) > 0) {
			continue
		}
		job := Job{PrevHash: prev, MerkleBranches: branchStrings, ExtraNonce1: extraNonce1, ExtraNonce2Size: extraNonce2Size, WorkerScript: identity.PayoutScript}
		job.Coinbase1, _ = msg.Params[2].(string)
		job.Coinbase2, _ = msg.Params[3].(string)
		job.Version, _ = msg.Params[5].(string)
		job.Bits, _ = msg.Params[6].(string)
		job.NTime, _ = msg.Params[7].(string)
		verification := VerifyJob(job)
		e := event{poolID: poolID, connectionID: endpointConnectionID(poolID, address, endpoint.TLS), prevHash: prev, at: time.Now(), hasTransactions: len(branches) > 0, tls: endpoint.TLS, verified: verification.Valid, blockHeight: verification.BlockHeight, coinbaseAnalyzed: verification.CoinbaseAnalyzed, workerWalletSeen: verification.WorkerWalletSeen, coinbaseTotalSats: verification.CoinbaseTotalSats, workerPayoutSats: verification.WorkerPayoutSats, coinbaseOutputs: verification.CoinbaseOutputs, coinbaseOutputCount: verification.CoinbaseOutputCount, coinbaseOutputsTruncated: verification.CoinbaseOutputsTruncated, coinbaseOmittedSats: verification.CoinbaseOmittedSats, estimatedPoolFeePct: verification.EstimatedPoolFeePct}
		select {
		case out <- e:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type notifyWindow struct {
	previous           string
	active             bool
	transactionJobSent bool
}

// accept rejects the initial current-block job and every update for it. A
// measurement window opens only after this connection observes a clean
// previous-hash transition, so startup timing cannot masquerade as block
// propagation latency.
func (window *notifyWindow) accept(previousHash string, clean, hasTransactions bool) bool {
	if window.previous == "" {
		window.previous = previousHash
		return false
	}
	if previousHash != window.previous {
		if !clean {
			return false
		}
		window.previous = previousHash
		window.active = true
		window.transactionJobSent = false
	}
	if !window.active || (hasTransactions && window.transactionJobSent) {
		return false
	}
	if hasTransactions {
		window.transactionJobSent = true
	}
	return true
}

// recordBlockEvent keeps the earliest structurally valid template for an endpoint.
// A coinbase-only template is useful work and counts immediately; the presence
// of transaction branches is retained only as raw empty-first evidence.
func recordBlockEvent(block *activeBlock, e event) {
	id := e.connectionID
	if id == "" {
		id = e.poolID
	}
	if !e.hasTransactions {
		block.empty[id] = true
	}
	if !e.verified {
		block.invalid[id] = true
		return
	}
	if old, exists := block.arrivals[id]; !exists || e.at.Before(old) {
		block.arrivals[id], block.tls[id] = e.at, e.tls
		block.payout[id] = e
	}
}

func endpointConnectionID(poolID, address string, tls bool) string {
	return poolID + "/" + address + "/tls=" + strconv.FormatBool(tls)
}

func publishProtocol(ctx context.Context, out chan<- event, poolID string, endpoint model.Endpoint, method string, started time.Time, status, errorCategory string) error {
	duration := float64(time.Since(started).Nanoseconds()) / float64(time.Millisecond)
	record := model.Observation{
		Version:        model.ObservationVersion,
		RecordType:     model.RecordTypeProtocol,
		ObservedAt:     time.Now().UTC(),
		PoolID:         poolID,
		Endpoint:       net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)),
		ProtocolMethod: method,
		DurationMS:     &duration,
		ResponseStatus: status,
		TLS:            endpoint.TLS,
		ErrorCategory:  errorCategory,
	}
	select {
	case out <- event{poolID: poolID, protocol: &record}:
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

func awaitResponse(ctx context.Context, conn net.Conn, r *bufio.Reader, w *bufio.Writer, agent string, id int, timeout time.Duration) (json.RawMessage, any, error) {
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, nil, err
		}
		line, err := readStratumMessage(r)
		if err != nil {
			return nil, nil, err
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
				return nil, nil, err
			}
			continue
		}
		if responseID(msg.ID) != id {
			continue
		}
		return msg.Result, msg.Error, nil
	}
}

func readStratumMessage(reader *bufio.Reader) ([]byte, error) {
	var message []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(message)+len(fragment) > maxStratumMessageSize {
			return nil, errStratumMessageTooLarge
		}
		message = append(message, fragment...)
		if err == nil {
			return message, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
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
		return int(v)
	case string:
		id, _ := strconv.Atoi(v)
		return id
	default:
		return -1
	}
}
