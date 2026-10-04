package noderoute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// DefaultDiskMargin is the free space a node keeps beyond a staged payload.
const DefaultDiskMargin = 256 << 20

// telemetryTimeout bounds the free-disk preflight so a slow node cannot
// stall a deployment before it starts.
const telemetryTimeout = 10 * time.Second

// Telemetry reads a remote node's current resources (disk of its server
// directories, memory, CPU, load). The figures are reported by the node and
// are used for display and the free-disk preflight only.
func (r *Router) Telemetry(ctx context.Context, nodeID string) (agentproto.Telemetry, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return agentproto.Telemetry{}, ErrNoRunner
	}
	ctx, cancel := context.WithTimeout(ctx, telemetryTimeout)
	defer cancel()
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodGet, "/node/v1/telemetry", nil, "")
	if err != nil {
		return agentproto.Telemetry{}, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return agentproto.Telemetry{}, agentError("telemetry", resp)
	}
	var out agentproto.Telemetry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return agentproto.Telemetry{}, err
	}
	return out, nil
}

// ErrNodeDiskFull is the free-disk preflight's refusal.
var ErrNodeDiskFull = errors.New("not enough free disk space on the node")

// EnsureDiskFree refuses to stage need bytes on a node whose server volume
// would keep less than the margin free afterwards. need < 0 means the size is
// not known in advance (a streamed repository archive): only the margin is
// required. A node that cannot report its disk is not refused here (the
// write itself still fails cleanly when the disk fills). This is a
// preflight, not a reservation: concurrent writers can still fill the disk.
func (r *Router) EnsureDiskFree(ctx context.Context, nodeID string, need int64) error {
	margin := r.DiskMargin
	if margin < 0 {
		return nil
	}
	if margin == 0 {
		margin = DefaultDiskMargin
	}
	t, err := r.Telemetry(ctx, nodeID)
	if err != nil {
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			return err // offline: the caller's operation cannot run either
		}
		return nil // older or failing telemetry: do not block on the check
	}
	if t.DiskTotalBytes == 0 {
		return nil
	}
	want := uint64(max(need, 0)) + uint64(margin)
	if t.DiskFreeBytes >= want {
		return nil
	}
	what := "at least"
	if need > 0 {
		what = fmt.Sprintf("%s for this change plus", byteSize(uint64(need)))
	}
	return &diskFullError{msg: fmt.Sprintf("the server's node has only %s of free disk space; it needs %s %s kept free. Free up space on the node and try again",
		byteSize(t.DiskFreeBytes), what, byteSize(uint64(margin)))}
}

// diskFullError is shown to the user (it unwraps to a
// domain.ValidationError) and matches ErrNodeDiskFull.
type diskFullError struct{ msg string }

func (e *diskFullError) Error() string { return e.msg }
func (e *diskFullError) Unwrap() []error {
	return []error{ErrNodeDiskFull, &domain.ValidationError{Msg: e.msg}}
}

func byteSize(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// sizeOf returns the remaining length of a staged payload when the reader
// knows it (a spooled file or an in-memory buffer), or -1.
func sizeOf(src io.Reader) int64 {
	switch v := src.(type) {
	case *os.File:
		st, err := v.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return -1
		}
		pos, err := v.Seek(0, io.SeekCurrent)
		if err != nil {
			return -1
		}
		return max(st.Size()-pos, 0)
	case *bytes.Reader:
		return int64(v.Len())
	case interface{ Len() int }:
		return int64(v.Len())
	}
	return -1
}

// RunDiagnostic runs one isolated AI diagnostic for a server on its remote
// node. The node validates argv against its runtime catalog, copies a safe
// snapshot of the workspace and runs the locked-down container on its own
// Docker; the caller authorizes, budgets and redacts.
func (r *Router) RunDiagnostic(ctx context.Context, nodeID, botID, runtimeID string, argv []string) (domain.DiagnosticResult, error) {
	var out domain.DiagnosticResult
	if !r.Remote(nodeID) || r.Hub == nil {
		return out, ErrNoRunner
	}
	b, err := json.Marshal(agentproto.DiagnosticRequest{Runtime: runtimeID, Argv: argv})
	if err != nil {
		return out, err
	}
	// The node bounds the run at 20 minutes; allow for the snapshot copy.
	ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost, filesPath(botID, "/diagnostics"), bytes.NewReader(b), "application/json")
	if err != nil {
		return out, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, agentError("diagnostic", resp)
	}
	var res agentproto.DiagnosticResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*agentproto.MaxDiagnosticOutput)).Decode(&res); err != nil {
		return out, err
	}
	if len(res.Output) > agentproto.MaxDiagnosticOutput {
		res.Output = res.Output[:agentproto.MaxDiagnosticOutput]
	}
	out.ExitCode, out.Output, out.Duration = res.ExitCode, res.Output, time.Duration(res.DurationMS)*time.Millisecond
	if res.Error != "" {
		return out, fmt.Errorf("diagnostic on the node: %s", res.Error)
	}
	return out, nil
}

// TelemetryStore persists node samples (the panel's node_telemetry table).
type TelemetryStore interface {
	InsertTelemetry(ctx context.Context, s domain.Telemetry) error
	CountRunningBots(ctx context.Context, nodeID string) (int, error)
}

// SampleNodes stores one telemetry sample for every connected remote node,
// so remote nodes get the same charts as the panel's own node. A node that
// does not answer is skipped. Disk figures are the node's server volume.
func (r *Router) SampleNodes(ctx context.Context, store TelemetryStore) int {
	if r.Hub == nil {
		return 0
	}
	n := 0
	for _, id := range r.Hub.ConnectedNodes() {
		if !r.Remote(id) {
			continue
		}
		t, err := r.Telemetry(ctx, id)
		if err != nil {
			continue
		}
		running, _ := store.CountRunningBots(ctx, id)
		s := domain.Telemetry{NodeID: id, SampledAtMS: time.Now().UnixMilli(), CPUPercent: min(max(t.CPUPercent, 0), 100),
			LogicalCPUs: t.LogicalCPUs, MemoryUsedBytes: max(t.MemoryUsedBytes, 0), MemoryTotalBytes: max(t.MemoryTotalBytes, 0),
			RunningBots: running, Load1: max(t.Load1, 0)}
		if t.DiskTotalBytes > 0 && t.DiskTotalBytes >= t.DiskFreeBytes && t.DiskTotalBytes < 1<<62 {
			s.DiskTotalBytes, s.DiskUsedBytes = int64(t.DiskTotalBytes), int64(t.DiskTotalBytes-t.DiskFreeBytes)
		}
		if store.InsertTelemetry(ctx, s) == nil {
			n++
		}
	}
	return n
}

// RunNodeSampler calls SampleNodes every interval until ctx ends. Retention
// pruning is shared with the panel's own sampler (it prunes every node).
func (r *Router) RunNodeSampler(ctx context.Context, store TelemetryStore, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.SampleNodes(ctx, store)
		}
	}
}
