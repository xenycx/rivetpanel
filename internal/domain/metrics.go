package domain

// Metrics attribution (migration 0076): per-app samples every 15 seconds,
// process snapshots and CPU spike records. All times are Unix milliseconds.

// ContainerSample is one app's resource use at one moment. Rates are bytes
// per second over the interval since the previous sample.
type ContainerSample struct {
	BotID       string
	NodeID      string
	SampledAtMS int64
	CPUCores    float64
	MemoryBytes int64
	NetRxBps    float64
	NetTxBps    float64
	BlkReadBps  float64
	BlkWriteBps float64
	PIDs        int64
}

// ContainerBucket is the mean of one app's samples in one time bucket.
type ContainerBucket struct {
	BotID       string
	BucketMS    int64
	CPUCores    float64
	MemoryBytes float64
	NetRxBps    float64
	NetTxBps    float64
	BlkReadBps  float64
	BlkWriteBps float64
	PIDs        float64
}

// ProcessEntry is one process in a snapshot. Cmd is the command line cut to
// 160 characters with known secret values and secret-looking arguments
// replaced; the environment is never read.
type ProcessEntry struct {
	PID         int     `json:"pid"`
	Comm        string  `json:"comm"`
	Cmd         string  `json:"cmd"`
	CPUPct      float64 `json:"cpu_pct"` // 100 = one core
	RSSBytes    int64   `json:"rss_bytes"`
	BotID       string  `json:"bot_id,omitempty"`
	BotName     string  `json:"bot_name,omitempty"`
	ContainerID string  `json:"container_id,omitempty"`
}

// Process snapshot reasons and sources.
const (
	SnapshotPeriodic = "periodic"
	SnapshotSpike    = "spike"
	SnapshotManual   = "manual"

	ProcSourceProc      = "proc"       // the host's /proc
	ProcSourceDockerTop = "docker-top" // Docker's per-container process list
	ProcSourceAgent     = "agent"      // a remote node's /proc, read by its agent
)

// ProcessSnapshot is the busiest processes of a node at one moment.
type ProcessSnapshot struct {
	ID          int64          `json:"id,omitempty"`
	NodeID      string         `json:"node_id,omitempty"`
	SampledAtMS int64          `json:"sampled_at_ms"`
	Reason      string         `json:"reason,omitempty"`
	Source      string         `json:"source"`
	HostCPUPct  float64        `json:"host_cpu_pct"`
	Items       []ProcessEntry `json:"items"`
}

// SpikeBot is an app's share of a CPU spike.
type SpikeBot struct {
	BotID    string  `json:"bot_id"`
	Name     string  `json:"name"`
	CPUCores float64 `json:"cpu_cores"`
	SharePct float64 `json:"share_pct"`
}

// LoadSpike is a period of high host CPU. EndedAtMS is nil while it lasts.
type LoadSpike struct {
	ID           int64          `json:"id"`
	NodeID       string         `json:"node_id"`
	StartedAtMS  int64          `json:"started_at_ms"`
	EndedAtMS    *int64         `json:"ended_at_ms"`
	PeakCPUPct   float64        `json:"peak_cpu_pct"`
	AvgCPUPct    float64        `json:"avg_cpu_pct"`
	TopBots      []SpikeBot     `json:"top_bots"`
	TopProcesses []ProcessEntry `json:"top_processes"`
}

// MetricEvent annotates a chart. Kind is deploy, start, stop, restart,
// crash, backup, spike or oom.
type MetricEvent struct {
	TMS     int64  `json:"t_ms"`
	Kind    string `json:"kind"`
	BotID   string `json:"bot_id,omitempty"`
	BotName string `json:"bot_name,omitempty"`
	Text    string `json:"text"`
}

// MetricSubject is what the metrics collector needs to know about an app.
type MetricSubject struct {
	ID            string
	Name          string
	NodeID        string
	ContainerID   string // "" when there is no container
	ObservedState string
	MemoryBytes   int64
	NanoCPUs      int64
}
