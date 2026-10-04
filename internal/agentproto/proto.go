// Package agentproto holds the wire types shared by the control plane and
// rivet-agent. Both directions are plain HTTP/1.1 over yamux streams on one
// mutually authenticated TLS connection that the agent opens:
//
//	panel → agent  /node/v1/...   (notify, files, console, stats, query, port probes)
//	agent → panel  /agent/v1/...  (desired state, observations, secrets, builds)
//
// The panel derives the node identity from the client certificate; nothing
// in a request body can name another node.
package agentproto

import (
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// Version is the protocol version; both sides must match exactly, so a panel
// and its agents are upgraded together.
//
//	2  remote backups and journaled restore transactions
//	3  remote GitHub deployments (staged → apply → commit/rollback) and the
//	   shared /node/v1/bots/:id/transactions/:tx completion endpoint
//	4  GitHub publish/push for remote servers: the node selects the files
//	   (POST /node/v1/bots/:id/push-set); contents are read one file at a
//	   time through the existing files/content endpoint
//	5  AI file changes for remote servers: POST /node/v1/bots/:id/patch
//	   stages and swaps a revision-checked patch as a workspace transaction
//	   that the panel commits or rolls back through /transactions/:tx
//	6  add-ons of remote servers: GET /node/v1/bots/:id/addons (container
//	   states) and GET /node/v1/bots/:id/addons/:kind/logs?lines=N
//	7  remote-node completion: DELETE /node/v1/bots/:id/files?recursive=false
//	   (file or empty directory only; the emptiness check is the node's
//	   rmdir), POST /node/v1/bots/:id/diagnostics (isolated AI diagnostic on
//	   the node's Docker) and GET /node/v1/telemetry (disk, memory, CPU and
//	   load of the node, used for the free-disk check before staging and for
//	   node charts), DELETE /node/v1/bots/:id/addons/:kind/data (an add-on's
//	   data on the node, when it is removed or attached again). A protocol 6 agent would ignore recursive=false and
//	   delete a whole tree, so panel and agents must be upgraded together.
//	8  POST /node/v1/ports/probe: the node tries to bind host ports (TCP and
//	   UDP) on an allocation address, so the panel can skip busy ports when it
//	   allocates and refuse to start a remote game server whose port is taken.
//	   A protocol 7 agent has no such route; it is refused at hello.
const Version = 8

// MaxProbePorts bounds the ports one probe request may test.
const MaxProbePorts = 64

// PortProbeRequest asks the node whether host ports can be bound on IP (an
// IP literal such as 0.0.0.0). The answer is a check, not a reservation:
// another process may take a port right after it.
type PortProbeRequest struct {
	IP    string `json:"ip"`
	Ports []int  `json:"ports"`
}

// PortProbeResult is one port's answer. Free means both a TCP and a UDP
// socket could be bound; otherwise Reason says why not, in words.
type PortProbeResult struct {
	Port   int    `json:"port"`
	Free   bool   `json:"free"`
	Reason string `json:"reason,omitempty"`
}

// PortProbe answers POST /node/v1/ports/probe, in request order.
type PortProbe struct {
	Ports []PortProbeResult `json:"ports"`
}

// MaxAddonLogLines bounds one add-on log request; MaxAddonLogBytes bounds the
// answer the panel reads.
const (
	MaxAddonLogLines = 500
	MaxAddonLogBytes = 1 << 20
)

// AddonStatus is the observed state of one add-on container on a node.
type AddonStatus struct {
	State    string `json:"state"`
	Health   string `json:"health"`
	ExitCode int    `json:"exit_code"`
}

// AddonStates answers GET /node/v1/bots/:id/addons, keyed by add-on kind.
type AddonStates struct {
	Addons map[string]AddonStatus `json:"addons"`
}

// AddonLogs answers GET /node/v1/bots/:id/addons/:kind/logs.
type AddonLogs struct {
	Output string `json:"output"`
}

// DefaultPort is the agent listener port.
const DefaultPort = 8444

// Hello is the agent's first request after connecting.
type Hello struct {
	Protocol     int          `json:"protocol"`
	AgentVersion string       `json:"agent_version"`
	Hostname     string       `json:"hostname"`
	Capabilities Capabilities `json:"capabilities"`
}

// Capabilities are enforcement facts the agent measured on its host. They
// are reported, not promised: the panel shows them and never relies on them
// for security decisions.
type Capabilities struct {
	DockerVersion string `json:"docker_version"`
	CgroupV2      bool   `json:"cgroup_v2"`
	MemoryLimit   bool   `json:"memory_limit"`
	CPUQuota      bool   `json:"cpu_quota"`
	PidsLimit     bool   `json:"pids_limit"`
	Rootless      bool   `json:"rootless"`
	Problem       string `json:"problem,omitempty"`
	CPUs          int    `json:"cpus"`
	MemoryBytes   int64  `json:"memory_bytes"`
	DiskBytes     uint64 `json:"disk_bytes"`
	DiskFreeBytes uint64 `json:"disk_free_bytes"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
}

// Welcome answers Hello.
type Welcome struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	// InstallID scopes containers to this panel installation.
	InstallID string `json:"install_id"`
}

// Bot is a server row as the agent's runner sees it.
type Bot = domain.Bot

// Observation is a runner observation; the panel applies it only when the
// bot belongs to the calling node.
type Observation = sqlite.Observation

// ObserveResult reports whether an observation was stored.
type ObserveResult struct {
	Updated bool `json:"updated"`
}

// InstallState updates a game server's installation.
type InstallState struct {
	State       string  `json:"state"`
	ImageChoice *string `json:"image_choice,omitempty"`
}

// Version records a resolved game version.
type InstalledVersion struct {
	Version string `json:"version"`
}

// BuildStart begins a recorded build.
type BuildStart struct {
	BotID      string `json:"bot_id"`
	Generation int64  `json:"generation"`
}

// BuildStarted returns the operation id ("" = not recorded).
type BuildStarted struct {
	ID string `json:"id"`
}

// BuildStage names the current build stage.
type BuildStage struct {
	Stage string `json:"stage"`
}

// BuildFinish records a build outcome.
type BuildFinish struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Msg    string `json:"msg"`
}

// Rotate asks for a renewed node certificate.
type Rotate struct {
	CSR string `json:"csr"`
}

// Rotated returns the renewed certificate.
type Rotated struct {
	Certificate string `json:"certificate"`
	CA          string `json:"ca"`
	Serial      string `json:"serial"`
	ExpiresAtMS int64  `json:"expires_at_ms"`
}

// Notify asks the agent to reconcile one server now.
type Notify struct {
	BotID string `json:"bot_id"`
}

// BackupRequest carries the small, control-plane-owned metadata files that
// are added to an archive created from a node's workspace. Secret values stay
// sealed; the agent never needs the panel's encryption keys.
type BackupRequest struct {
	Extra      map[string][]byte `json:"extra,omitempty"`
	MaxEntries int               `json:"max_entries"`
	MaxBytes   int64             `json:"max_bytes"`
	MaxFile    int64             `json:"max_file"`
}

// TransactionStarted identifies a remote workspace replacement (a backup
// restore or a GitHub deployment) whose previous files are retained until the
// panel confirms or rolls back its database update.
type TransactionStarted struct {
	Transaction string `json:"transaction"`
}

// TransactionComplete finalizes a remote workspace replacement. Commit is
// false when the panel's database update failed (or the deployment is no
// longer wanted) and the old files must stay or return.
type TransactionComplete struct {
	Commit bool `json:"commit"`
}

// DeployApplied answers the swap of a staged deployment.
type DeployApplied struct {
	Files int `json:"files"`
}

// RestoreStarted is the protocol 2 name of TransactionStarted.
type RestoreStarted = TransactionStarted

// RestoreComplete is the protocol 2 name of TransactionComplete.
type RestoreComplete = TransactionComplete

// Status is the agent's runner state.
type Status struct {
	Ready   bool   `json:"ready"`
	Problem string `json:"problem,omitempty"`
	Running int    `json:"running"`
}

// PushSetRequest asks a node to select a workspace's files for a GitHub push
// with the same rules the panel applies locally (.gitignore files, built-in
// dependency/build/metadata exclusions and secret .env files). The limits
// may not exceed filesystem.DefaultPushLimits.
type PushSetRequest struct {
	MaxFiles int   `json:"max_files"`
	MaxBytes int64 `json:"max_bytes"`
	MaxFile  int64 `json:"max_file"`
}

// PatchRequest asks a node to apply an AI change set to a workspace. Every
// file names the revision it was based on ("" = must not exist yet); a
// mismatch refuses the whole patch. Limits may not exceed the node's own
// ceiling (MaxPatchFile, MaxPatchTotal).
type PatchRequest struct {
	Files    []PatchFile `json:"files"`
	MaxFile  int64       `json:"max_file"`
	MaxTotal int64       `json:"max_total"`
}

// PatchFile is one file of a PatchRequest. Delete removes the file; otherwise
// Content (base64 in JSON) becomes its complete new contents.
type PatchFile struct {
	Path           string `json:"path"`
	BeforeRevision string `json:"before_revision"`
	Content        []byte `json:"content"`
	Delete         bool   `json:"delete,omitempty"`
	Mode           uint32 `json:"mode,omitempty"`
}

// Ceilings a node accepts for one patch.
const (
	MaxPatchFile  = 8 << 20
	MaxPatchTotal = 32 << 20
)

// PatchStarted is a swapped patch awaiting the panel's commit or rollback.
// Revisions maps each written request path to its new revision.
type PatchStarted struct {
	Transaction string            `json:"transaction"`
	Revisions   map[string]string `json:"revisions"`
}

// PushSet is a node's selection. The panel then reads only these files, one
// at a time, through /node/v1/bots/:id/files/content.
type PushSet struct {
	Files   []filesystem.PushFile `json:"files"`
	Bytes   int64                 `json:"bytes"`
	Skipped []filesystem.PushSkip `json:"skipped"`
	Ignored int                   `json:"ignored"`
}

// DiagnosticRequest asks a node to run one isolated AI diagnostic for a
// server: the agent validates argv against its own runtime catalog (the same
// administrator-approved prefixes the panel uses), copies a safe snapshot of
// the workspace into its scratch directory and runs the same locked-down
// container as the panel does locally (no network, memory/CPU/PID/tmpfs
// caps, bounded output). The panel keeps authorization, approvals, run
// budgets and secret redaction.
type DiagnosticRequest struct {
	Runtime string   `json:"runtime"`
	Argv    []string `json:"argv"`
}

// DiagnosticResult answers DiagnosticRequest. Error is set when the run
// failed after it started (for example a timeout); Output may still hold
// what the command printed.
type DiagnosticResult struct {
	ExitCode   int64  `json:"exit_code"`
	Output     string `json:"output"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// MaxDiagnosticOutput bounds the output the panel reads (the agent clips at
// 1 MiB plus an ellipsis).
const MaxDiagnosticOutput = 1<<20 + 64

// Telemetry answers GET /node/v1/telemetry. Disk figures are for the
// filesystem holding the agent's server directories. Zero totals mean the
// value could not be read. Reported, not trusted: the panel uses it for
// display and for a free-disk preflight, never for authorization.
type Telemetry struct {
	SampledAtMS      int64   `json:"sampled_at_ms"`
	CPUPercent       float64 `json:"cpu_percent"`
	LogicalCPUs      int     `json:"logical_cpus"`
	MemoryUsedBytes  int64   `json:"memory_used_bytes"`
	MemoryTotalBytes int64   `json:"memory_total_bytes"`
	DiskTotalBytes   uint64  `json:"disk_total_bytes"`
	DiskFreeBytes    uint64  `json:"disk_free_bytes"`
	Load1            float64 `json:"load1"`
}
