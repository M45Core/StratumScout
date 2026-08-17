package scout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

const (
	maxProbeConfigBytes = 1 << 20
	maxProbePools       = 128
	maxProbeEndpoints   = 256
)

type Config struct {
	CollectorURL     *url.URL
	KeyID            string
	Secret           []byte
	Region           string
	Vantage          string
	RunFor           time.Duration
	Continuous       bool
	ProcessNice      int
	FilterContinents bool
	Client           *http.Client
}

type ProbeConfig struct {
	SchemaVersion  int         `json:"schema_version"`
	ConfigRevision string      `json:"config_revision"`
	Pools          []ProbePool `json:"pools"`
}

type ProbePool struct {
	ID        string          `json:"id"`
	Endpoints []ProbeEndpoint `json:"endpoints"`
}

type ProbeEndpoint struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	TLS       bool   `json:"tls"`
	Continent string `json:"continent,omitempty"`
}

func LoadConfig(getenv func(string) string) (Config, error) {
	rawURL := strings.TrimSpace(getenv("COLLECTOR_URL"))
	collectorURL, err := url.Parse(rawURL)
	if err != nil || collectorURL.Scheme != "https" || collectorURL.Hostname() == "" || collectorURL.User != nil {
		return Config{}, errors.New("COLLECTOR_URL must be an HTTPS origin")
	}
	collectorURL.Path, collectorURL.RawQuery, collectorURL.Fragment = "", "", ""
	keyID := strings.TrimSpace(getenv("INGEST_KEY_ID"))
	secret := []byte(getenv("INGEST_SECRET"))
	if !validID(keyID, 128) || len(secret) < 32 {
		return Config{}, errors.New("INGEST_KEY_ID and an INGEST_SECRET of at least 32 bytes are required")
	}
	region := strings.TrimSpace(getenv("FLY_REGION"))
	productionRegion, ok := model.ProductionRegionForCode(region)
	if !ok {
		return Config{}, errors.New("unsupported FLY_REGION")
	}
	vantage := productionRegion.Vantage
	runFor := 5 * time.Minute
	if raw := strings.TrimSpace(getenv("RUN_FOR")); raw != "" {
		runFor, err = time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("RUN_FOR: %w", err)
		}
	}
	if runFor <= 0 || runFor > 14*time.Minute {
		return Config{}, errors.New("RUN_FOR must be greater than zero and at most 14 minutes")
	}
	continuous := true
	if raw := strings.TrimSpace(getenv("CONTINUOUS")); raw != "" {
		continuous, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("CONTINUOUS: %w", err)
		}
	}
	processNice := 0
	if raw := strings.TrimSpace(getenv("PROCESS_NICE")); raw != "" {
		processNice, err = strconv.Atoi(raw)
		if err != nil || processNice < 0 || processNice > 19 {
			return Config{}, errors.New("PROCESS_NICE must be an integer from 0 through 19")
		}
	}
	filterContinents := false
	if raw := strings.TrimSpace(getenv("FILTER_CONTINENTS")); raw != "" {
		filterContinents, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FILTER_CONTINENTS: %w", err)
		}
	}
	return Config{
		CollectorURL:     collectorURL,
		KeyID:            keyID,
		Secret:           secret,
		Region:           region,
		Vantage:          vantage,
		RunFor:           runFor,
		Continuous:       continuous,
		ProcessNice:      processNice,
		FilterContinents: filterContinents,
		Client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func fetchProbeConfig(ctx context.Context, cfg Config) (ProbeConfig, []model.Pool, error) {
	endpoint := cfg.CollectorURL.ResolveReference(&url.URL{Path: "/api/v1/probe-config"})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ProbeConfig{}, nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := cfg.Client.Do(request)
	if err != nil {
		return ProbeConfig{}, nil, fmt.Errorf("fetch probe configuration: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return ProbeConfig{}, nil, fmt.Errorf("fetch probe configuration: HTTP %d", response.StatusCode)
	}
	limited := &io.LimitedReader{R: response.Body, N: maxProbeConfigBytes + 1}
	decoder := json.NewDecoder(limited)
	var remote ProbeConfig
	if err := decoder.Decode(&remote); err != nil {
		return ProbeConfig{}, nil, fmt.Errorf("decode probe configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ProbeConfig{}, nil, err
	}
	if limited.N == 0 {
		return ProbeConfig{}, nil, errors.New("probe configuration is too large")
	}
	if err := validateProbeConfig(remote); err != nil {
		return ProbeConfig{}, nil, err
	}
	pools := make([]model.Pool, 0, len(remote.Pools))
	vantageContinent := continentForVantage(cfg.Vantage)
	for _, pool := range remote.Pools {
		converted := model.Pool{ID: pool.ID}
		for _, endpoint := range pool.Endpoints {
			if cfg.FilterContinents && endpoint.Continent != "" && endpoint.Continent != vantageContinent {
				continue
			}
			converted.Endpoints = append(converted.Endpoints, model.Endpoint{Host: endpoint.Host, Port: endpoint.Port, TLS: endpoint.TLS, Continent: endpoint.Continent})
		}
		if len(converted.Endpoints) > 0 {
			pools = append(pools, converted)
		}
	}
	if len(pools) == 0 {
		return ProbeConfig{}, nil, errors.New("probe configuration has no endpoints for this vantage")
	}
	return remote, pools, nil
}

func validateProbeConfig(cfg ProbeConfig) error {
	if cfg.SchemaVersion != 1 {
		return errors.New("unsupported probe configuration version")
	}
	if !strings.HasPrefix(cfg.ConfigRevision, "sha256:") || len(cfg.ConfigRevision) != len("sha256:")+sha256.Size*2 {
		return errors.New("invalid probe configuration revision")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(cfg.ConfigRevision, "sha256:")); err != nil {
		return errors.New("invalid probe configuration revision")
	}
	if len(cfg.Pools) == 0 || len(cfg.Pools) > maxProbePools {
		return errors.New("invalid pool count in probe configuration")
	}
	seenPools := make(map[string]bool, len(cfg.Pools))
	totalEndpoints := 0
	for _, pool := range cfg.Pools {
		if !validID(pool.ID, 128) || seenPools[pool.ID] || len(pool.Endpoints) == 0 {
			return errors.New("invalid pool in probe configuration")
		}
		seenPools[pool.ID] = true
		seenEndpoints := make(map[string]bool, len(pool.Endpoints))
		for _, endpoint := range pool.Endpoints {
			totalEndpoints++
			if totalEndpoints > maxProbeEndpoints || !validEndpointHost(endpoint.Host) || endpoint.Port < 1 || endpoint.Port > 65535 {
				return errors.New("invalid endpoint in probe configuration")
			}
			endpointKey := strings.ToLower(net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))) + "/tls=" + strconv.FormatBool(endpoint.TLS)
			if seenEndpoints[endpointKey] {
				return errors.New("duplicate endpoint in probe configuration")
			}
			seenEndpoints[endpointKey] = true
			if endpoint.Continent != "" && !validContinent(endpoint.Continent) {
				return errors.New("invalid endpoint continent in probe configuration")
			}
		}
	}
	expectedRevision, err := probeConfigRevision(cfg)
	if err != nil || cfg.ConfigRevision != expectedRevision {
		return errors.New("probe configuration revision does not match its contents")
	}
	return nil
}

func probeConfigRevision(cfg ProbeConfig) (string, error) {
	cfg.ConfigRevision = ""
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validEndpointHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, character := range host {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func continentForVantage(vantage string) string {
	if region, ok := model.ProductionRegionForVantage(vantage); ok {
		return region.Continent
	}
	switch vantage {
	case "us-west", "us-central", "us-east":
		return "north-america"
	case "europe":
		return "europe"
	default:
		return ""
	}
}

func validContinent(continent string) bool {
	switch continent {
	case "north-america", "south-america", "europe", "asia", "africa", "oceania", "antarctica":
		return true
	default:
		return false
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("probe configuration has trailing JSON")
	}
	return fmt.Errorf("decode probe configuration trailing data: %w", err)
}

func validID(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-_./:", character) {
			continue
		}
		return false
	}
	return true
}

func endpointCount(pools []model.Pool) int {
	total := 0
	for _, pool := range pools {
		total += len(pool.Endpoints)
	}
	return total
}

func parseAccepted(body io.Reader) (string, int, error) {
	var response struct {
		BatchID  string `json:"batch_id"`
		Accepted int    `json:"accepted"`
	}
	limited := &io.LimitedReader{R: body, N: 64<<10 + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(&response); err != nil {
		return "", 0, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", 0, errors.New("collector acknowledgement has trailing JSON")
		}
		return "", 0, err
	}
	if limited.N == 0 {
		return "", 0, errors.New("collector acknowledgement is too large")
	}
	if response.BatchID == "" || response.Accepted < 1 {
		return "", 0, errors.New("invalid collector acknowledgement")
	}
	return response.BatchID, response.Accepted, nil
}

func sequenceID(runID string, sequence uint64) string {
	return runID + "-" + strconv.FormatUint(sequence, 10)
}
