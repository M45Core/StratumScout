package scout

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Distortions81/StratumScout/internal/model"
)

const (
	batchSize      = 100
	queueLimit     = 2000
	batchInterval  = 5 * time.Second
	initialBackoff = 250 * time.Millisecond
	maxBackoff     = 5 * time.Second
)

type envelope struct {
	SchemaVersion  int                 `json:"schema_version"`
	BatchID        string              `json:"batch_id"`
	RunID          string              `json:"run_id"`
	AgentVersion   string              `json:"agent_version"`
	ConfigRevision string              `json:"config_revision"`
	Region         string              `json:"region"`
	Vantage        string              `json:"vantage"`
	MachineID      string              `json:"machine_id"`
	StartedAt      time.Time           `json:"started_at"`
	SentAt         time.Time           `json:"sent_at"`
	Observations   []model.Observation `json:"observations"`
}

type uploadStats struct {
	Uploaded int
	Dropped  int
	Failed   bool
}

type uploader struct {
	cfg           Config
	runID         string
	startedAt     time.Time
	revision      string
	ingestURL     *url.URL
	wake          chan struct{}
	stop          chan struct{}
	done          chan struct{}
	mu            sync.Mutex
	queue         []model.Observation
	sequence      uint64
	batchSequence uint64
	uploaded      int
	dropped       int
	failed        bool
	closed        bool
}

func newUploader(ctx context.Context, cfg Config, runID string, startedAt time.Time, revision string) *uploader {
	u := &uploader{
		cfg:       cfg,
		runID:     runID,
		startedAt: startedAt,
		revision:  revision,
		ingestURL: cfg.CollectorURL.ResolveReference(&url.URL{Path: "/api/v1/ingest"}),
		wake:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go u.loop(ctx)
	return u
}

func (u *uploader) enqueue(records []model.Observation) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return errors.New("uploader is closed")
	}
	for _, record := range records {
		u.sequence++
		record.ObservationID = sequenceID(u.runID, u.sequence)
		record.RunID = u.runID
		if len(u.queue) == queueLimit {
			copy(u.queue, u.queue[1:])
			u.queue[len(u.queue)-1] = record
			u.dropped++
			u.failed = true
		} else {
			u.queue = append(u.queue, record)
		}
	}
	if len(u.queue) >= batchSize {
		select {
		case u.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (u *uploader) loop(ctx context.Context) {
	defer close(u.done)
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			u.dropRemaining()
			return
		case <-ticker.C:
			u.flushOne(ctx)
		case <-u.wake:
			u.flushOne(ctx)
		case <-u.stop:
			for u.pending() > 0 && ctx.Err() == nil {
				u.flushOne(ctx)
			}
			if ctx.Err() != nil {
				u.dropRemaining()
			}
			return
		}
	}
}

func (u *uploader) flushOne(ctx context.Context) {
	batch := u.takeBatch()
	if len(batch) == 0 {
		return
	}
	if err := u.postWithRetry(ctx, batch); err != nil {
		log.Print("upload batch failed permanently")
		u.mu.Lock()
		u.dropped += len(batch)
		u.failed = true
		u.mu.Unlock()
	}
}

func (u *uploader) takeBatch() []model.Observation {
	u.mu.Lock()
	defer u.mu.Unlock()
	count := len(u.queue)
	if count > batchSize {
		count = batchSize
	}
	batch := append([]model.Observation(nil), u.queue[:count]...)
	copy(u.queue, u.queue[count:])
	u.queue = u.queue[:len(u.queue)-count]
	return batch
}

func (u *uploader) pending() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.queue)
}

func (u *uploader) dropRemaining() {
	u.mu.Lock()
	u.dropped += len(u.queue)
	u.failed = u.failed || len(u.queue) > 0
	u.queue = nil
	u.mu.Unlock()
}

func (u *uploader) closeAndFlush() uploadStats {
	u.mu.Lock()
	if !u.closed {
		u.closed = true
		close(u.stop)
	}
	u.mu.Unlock()
	<-u.done
	return u.stats()
}

func (u *uploader) stats() uploadStats {
	u.mu.Lock()
	defer u.mu.Unlock()
	return uploadStats{Uploaded: u.uploaded, Dropped: u.dropped, Failed: u.failed}
}

func (u *uploader) uploadFinal(ctx context.Context, record model.Observation) error {
	u.mu.Lock()
	u.sequence++
	record.ObservationID = sequenceID(u.runID, u.sequence)
	record.RunID = u.runID
	u.mu.Unlock()
	return u.postWithRetry(ctx, []model.Observation{record})
}

func (u *uploader) postWithRetry(ctx context.Context, records []model.Observation) error {
	u.mu.Lock()
	u.batchSequence++
	batchID := u.runID + "-batch-" + strconv.FormatUint(u.batchSequence, 10)
	u.mu.Unlock()
	sentAt := time.Now().UTC()
	payload, err := encodeEnvelope(envelope{
		SchemaVersion:  1,
		BatchID:        batchID,
		RunID:          u.runID,
		AgentVersion:   AgentVersion,
		ConfigRevision: u.revision,
		Region:         u.cfg.Region,
		Vantage:        u.cfg.Vantage,
		MachineID:      u.cfg.MachineID,
		StartedAt:      u.startedAt,
		SentAt:         sentAt,
		Observations:   records,
	})
	if err != nil {
		return err
	}
	backoff := initialBackoff
	for {
		accepted, retry, err := u.post(ctx, payload)
		if err == nil {
			if accepted < 0 {
				// The collector has already appended this exact batch but its
				// original acknowledgement was lost. It has no response count
				// on the duplicate path, so retain the known batch size locally.
				accepted = len(records)
			}
			u.mu.Lock()
			u.uploaded += accepted
			u.mu.Unlock()
			return nil
		}
		if !retry {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func encodeEnvelope(value envelope) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(encoded); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func (u *uploader) post(ctx context.Context, payload []byte) (accepted int, retry bool, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.ingestURL.String(), bytes.NewReader(payload))
	if err != nil {
		return 0, false, err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, u.cfg.Secret)
	_, _ = io.WriteString(mac, timestamp+"\n")
	_, _ = mac.Write(payload)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("X-StratumStats-Key-ID", u.cfg.KeyID)
	request.Header.Set("X-StratumStats-Timestamp", timestamp)
	request.Header.Set("X-StratumStats-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := u.cfg.Client.Do(request)
	if err != nil {
		return 0, true, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusAccepted {
		accepted, err := parseAccepted(response.Body)
		if err != nil {
			return 0, true, fmt.Errorf("decode collector acknowledgement: %w", err)
		}
		return accepted, false, nil
	}
	if response.StatusCode == http.StatusConflict {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return -1, false, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	err = fmt.Errorf("collector returned HTTP %d", response.StatusCode)
	return 0, response.StatusCode >= 500, err
}
