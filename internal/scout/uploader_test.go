package scout

import (
	"bytes"
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
		fmt.Fprintf(response, `{"batch_id":%q,"accepted":1}`, got.BatchID)
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

func TestUploaderRetriesMismatchedAcknowledgement(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if attempts.Add(1) == 1 {
			response.WriteHeader(http.StatusAccepted)
			fmt.Fprint(response, `{"batch_id":"wrong-batch","accepted":1}`)
			return
		}
		http.Error(response, "duplicate batch", http.StatusConflict)
	}))
	defer server.Close()
	collectorURL, _ := url.Parse(server.URL)
	cfg := Config{CollectorURL: collectorURL, KeyID: "current", Secret: []byte(strings.Repeat("k", 32)), Region: "lax", Vantage: "us-west", MachineID: "test", Client: server.Client()}
	started := time.Now().UTC()
	u := newUploader(context.Background(), cfg, "run-test", started, "sha256:"+strings.Repeat("a", 64))
	if err := u.enqueue([]model.Observation{{Version: model.ObservationVersion, ObservedAt: started}, {Version: model.ObservationVersion, ObservedAt: started}}); err != nil {
		t.Fatal(err)
	}
	stats := u.closeAndFlush()
	if attempts.Load() != 2 || stats.Uploaded != 2 || stats.Dropped != 0 || stats.Failed {
		t.Fatalf("attempts=%d stats=%+v", attempts.Load(), stats)
	}
}

func TestUploaderStopsAcceptingRecordsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	collectorURL, _ := url.Parse("https://collector.example")
	u := newUploader(ctx, Config{CollectorURL: collectorURL}, "run-test", time.Now().UTC(), "sha256:"+strings.Repeat("a", 64))
	if err := u.enqueue([]model.Observation{{Version: model.ObservationVersion}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-u.done:
	case <-time.After(time.Second):
		t.Fatal("uploader did not stop after cancellation")
	}
	if err := u.enqueue([]model.Observation{{Version: model.ObservationVersion}}); err == nil {
		t.Fatal("stopped uploader accepted another observation")
	}
	stats := u.closeAndFlush()
	if stats.Uploaded != 0 || stats.Dropped != 1 || !stats.Failed {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestUploaderSplitsEnvelopesAtCollectorSizeLimits(t *testing.T) {
	var requests atomic.Int32
	var oversized atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		compressed, err := io.ReadAll(io.LimitReader(request.Body, maxCompressedEnvelopeBytes+1))
		if err != nil || len(compressed) > maxCompressedEnvelopeBytes {
			oversized.Store(true)
			http.Error(response, "compressed request too large", http.StatusRequestEntityTooLarge)
			return
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			http.Error(response, "invalid gzip", http.StatusBadRequest)
			return
		}
		decompressed, err := io.ReadAll(io.LimitReader(reader, maxDecompressedEnvelopeBytes+1))
		_ = reader.Close()
		if err != nil || len(decompressed) > maxDecompressedEnvelopeBytes {
			oversized.Store(true)
			http.Error(response, "decompressed request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var got envelope
		if err := json.Unmarshal(decompressed, &got); err != nil {
			http.Error(response, "invalid envelope", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		response.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(response, `{"batch_id":%q,"accepted":%d}`, got.BatchID, len(got.Observations))
	}))
	defer server.Close()

	observations := make([]model.Observation, batchSize)
	for observationIndex := range observations {
		outputs := make([]model.CoinbaseOutput, model.MaxRetainedCoinbaseOutputs)
		for outputIndex := range outputs {
			seed := fmt.Sprintf("%d/%d", observationIndex, outputIndex)
			first := sha256.Sum256([]byte(seed + "/first"))
			second := sha256.Sum256([]byte(seed + "/second"))
			third := sha256.Sum256([]byte(seed + "/third"))
			script := make([]byte, 0, model.MaxRetainedCoinbaseScriptBytes)
			script = append(script, first[:]...)
			script = append(script, second[:]...)
			script = append(script, third[:16]...)
			outputs[outputIndex] = model.CoinbaseOutput{ValueSats: 1, ScriptPubKey: hex.EncodeToString(script), ScriptType: "unknown"}
		}
		observations[observationIndex] = model.Observation{
			Version: model.ObservationVersion, ObservedAt: time.Now().UTC(), PoolID: "pool", Endpoint: "pool.example:3333",
			Eligible: true, Arrived: true, CoinbaseAnalyzed: true, CoinbaseTotalSats: uint64(len(outputs)),
			CoinbaseOutputs: outputs, CoinbaseOutputCount: len(outputs),
		}
	}
	collectorURL, _ := url.Parse(server.URL)
	cfg := Config{CollectorURL: collectorURL, KeyID: "current", Secret: []byte(strings.Repeat("k", 32)), Region: "lax", Vantage: "us-west", MachineID: "test", Client: server.Client()}
	u := newUploader(context.Background(), cfg, "run-test", time.Now().UTC(), "sha256:"+strings.Repeat("a", 64))
	if err := u.enqueue(observations); err != nil {
		t.Fatal(err)
	}
	stats := u.closeAndFlush()
	if oversized.Load() || requests.Load() < 2 || stats.Uploaded != len(observations) || stats.Dropped != 0 || stats.Failed {
		t.Fatalf("requests=%d oversized=%t stats=%+v", requests.Load(), oversized.Load(), stats)
	}
}

func TestParseAcceptedRequiresOneCompleteAcknowledgement(t *testing.T) {
	batchID, accepted, err := parseAccepted(strings.NewReader(`{"batch_id":"run-batch-1","accepted":2}`))
	if err != nil || batchID != "run-batch-1" || accepted != 2 {
		t.Fatalf("batch=%q accepted=%d err=%v", batchID, accepted, err)
	}
	for _, body := range []string{
		`{"accepted":2}`,
		`{"batch_id":"run-batch-1","accepted":0}`,
		`{"batch_id":"run-batch-1","accepted":2}{}`,
	} {
		if _, _, err := parseAccepted(strings.NewReader(body)); err == nil {
			t.Fatalf("invalid acknowledgement accepted: %s", body)
		}
	}
}

func TestObservationQueueDropsOldestInConstantSpace(t *testing.T) {
	var queue observationQueue
	for index := 0; index < queueLimit+3; index++ {
		dropped := queue.push(model.Observation{PoolID: strconv.Itoa(index)})
		if dropped != (index >= queueLimit) {
			t.Fatalf("push %d dropped=%t", index, dropped)
		}
	}
	var records []model.Observation
	for queue.size > 0 {
		records = append(records, queue.pop(batchSize)...)
	}
	if len(records) != queueLimit || records[0].PoolID != "3" || records[len(records)-1].PoolID != strconv.Itoa(queueLimit+2) {
		t.Fatalf("retained %d records from %q through %q", len(records), records[0].PoolID, records[len(records)-1].PoolID)
	}
}

func BenchmarkEncodeEnvelope(b *testing.B) {
	started := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	observations := make([]model.Observation, batchSize)
	for index := range observations {
		duration := 12.5 + float64(index)
		observations[index] = model.Observation{
			Version: model.ObservationVersion, ObservationID: fmt.Sprintf("run/observation-%d", index),
			RunID: "run", ObservedAt: started, PoolID: "pool", Endpoint: "pool.example:3333",
			RecordType: model.RecordTypeProtocol, ProtocolMethod: model.ProtocolPing,
			ResponseStatus: model.ProtocolStatusOK, DurationMS: &duration,
		}
	}
	value := envelope{
		SchemaVersion: 1, BatchID: "run-batch-1", RunID: "run", AgentVersion: AgentVersion,
		ConfigRevision: "sha256:" + strings.Repeat("a", 64), Region: "lax", Vantage: "us-west",
		MachineID: "machine", StartedAt: started, SentAt: started.Add(time.Minute), Observations: observations,
	}
	var compressed bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodeEnvelopeInto(&compressed, value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObservationQueueAtCapacity(b *testing.B) {
	queue := observationQueue{entries: make([]model.Observation, queueLimit), size: queueLimit}
	record := model.Observation{PoolID: "pool", Endpoint: "pool.example:3333"}
	b.ReportAllocs()
	for b.Loop() {
		queue.push(record)
	}
}
