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
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

const (
	blockEnvelopeVersion         = 3
	maxCompressedEnvelopeBytes   = 256 << 10
	maxDecompressedEnvelopeBytes = 1 << 20
)

var errEnvelopeTooLarge = errors.New("encoded block sample exceeds collector limits")

// Envelope version 3 carries one Bitcoin block sample plus only the coinbase
// source needed by the webpage. There is deliberately no observation array,
// run summary, queue sequence, or time-based batch.
type envelope struct {
	SchemaVersion    int               `json:"schema_version"`
	BatchID          string            `json:"batch_id"`
	ConfigRevision   string            `json:"config_revision"`
	Region           string            `json:"region"`
	FilterContinents bool              `json:"filter_continents,omitempty"`
	Sample           model.BlockSample `json:"sample"`
}

type postResult struct {
	batchID  string
	accepted int
}

type uploader struct {
	cfg       Config
	revision  string
	ingestURL *url.URL
}

func newUploader(cfg Config, revision string) *uploader {
	return &uploader{
		cfg: cfg, revision: revision,
		ingestURL: cfg.CollectorURL.ResolveReference(&url.URL{Path: "/api/v1/ingest"}),
	}
}

// uploadBlock performs one bounded attempt for one Bitcoin block. A failure is
// returned to the caller for logging and the sample is never retried or split.
func (u *uploader) uploadBlock(ctx context.Context, sample model.BlockSample) error {
	if sample.BlockID == "" {
		return errors.New("invalid block sample")
	}
	batchID := u.cfg.Region + "-" + sample.BlockID
	var compressed bytes.Buffer
	uncompressedBytes, err := encodeEnvelopeInto(&compressed, envelope{
		SchemaVersion: blockEnvelopeVersion, BatchID: batchID,
		ConfigRevision: u.revision, Region: u.cfg.Region, FilterContinents: u.cfg.FilterContinents,
		Sample: sample,
	})
	if err != nil {
		return err
	}
	if envelopeTooLarge(compressed.Len(), uncompressedBytes) {
		return fmt.Errorf("%w: compressed=%d decompressed=%d", errEnvelopeTooLarge, compressed.Len(), uncompressedBytes)
	}
	result, err := u.post(ctx, compressed.Bytes())
	if err != nil {
		return err
	}
	if result.accepted >= 0 && (result.batchID != batchID || result.accepted != 1) {
		return errors.New("collector acknowledgement does not match block sample")
	}
	return nil
}

func envelopeTooLarge(compressedBytes, uncompressedBytes int) bool {
	return compressedBytes > maxCompressedEnvelopeBytes || uncompressedBytes > maxDecompressedEnvelopeBytes
}

type countingWriter struct {
	writer  io.Writer
	written int
}

func (w *countingWriter) Write(value []byte) (int, error) {
	count, err := w.writer.Write(value)
	w.written += count
	return count, err
}

func encodeEnvelopeInto(compressed *bytes.Buffer, value envelope) (int, error) {
	compressed.Reset()
	writer := gzip.NewWriter(compressed)
	counter := &countingWriter{writer: writer}
	if err := json.NewEncoder(counter).Encode(value); err != nil {
		_ = writer.Close()
		return counter.written, err
	}
	if err := writer.Close(); err != nil {
		return counter.written, err
	}
	return counter.written, nil
}

func (u *uploader) post(ctx context.Context, payload []byte) (postResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.ingestURL.String(), bytes.NewReader(payload))
	if err != nil {
		return postResult{}, err
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
		return postResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusAccepted {
		batchID, accepted, err := parseAccepted(response.Body)
		if err != nil {
			return postResult{}, fmt.Errorf("decode collector acknowledgement: %w", err)
		}
		return postResult{batchID: batchID, accepted: accepted}, nil
	}
	if response.StatusCode == http.StatusConflict {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return postResult{accepted: -1}, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return postResult{}, fmt.Errorf("collector returned HTTP %d", response.StatusCode)
}
