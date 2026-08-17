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
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

func testBlockSample(observedAt time.Time) model.BlockSample {
	receivedAt := observedAt
	return model.BlockSample{
		BlockID: strings.Repeat("a", 64),
		EndpointSamples: []model.ForwardedEndpointSample{{
			PoolID: "pool", Endpoint: "pool.example:3333", ReceivedAt: &receivedAt,
		}},
	}
}

func testUploaderConfig(server *httptest.Server, secret []byte) Config {
	collectorURL, _ := url.Parse(server.URL)
	return Config{
		CollectorURL: collectorURL, KeyID: "current", Secret: secret,
		Region: "lax", Vantage: "us-west", Client: server.Client(),
	}
}

func TestUploaderSignsOneCompressedBlockEnvelope(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
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
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		var got envelope
		if err := json.NewDecoder(reader).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.SchemaVersion != blockEnvelopeVersion || got.Region != "lax" || !got.FilterContinents ||
			len(got.Sample.EndpointSamples) != 1 || got.Sample.BlockID != strings.Repeat("a", 64) {
			t.Fatalf("envelope=%+v", got)
		}
		response.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(response, `{"batch_id":%q,"accepted":1}`, got.BatchID)
	}))
	defer server.Close()

	cfg := testUploaderConfig(server, secret)
	cfg.FilterContinents = true
	u := newUploader(cfg, "sha256:"+strings.Repeat("a", 64))
	if err := u.uploadBlock(context.Background(), testBlockSample(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d, want 1", attempts.Load())
	}
}

func TestUploaderDoesNotRetryCollectorFailure(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				http.Error(response, "drop", status)
			}))
			defer server.Close()
			u := newUploader(testUploaderConfig(server, []byte(strings.Repeat("k", 32))), "sha256:"+strings.Repeat("a", 64))
			if err := u.uploadBlock(context.Background(), testBlockSample(time.Now().UTC())); err == nil {
				t.Fatal("collector failure unexpectedly succeeded")
			}
			if attempts.Load() != 1 {
				t.Fatalf("attempts=%d, want 1", attempts.Load())
			}
		})
	}
}

func TestUploaderDoesNotRetryClientFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(response, "bad", http.StatusUnprocessableEntity)
	}))
	defer server.Close()
	u := newUploader(testUploaderConfig(server, []byte(strings.Repeat("k", 32))), "sha256:"+strings.Repeat("a", 64))
	if err := u.uploadBlock(context.Background(), testBlockSample(time.Now().UTC())); err == nil {
		t.Fatal("client failure unexpectedly succeeded")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d, want 1", attempts.Load())
	}
}

func TestUploaderTreatsDuplicateBlockAsDelivered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "duplicate batch", http.StatusConflict)
	}))
	defer server.Close()
	u := newUploader(testUploaderConfig(server, []byte(strings.Repeat("k", 32))), "sha256:"+strings.Repeat("a", 64))
	if err := u.uploadBlock(context.Background(), testBlockSample(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
}

func TestUploaderDoesNotRetryMismatchedAcknowledgement(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		response.WriteHeader(http.StatusAccepted)
		fmt.Fprint(response, `{"batch_id":"wrong","accepted":1}`)
	}))
	defer server.Close()
	u := newUploader(testUploaderConfig(server, []byte(strings.Repeat("k", 32))), "sha256:"+strings.Repeat("a", 64))
	if err := u.uploadBlock(context.Background(), testBlockSample(time.Now().UTC())); err == nil {
		t.Fatal("mismatched acknowledgement unexpectedly succeeded")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d, want 1", attempts.Load())
	}
}

func TestUploaderRejectsOversizedBlockWithoutSplittingOrTransmitting(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(response, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	sample := testBlockSample(time.Now().UTC())
	firstReceivedAt := *sample.EndpointSamples[0].ReceivedAt
	for endpointIndex := range 100 {
		receivedAt := firstReceivedAt.Add(time.Duration(endpointIndex) * time.Millisecond)
		sample.EndpointSamples = append(sample.EndpointSamples, model.ForwardedEndpointSample{
			PoolID:   strings.Repeat(fmt.Sprintf("pool-%d", endpointIndex), 2_500),
			Endpoint: fmt.Sprintf("pool-%d.example:3333", endpointIndex), ReceivedAt: &receivedAt,
		})
	}
	u := newUploader(testUploaderConfig(server, []byte(strings.Repeat("k", 32))), "sha256:"+strings.Repeat("a", 64))
	if err := u.uploadBlock(context.Background(), sample); !errors.Is(err, errEnvelopeTooLarge) {
		t.Fatalf("error=%v, want oversized envelope", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d, want none", requests.Load())
	}
}

func TestBlockEnvelopeOmitsUnavailableFields(t *testing.T) {
	var compressed bytes.Buffer
	sample := testBlockSample(time.Now().UTC())
	_, err := encodeEnvelopeInto(&compressed, envelope{
		SchemaVersion: blockEnvelopeVersion, BatchID: "lax-" + sample.BlockID,
		ConfigRevision: "sha256:" + strings.Repeat("a", 64),
		Region:         "lax", Sample: sample,
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{
		`"observations"`, `"run_id"`, `"filter_continents"`, `"setup"`, `"coinbase"`, `"eligible_endpoints"`,
		`"observation_id"`, `"record_type"`, `"version":9`, `"offset_ms"`, `"source"`, `"vantage"`,
		`"machine_id"`, `"agent_version"`, `"sent_at"`,
		`"pool_id":""`, `"eligible":false`, `"arrived":false`, `"tls":false`,
	} {
		if bytes.Contains(raw, []byte(absent)) {
			t.Fatalf("optional field %s was serialized: %s", absent, raw)
		}
	}
}

func TestParseAcceptedRequiresOneCompleteAcknowledgement(t *testing.T) {
	batchID, accepted, err := parseAccepted(strings.NewReader(`{"batch_id":"block","accepted":1}`))
	if err != nil || batchID != "block" || accepted != 1 {
		t.Fatalf("batch=%q accepted=%d err=%v", batchID, accepted, err)
	}
}

func BenchmarkEncodeBlockEnvelope(b *testing.B) {
	started := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	sample := testBlockSample(started)
	value := envelope{
		SchemaVersion: blockEnvelopeVersion, BatchID: "lax-" + sample.BlockID,
		ConfigRevision: "sha256:" + strings.Repeat("a", 64),
		Region:         "lax", Sample: sample,
	}
	var compressed bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodeEnvelopeInto(&compressed, value); err != nil {
			b.Fatal(err)
		}
	}
}
