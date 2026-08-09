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

	"github.com/Distortions81/StratumScout/internal/model"
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
