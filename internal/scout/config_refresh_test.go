package scout

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
	"github.com/M45Core/StratumScout/internal/probe"
)

func TestRunRefreshesConfigurationWithoutRetryingBlocks(t *testing.T) {
	for _, firstStatus := range []int{http.StatusAccepted, http.StatusUnprocessableEntity} {
		t.Run(fmt.Sprint(firstStatus), func(t *testing.T) {
			oldBody := encodedProbeConfig(t, []ProbePool{{ID: "pool", Endpoints: []ProbeEndpoint{{Host: "old.example", Port: 3333}}}})
			newBody := encodedProbeConfig(t, []ProbePool{{ID: "pool", Endpoints: []ProbeEndpoint{{Host: "new.example", Port: 4444}}}})
			var oldConfig, newConfig ProbeConfig
			json.Unmarshal(oldBody, &oldConfig)
			json.Unmarshal(newBody, &newConfig)
			var mu sync.Mutex
			gets := 0
			var uploads []envelope
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/api/v1/probe-config" {
					gets++
					if gets == 1 {
						w.Write(oldBody)
					} else {
						w.Write(newBody)
					}
					return
				}
				reader, err := gzip.NewReader(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				defer reader.Close()
				var value envelope
				if err := json.NewDecoder(reader).Decode(&value); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				uploads = append(uploads, value)
				if value.ConfigRevision == oldConfig.ConfigRevision && firstStatus != http.StatusAccepted {
					http.Error(w, "unknown or expired configuration revision", firstStatus)
					return
				}
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprintf(w, `{"batch_id":%q,"accepted":1}`, value.BatchID)
			}))
			defer server.Close()
			cfg := testUploaderConfig(server, []byte(strings.Repeat("k", 32)))
			cfg.Continuous = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := run(ctx, cfg, func(ctx context.Context, pools []model.Pool, emit func(model.BlockSample) error) error {
				calls++
				if calls > 2 {
					t.Fatal("unexpected extra session restart")
				}
				wantHost := "old.example"
				if calls == 2 {
					wantHost = "new.example"
				}
				if pools[0].Endpoints[0].Host != wantHost {
					t.Fatalf("pools=%+v, want %s", pools, wantHost)
				}
				if calls == 1 {
					// Two overlapping windows must both keep the original revision.
					for _, hash := range []string{"a", "b"} {
						sample := testBlockSample(time.Now())
						sample.BlockID = strings.Repeat(hash, 64)
						if err := emit(sample); !errors.Is(err, probe.ErrConnectionRefresh) {
							t.Fatalf("refresh request=%v", err)
						}
					}
					return probe.ErrConnectionRefresh
				}
				sample := testBlockSample(time.Now())
				sample.BlockID = strings.Repeat("c", 64)
				if err := emit(sample); err != nil {
					t.Fatal(err)
				}
				cancel()
				return ctx.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if calls != 2 || gets != 3 || len(uploads) != 3 {
				t.Fatalf("sessions=%d config requests=%d uploads=%d", calls, gets, len(uploads))
			}
			for i, value := range uploads {
				wantRevision := oldConfig.ConfigRevision
				if i == 2 {
					wantRevision = newConfig.ConfigRevision
				}
				if value.ConfigRevision != wantRevision || value.Sample.BlockID != strings.Repeat(string(rune('a'+i)), 64) {
					t.Fatalf("upload %d: revision=%s block=%s", i, value.ConfigRevision, value.Sample.BlockID)
				}
			}
		})
	}
}

func TestRunKeepsSessionsUntilValidConfigurationChanges(t *testing.T) {
	for _, refresh := range []string{"unavailable", "invalid", "unchanged"} {
		t.Run(refresh, func(t *testing.T) {
			oldBody := encodedProbeConfig(t, []ProbePool{{ID: "pool", Endpoints: []ProbeEndpoint{{Host: "old.example", Port: 3333}}}})
			newBody := encodedProbeConfig(t, []ProbePool{{ID: "pool", Endpoints: []ProbeEndpoint{{Host: "new.example", Port: 4444}}}})
			var mu sync.Mutex
			gets, posts := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/api/v1/probe-config" {
					gets++
					switch {
					case gets == 1:
						w.Write(oldBody)
					case gets >= 4:
						w.Write(newBody)
					case refresh == "unavailable":
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
					case refresh == "invalid":
						w.Write([]byte(strings.Replace(string(newBody), "new.example", "tampered.example", 1)))
					default:
						w.Write(oldBody)
					}
					return
				}
				posts++
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer server.Close()
			cfg := testUploaderConfig(server, []byte(strings.Repeat("k", 32)))
			cfg.Continuous = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := run(ctx, cfg, func(ctx context.Context, pools []model.Pool, emit func(model.BlockSample) error) error {
				calls++
				if calls == 2 {
					if pools[0].Endpoints[0].Host != "new.example" {
						t.Fatalf("new config not applied: %+v", pools)
					}
					cancel()
					return ctx.Err()
				}
				if calls > 2 {
					t.Fatal("unexpected extra session restart")
				}
				for i := 0; i < 3; i++ {
					sample := testBlockSample(time.Now())
					sample.BlockID = strings.Repeat(string(rune('a'+i)), 64)
					err := emit(sample)
					if i < 2 && err != nil {
						t.Fatalf("sessions disturbed by %s config: %v", refresh, err)
					}
					if i == 2 && !errors.Is(err, probe.ErrConnectionRefresh) {
						t.Fatalf("valid revision change ignored: %v", err)
					}
				}
				return probe.ErrConnectionRefresh
			})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if calls != 2 || gets != 4 || posts != 3 {
				t.Fatalf("sessions=%d config requests=%d uploads=%d", calls, gets, posts)
			}
		})
	}
}
