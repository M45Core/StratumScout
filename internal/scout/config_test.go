package scout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"COLLECTOR_URL":  "https://stats.example.com/path",
		"INGEST_KEY_ID":  "regional-test",
		"INGEST_SECRET":  strings.Repeat("s", 32),
		"FLY_REGION":     "lax",
		"FLY_MACHINE_ID": "machine-test",
		"RUN_FOR":        "30s",
	}
}

func TestLoadConfig(t *testing.T) {
	environment := validEnvironment()
	cfg, err := LoadConfig(func(key string) string { return environment[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CollectorURL.String() != "https://stats.example.com" || cfg.Vantage != "us-west" || cfg.RunFor != 30*time.Second || cfg.Continuous || cfg.ProcessNice != 0 || cfg.FilterContinents {
		t.Fatalf("collector=%q vantage=%q duration=%s continuous=%t nice=%d filter=%t", cfg.CollectorURL, cfg.Vantage, cfg.RunFor, cfg.Continuous, cfg.ProcessNice, cfg.FilterContinents)
	}
	if cfg.Client.CheckRedirect == nil || cfg.Client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("collector redirects are not disabled")
	}
	environment["FLY_REGION"] = "fra"
	cfg, err = LoadConfig(func(key string) string { return environment[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Vantage != "europe" {
		t.Fatalf("Frankfurt vantage=%q, want europe", cfg.Vantage)
	}
	environment["FLY_REGION"] = "ewr"
	cfg, err = LoadConfig(func(key string) string { return environment[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Vantage != "us-east" {
		t.Fatalf("Secaucus vantage=%q, want us-east", cfg.Vantage)
	}
	environment["FLY_REGION"] = "iad"
	cfg, err = LoadConfig(func(key string) string { return environment[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Vantage != "us-east" {
		t.Fatalf("Ashburn vantage=%q, want us-east", cfg.Vantage)
	}
	environment["FLY_REGION"] = "ord"
	if _, err := LoadConfig(func(key string) string { return environment[key] }); err == nil {
		t.Fatal("unknown region accepted")
	}
	environment["FLY_REGION"] = "lax"
	environment["FILTER_CONTINENTS"] = "true"
	cfg, err = LoadConfig(func(key string) string { return environment[key] })
	if err != nil || !cfg.FilterContinents {
		t.Fatalf("continent filtering config=%+v err=%v", cfg, err)
	}
	environment["FILTER_CONTINENTS"] = "sometimes"
	if _, err := LoadConfig(func(key string) string { return environment[key] }); err == nil {
		t.Fatal("invalid continent filter accepted")
	}
	environment["FILTER_CONTINENTS"] = ""
	environment["CONTINUOUS"] = "true"
	environment["PROCESS_NICE"] = "10"
	cfg, err = LoadConfig(func(key string) string { return environment[key] })
	if err != nil || !cfg.Continuous || cfg.ProcessNice != 10 {
		t.Fatalf("continuous config=%+v err=%v", cfg, err)
	}
	for key, value := range map[string]string{"CONTINUOUS": "sometimes", "PROCESS_NICE": "20"} {
		environment := validEnvironment()
		environment[key] = value
		if _, err := LoadConfig(func(name string) string { return environment[name] }); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
	}
}

func TestLoadConfigRejectsUnsafeIdentifiers(t *testing.T) {
	for key, value := range map[string]string{
		"INGEST_KEY_ID":  "key\ninjection",
		"FLY_MACHINE_ID": "machine\ninjection",
	} {
		environment := validEnvironment()
		environment[key] = value
		if _, err := LoadConfig(func(name string) string { return environment[name] }); err == nil {
			t.Fatalf("unsafe %s accepted", key)
		}
	}
}

func TestFetchProbeConfig(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/probe-config" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		sum := sha256.Sum256([]byte("config"))
		fmt.Fprintf(response, `{"schema_version":1,"config_revision":"sha256:%s","pools":[{"id":"pool","endpoints":[{"host":"us.example","port":3333,"tls":false,"continent":"north-america"},{"host":"eu.example","port":3333,"tls":false,"continent":"europe"},{"host":"global.example","port":3333,"tls":false}]}]}`, hex.EncodeToString(sum[:]))
	}))
	defer server.Close()
	collectorURL, _ := url.Parse(server.URL)
	cfg := Config{CollectorURL: collectorURL, Vantage: "us-west", Client: server.Client()}
	remote, pools, err := fetchProbeConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 1 || pools[0].ID != "pool" || len(pools[0].Endpoints) != 3 || remote.ConfigRevision == "" {
		t.Fatalf("remote=%+v pools=%+v", remote, pools)
	}
	europeCfg := Config{CollectorURL: collectorURL, Vantage: "europe", FilterContinents: true, Client: server.Client()}
	_, europePools, err := fetchProbeConfig(context.Background(), europeCfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(europePools) != 1 || len(europePools[0].Endpoints) != 2 || europePools[0].Endpoints[0].Host != "eu.example" || europePools[0].Endpoints[1].Host != "global.example" {
		t.Fatalf("Europe pools=%+v", europePools)
	}
}

func TestProbeConfigRejectsInvalidContinent(t *testing.T) {
	sum := sha256.Sum256([]byte("config"))
	config := ProbeConfig{
		SchemaVersion: 1, ConfigRevision: "sha256:" + hex.EncodeToString(sum[:]),
		Pools: []ProbePool{{ID: "pool", Endpoints: []ProbeEndpoint{{Host: "bad.example", Port: 3333, Continent: "elsewhere"}}}},
	}
	if err := validateProbeConfig(config); err == nil {
		t.Fatal("invalid continent accepted")
	}
}

func TestProbeConfigRejectsUnsafeOrDuplicateEndpoints(t *testing.T) {
	sum := sha256.Sum256([]byte("config"))
	revision := "sha256:" + hex.EncodeToString(sum[:])
	for name, endpoints := range map[string][]ProbeEndpoint{
		"control character": {{Host: "bad\n.example", Port: 3333}},
		"duplicate": {
			{Host: "pool.example", Port: 3333},
			{Host: "POOL.EXAMPLE", Port: 3333},
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := ProbeConfig{SchemaVersion: 1, ConfigRevision: revision, Pools: []ProbePool{{ID: "pool", Endpoints: endpoints}}}
			if err := validateProbeConfig(config); err == nil {
				t.Fatal("unsafe endpoint configuration accepted")
			}
		})
	}
}

func TestProbeConfigRejectsExcessiveEndpointCount(t *testing.T) {
	sum := sha256.Sum256([]byte("config"))
	endpoints := make([]ProbeEndpoint, maxProbeEndpoints+1)
	for index := range endpoints {
		endpoints[index] = ProbeEndpoint{Host: fmt.Sprintf("pool-%d.example", index), Port: 3333}
	}
	config := ProbeConfig{
		SchemaVersion:  1,
		ConfigRevision: "sha256:" + hex.EncodeToString(sum[:]),
		Pools:          []ProbePool{{ID: "pool", Endpoints: endpoints}},
	}
	if err := validateProbeConfig(config); err == nil {
		t.Fatal("excessive endpoint count accepted")
	}
}
