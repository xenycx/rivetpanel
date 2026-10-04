// Package domain holds core RivetPanel types shared across layers.
package domain

// LocalNodeID is the stable ID of the node seeded on initial setup.
const LocalNodeID = "6f1c0a52-3b7e-4d0e-9a41-0c5b7d2e8f10"

// LocalNodeName is the name of the seeded local node.
const LocalNodeName = "local"

// LocalLocationID is the location assigned to the built-in local node.
const LocalLocationID = "00000000-0000-0000-0000-000000000001"

// Node is a host that runs bot containers.
type Node struct {
	ID         string
	LocationID string
	Name       string
	Transport  string // "local", "agent" or "https"
	Endpoint   *string
	Enabled    bool
	LastSeenMS *int64
	// Draining nodes keep their servers but receive no new ones.
	Draining bool
	// PublicAddress is the host name or IP users connect to ("" = unknown).
	PublicAddress string
}

// IsAgent reports whether the node is run by a remote rivet-agent.
func (n Node) IsAgent() bool { return n.Transport == "agent" }

// AgentCertificate is one node certificate issued by the panel.
type AgentCertificate struct {
	Serial, NodeID          string
	IssuedAtMS, ExpiresAtMS int64
	RevokedAtMS             *int64
	RevokedReason           *string
}

// Location groups nodes for placement and operator navigation.
type Location struct {
	ID, Name, Description    string
	CreatedAtMS, UpdatedAtMS int64
}

// AgentState is the last capability and connection report for an outbound
// rivet-agent. CapabilitiesJSON is validated JSON and contains enforcement
// facts reported by the agent, not promises made by the control plane.
type AgentState struct {
	NodeID, CapabilitiesJSON string
	ProtocolVersion          int
	Connected                bool
	CertificateSerial        *string
	CertificateExpiresAtMS   *int64
	ConnectedAtMS            *int64
	DisconnectedAtMS         *int64
	UpdatedAtMS              int64
	AgentVersion, Hostname   string
}

// AgentCommand is a durable, idempotent unit of work delivered to one node.
type AgentCommand struct {
	ID, NodeID, Kind, PayloadJSON, IdempotencyKey string
	OperationID, ServerID                         *string
	Generation, DeadlineAtMS, CreatedAtMS         int64
	Status                                        string
	DeliveredAtMS, CompletedAtMS                  *int64
	Error                                         *string
}

// AgentEnrollment is a single-use credential bound to one pre-created agent
// node. TokenHash is persisted; the plaintext token is returned only once.
type AgentEnrollment struct {
	ID, NodeID, NodeName, LocationID, Prefix string
	TokenHash                                []byte
	CreatedAtMS, ExpiresAtMS                 int64
	UsedAtMS, RevokedAtMS                    *int64
}

// Telemetry is one node resource sample. CPUPercent is normalized to 0-100%
// of ALL logical CPUs.
type Telemetry struct {
	NodeID           string
	SampledAtMS      int64
	CPUPercent       float64
	LogicalCPUs      int
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	DiskUsedBytes    int64
	DiskTotalBytes   int64
	RunningBots      int

	// Added in migration 0032. Rates are bytes per second averaged over the
	// interval since the previous sample.
	Load1                         float64
	SwapUsedBytes, SwapTotalBytes int64
	NetRxBps, NetTxBps            int64
	DiskReadBps, DiskWriteBps     int64
}

// TelemetryBucket is one point of a downsampled series: the mean of the samples
// in the bucket, with the highest CPU and memory readings kept so a short spike
// is still visible on a week-long chart.
type TelemetryBucket struct {
	Telemetry
	CPUMax    float64
	MemoryMax int64
	Samples   int
}
