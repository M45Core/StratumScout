package scout

import (
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

func TestUploaderSignsCompressedEnvelopeAndRetriesServerFailure(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(response, "retry", http.StatusServiceUnavailable)
			return
		}
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		timestamp := request.Header.Get("X-StratumStats-Timestamp")
		if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
			t.Fatalf("timestamp=%q", timestamp)
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = io.WriteString(mac, timestamp+"\n")
		_, _ = mac.Write(raw)
		if request.Header.Get("X-StratumStats-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			t.Fatal("invalid signature")
		}
		reader, err := gzip.NewReader(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		var got envelope
		if err := json.NewDecoder(reader).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Region != "lax" || got.Vantage != "us-west" || len(got.Observations) != 1 || got.Observations[0].RunID != got.RunID {
			t.Fatalf("envelope=%+v", got)
		}
		response.WriteHeader(http.StatusAccepted)
		fmt.Fprint(response, `{"accepted":1}`)
	}))
	defer server.Close()
	collectorURL, _ := url.Parse(server.URL)
	cfg := Config{
		CollectorURL: collectorURL,
		KeyID:        "current", Secret: secret,
		Region: "lax", Vantage: "us-west", MachineID: "test-machine",
		Client: server.Client(),
	}
	started := time.Now().UTC()
	u := newUploader(context.Background(), cfg, "run-test", started, "sha256:"+strings.Repeat("a", 64))
	if err := u.enqueue([]model.Observation{{Version: model.ObservationVersion, ObservedAt: started, RecordType: model.RecordTypeProbeRun, RunStartedAt: &started, RunStatus: "ok"}}); err != nil {
		t.Fatal(err)
	}
	stats := u.closeAndFlush()
	if attempts.Load() != 2 || stats.Uploaded != 1 || stats.Dropped != 0 || stats.Failed {
		t.Fatalf("attempts=%d stats=%+v", attempts.Load(), stats)
	}
}

func TestUploaderDoesNotRetryClientFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		http.Error(response, "bad", http.StatusUnprocessableEntity)
	}))
	defer server.Close()
	collectorURL, _ := url.Parse(server.URL)
	cfg := Config{CollectorURL: collectorURL, KeyID: "current", Secret: []byte(strings.Repeat("k", 32)), Region: "lax", Vantage: "us-west", MachineID: "test", Client: server.Client()}
	started := time.Now().UTC()
	u := newUploader(context.Background(), cfg, "run-test", started, "sha256:"+strings.Repeat("a", 64))
	if err := u.enqueue([]model.Observation{{Version: model.ObservationVersion, ObservedAt: started}}); err != nil {
		t.Fatal(err)
	}
	stats := u.closeAndFlush()
	if attempts.Load() != 1 || stats.Dropped != 1 || !stats.Failed {
		t.Fatalf("attempts=%d stats=%+v", attempts.Load(), stats)
	}
}

func TestUploaderTreatsRateLimitAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Retry-After", "1")
		http.Error(response, "too many requests", http.StatusTooManyRequests)
	}))
	defer server.Close()
	collectorURL, _ := url.Parse(server.URL)
	u := &uploader{
		cfg: Config{
			KeyID: "current", Secret: []byte(strings.Repeat("k", 32)),
			Client: server.Client(),
		},
		ingestURL: collectorURL.ResolveReference(&url.URL{Path: "/api/v1/ingest"}),
	}
	result, err := u.post(context.Background(), []byte("payload"))
	if err == nil || !result.retry || result.retryAfter != time.Second {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRetryAfterDelayIsBounded(t *testing.T) {
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	if got := retryAfterDelay("1", now); got != time.Second {
		t.Fatalf("one-second delay = %s", got)
	}
	if got := retryAfterDelay("3600", now); got != maxRetryAfter {
		t.Fatalf("large delay = %s, want %s", got, maxRetryAfter)
	}
	if got := retryAfterDelay(now.Add(2*time.Second).Format(http.TimeFormat), now); got != 2*time.Second {
		t.Fatalf("HTTP-date delay = %s", got)
	}
}

func TestUploaderTreatsDuplicateBatchAsDelivered(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		http.Error(response, "duplicate batch", http.StatusConflict)
	}))
	defer server.Close()
	collectorURL, _ := url.Parse(server.URL)
	cfg := Config{CollectorURL: collectorURL, KeyID: "current", Secret: []byte(strings.Repeat("k", 32)), Region: "lax", Vantage: "us-west", MachineID: "test", Client: server.Client()}
	started := time.Now().UTC()
	u := newUploader(context.Background(), cfg, "run-test", started, "sha256:"+strings.Repeat("a", 64))
	if err := u.enqueue([]model.Observation{{Version: model.ObservationVersion, ObservedAt: started}}); err != nil {
		t.Fatal(err)
	}
	stats := u.closeAndFlush()
	if attempts.Load() != 1 || stats.Uploaded != 1 || stats.Dropped != 0 || stats.Failed {
		t.Fatalf("attempts=%d stats=%+v", attempts.Load(), stats)
	}
}
