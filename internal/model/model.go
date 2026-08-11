package model

import "time"

// Pool describes one operator and the endpoints tested on equal terms.
type Pool struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Operator  string     `json:"operator,omitempty"`
	Website   string     `json:"website,omitempty"`
	Category  string     `json:"category,omitempty"`
	Status    string     `json:"status,omitempty"`
	Products  []string   `json:"products,omitempty"`
	Endpoints []Endpoint `json:"endpoints"`
}

type Endpoint struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	TLS    bool   `json:"tls"`
	Region string `json:"region,omitempty"`
	// Continent is a coarse, best-effort location derived from Region. It is
	// intentionally omitted for global and unlocated endpoints.
	Continent string `json:"continent,omitempty"`
}

type Config struct {
	Pools []Pool `json:"pools"`
}

const (
	MaxRetainedCoinbaseOutputs     = 64
	MaxRetainedCoinbaseScriptBytes = 80
)

// CoinbaseOutput is a retained destination from a decoded coinbase transaction.
// Address is populated only for standard Bitcoin mainnet output scripts; the
// raw script (or a marked prefix for oversized scripts) remains available when
// an address cannot be decoded.
type CoinbaseOutput struct {
	ValueSats             uint64 `json:"value_sats"`
	ScriptPubKey          string `json:"script_pubkey"`
	ScriptPubKeyTruncated bool   `json:"script_pubkey_truncated,omitempty"`
	Address               string `json:"address,omitempty"`
	ScriptType            string `json:"script_type"`
	// Worker exists only to recognize and redact legacy version 7 records.
	// Version 8 and later observations never serialize a matched worker destination.
	Worker bool `json:"worker,omitempty"`
}

// PayoutDestination is a latest-report view of a retained coinbase output.
type PayoutDestination struct {
	ValueSats             uint64  `json:"value_sats"`
	Percentage            float64 `json:"percentage"`
	ScriptPubKey          string  `json:"script_pubkey"`
	ScriptPubKeyTruncated bool    `json:"script_pubkey_truncated,omitempty"`
	Address               string  `json:"address,omitempty"`
	ScriptType            string  `json:"script_type"`
}

// MetricHistoryPoint is one recent, canonical observation in a report series.
type MetricHistoryPoint struct {
	ObservedAt time.Time `json:"observed_at"`
	Value      float64   `json:"value"`
}

// Observation is the immutable evidence used to produce reports. OffsetMS is
// relative to the first structurally valid block template seen by the same
// vantage for a block.
type Observation struct {
	Version                  int              `json:"version"`
	ObservationID            string           `json:"observation_id,omitempty"`
	Source                   string           `json:"source,omitempty"`
	RunID                    string           `json:"run_id,omitempty"`
	MachineID                string           `json:"machine_id,omitempty"`
	AgentVersion             string           `json:"agent_version,omitempty"`
	ConfigRevision           string           `json:"config_revision,omitempty"`
	RecordType               string           `json:"record_type,omitempty"`
	Endpoint                 string           `json:"endpoint,omitempty"`
	ProtocolMethod           string           `json:"protocol_method,omitempty"`
	DurationMS               *float64         `json:"duration_ms,omitempty"`
	ResponseStatus           string           `json:"response_status,omitempty"`
	ObservedAt               time.Time        `json:"observed_at"`
	Vantage                  string           `json:"vantage"`
	BlockID                  string           `json:"block_id"`
	PoolID                   string           `json:"pool_id"`
	Eligible                 bool             `json:"eligible"`
	Arrived                  bool             `json:"arrived"`
	OffsetMS                 float64          `json:"offset_ms,omitempty"`
	EmptyFirst               bool             `json:"empty_first,omitempty"`
	ConnectMS                float64          `json:"connect_ms,omitempty"`
	TLS                      bool             `json:"tls"`
	ErrorCategory            string           `json:"error_category,omitempty"`
	CoinbaseAnalyzed         bool             `json:"coinbase_analyzed,omitempty"`
	WorkerWalletInCoinbase   bool             `json:"worker_wallet_in_coinbase,omitempty"`
	CoinbaseTotalSats        uint64           `json:"coinbase_total_sats,omitempty"`
	WorkerPayoutSats         uint64           `json:"worker_payout_sats,omitempty"`
	CoinbaseOutputs          []CoinbaseOutput `json:"coinbase_outputs,omitempty"`
	CoinbaseOutputCount      int              `json:"coinbase_output_count,omitempty"`
	CoinbaseOutputsTruncated bool             `json:"coinbase_outputs_truncated,omitempty"`
	CoinbaseOmittedSats      uint64           `json:"coinbase_omitted_sats,omitempty"`
	EstimatedPoolFeePct      *float64         `json:"estimated_pool_fee_pct,omitempty"`
	RunStartedAt             *time.Time       `json:"run_started_at,omitempty"`
	RunStatus                string           `json:"run_status,omitempty"`
	ConfiguredEndpoints      int              `json:"configured_endpoints,omitempty"`
	SuccessfulSessions       int              `json:"successful_sessions,omitempty"`
	AcceptedBlocks           int              `json:"accepted_blocks,omitempty"`
	UploadedObservations     int              `json:"uploaded_observations,omitempty"`
	DroppedObservations      int              `json:"dropped_observations,omitempty"`
}

type PoolReport struct {
	PoolID                            string               `json:"pool_id"`
	PoolName                          string               `json:"pool_name"`
	Endpoint                          string               `json:"endpoint"`
	EndpointTLS                       bool                 `json:"endpoint_tls"`
	EndpointRegion                    string               `json:"endpoint_region,omitempty"`
	LastObservedAt                    *time.Time           `json:"last_observed_at,omitempty"`
	Category                          string               `json:"category,omitempty"`
	Products                          []string             `json:"products,omitempty"`
	Blocks                            int                  `json:"blocks"`
	Arrivals                          int                  `json:"arrivals"`
	MedianMS                          *float64             `json:"median_ms"`
	P95MS                             *float64             `json:"p95_ms"`
	EstimatedMiningLossPct            *float64             `json:"estimated_mining_loss_pct"`
	OverallScore                      *float64             `json:"overall_score"`
	RecentFeeIncreasePenalty          float64              `json:"recent_fee_increase_penalty,omitempty"`
	HighFeePenalty                    float64              `json:"high_fee_penalty,omitempty"`
	TLSCertificatePenalty             float64              `json:"tls_certificate_penalty,omitempty"`
	ScoreOverrideReason               string               `json:"score_override_reason,omitempty"`
	Availability                      float64              `json:"availability_pct"`
	TLSObserved                       bool                 `json:"tls_observed"`
	ConnectTiming                     TimingStats          `json:"connect_timing"`
	TLSTiming                         TimingStats          `json:"tls_handshake_timing"`
	SubscribeTiming                   TimingStats          `json:"subscribe_timing"`
	AuthorizeTiming                   TimingStats          `json:"authorize_timing"`
	PingTiming                        TimingStats          `json:"ping_timing"`
	CoinbaseSamples                   int                  `json:"coinbase_samples"`
	WorkerAddressObservedPct          *float64             `json:"worker_address_observed_pct"`
	WorkerAddressStatus               string               `json:"worker_address_status"`
	LatestPoolFeePct                  *float64             `json:"latest_pool_fee_pct"`
	PreviousPoolFeePct                *float64             `json:"previous_pool_fee_pct,omitempty"`
	PoolFeeChanged                    bool                 `json:"pool_fee_changed"`
	PoolFeeChanges                    int                  `json:"pool_fee_changes"`
	PoolFeeSamples                    int                  `json:"pool_fee_samples"`
	PoolFeeLastChangedAt              *time.Time           `json:"pool_fee_last_changed_at,omitempty"`
	LatestCoinbaseObservedAt          *time.Time           `json:"latest_coinbase_observed_at,omitempty"`
	LatestCoinbaseTotalSats           uint64               `json:"latest_coinbase_total_sats,omitempty"`
	LatestCoinbaseOutputCount         int                  `json:"latest_coinbase_output_count,omitempty"`
	LatestPayoutDestinations          []PayoutDestination  `json:"latest_payout_destinations,omitempty"`
	LatestPayoutDestinationsTruncated bool                 `json:"latest_payout_destinations_truncated,omitempty"`
	LatestPayoutOmittedSats           uint64               `json:"latest_payout_omitted_sats,omitempty"`
	TemplateLatencyHistory            []MetricHistoryPoint `json:"template_latency_history,omitempty"`
	PoolFeeHistory                    []MetricHistoryPoint `json:"pool_fee_history,omitempty"`
}

type Snapshot struct {
	GeneratedAt             time.Time    `json:"generated_at"`
	Methodology             string       `json:"methodology_version"`
	LatencyWindowHours      int          `json:"latency_window_hours"`
	RetentionWindowDays     int          `json:"retention_window_days"`
	BlocksObserved          int          `json:"blocks_observed"`
	EligibleEndpointSamples int          `json:"eligible_endpoint_samples"`
	TemplateDeliveries      int          `json:"template_deliveries"`
	Reports                 []PoolReport `json:"reports"`
	Disclosure              []string     `json:"disclosure"`
}
