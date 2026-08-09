package model

const (
	RecordTypeProtocol = "protocol"
	RecordTypeProbeRun = "probe_run"

	ProtocolConnect      = "tcp.connect"
	ProtocolTLSHandshake = "tls.handshake"
	ProtocolSubscribe    = "mining.subscribe"
	ProtocolAuthorize    = "mining.authorize"
	ProtocolPing         = "mining.ping"

	ProtocolStatusOK          = "ok"
	ProtocolStatusRejected    = "rejected"
	ProtocolStatusUnsupported = "unsupported"
	ProtocolStatusTimeout     = "timeout"
	ProtocolStatusError       = "error"

	ProtocolErrorTLSCertificateInvalid = "tls_certificate_invalid"
)

// TimingStats summarizes successful durations while keeping every outcome
// count visible. Unsupported optional methods are not treated as failures.
type TimingStats struct {
	Attempts          int      `json:"attempts"`
	Successes         int      `json:"successes"`
	Rejected          int      `json:"rejected,omitempty"`
	Unsupported       int      `json:"unsupported,omitempty"`
	Timeouts          int      `json:"timeouts,omitempty"`
	Errors            int      `json:"errors,omitempty"`
	CertificateErrors int      `json:"certificate_errors,omitempty"`
	MedianMS          *float64 `json:"median_ms"`
	P95MS             *float64 `json:"p95_ms"`
}
