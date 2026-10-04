// Package noderoute sends each server's lifecycle, console, statistics and
// file requests to the node that runs it: the in-process runner for the local
// node, or the node's rivet-agent over its authenticated connection.
package noderoute

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/agenthub"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/console"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/gamequery"
)

// Lifecycle is the local runner's control surface.
type Lifecycle interface {
	Notify(botID string)
	Purge(ctx context.Context, botID string) error
	Kill(ctx context.Context, botID string) error
}

// Backup streams a workspace archive from its node into dst. The panel-owned
// metadata is sealed before it crosses the mutually authenticated connection.
func (r *Router) Backup(ctx context.Context, nodeID, botID string, extra map[string][]byte, limits filesystem.BackupLimits, dst io.Writer) error {
	if !r.Remote(nodeID) || r.Hub == nil {
		return ErrNoRunner
	}
	b, err := json.Marshal(agentproto.BackupRequest{Extra: extra, MaxEntries: limits.MaxEntries, MaxBytes: limits.MaxBytes, MaxFile: limits.MaxFile})
	if err != nil {
		return err
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost, "/node/v1/bots/"+url.PathEscape(botID)+"/backups/archive", bytes.NewReader(b), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("agent backup answered %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

// BeginRestore swaps in a validated archive on the node but retains the old
// workspace until CompleteRestore confirms the panel's database update.
func (r *Router) BeginRestore(ctx context.Context, nodeID, botID string, src io.Reader, limits filesystem.BackupLimits) (string, error) {
	return r.beginTransaction(ctx, nodeID, "/node/v1/bots/"+url.PathEscape(botID)+"/backups/restore", limitQuery(limits), src)
}

// CompleteRestore commits or rolls back a remote workspace transaction.
func (r *Router) CompleteRestore(ctx context.Context, nodeID, botID, tx string, commit bool) error {
	return r.CompleteTransaction(ctx, nodeID, botID, tx, commit)
}

// Online reports whether a remote node's agent is connected now.
func (r *Router) Online(nodeID string) bool {
	return r.Remote(nodeID) && r.Hub != nil && r.Hub.Connected(nodeID)
}

// BeginDeploy streams a repository tarball to the node, which validates and
// stages all of it without changing the workspace. The returned transaction
// is swapped in by ApplyDeploy, or abandoned by CompleteTransaction(false).
func (r *Router) BeginDeploy(ctx context.Context, nodeID, botID string, src io.Reader, rootDir string, limits filesystem.BackupLimits) (string, error) {
	q := limitQuery(limits)
	q.Set("root_dir", rootDir)
	return r.beginTransaction(ctx, nodeID, "/node/v1/bots/"+url.PathEscape(botID)+"/deploy", q, src)
}

// ApplyDeploy swaps a staged deployment into place on the node. The previous
// files stay on the node until CompleteTransaction.
func (r *Router) ApplyDeploy(ctx context.Context, nodeID, botID, tx string) (int, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return 0, ErrNoRunner
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost,
		"/node/v1/bots/"+url.PathEscape(botID)+"/transactions/"+url.PathEscape(tx)+"/apply", nil, "")
	if err != nil {
		return 0, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, agentError("deploy", resp)
	}
	var out agentproto.DeployApplied
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return 0, err
	}
	return out.Files, nil
}

// PushSet asks the node to select a workspace's files for a GitHub push. The
// node applies the shared .gitignore, built-in and secret-file rules.
func (r *Router) PushSet(ctx context.Context, nodeID, botID string, lim filesystem.PushLimits) (filesystem.PushSet, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return filesystem.PushSet{}, ErrNoRunner
	}
	b, err := json.Marshal(agentproto.PushSetRequest{MaxFiles: lim.MaxFiles, MaxBytes: lim.MaxBytes, MaxFile: lim.MaxFile})
	if err != nil {
		return filesystem.PushSet{}, err
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost, "/node/v1/bots/"+url.PathEscape(botID)+"/push-set", bytes.NewReader(b), "application/json")
	if err != nil {
		return filesystem.PushSet{}, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return filesystem.PushSet{}, agentError("push selection", resp)
	}
	var out agentproto.PushSet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return filesystem.PushSet{}, err
	}
	// The node is trusted for its own workspace, but the panel still holds
	// it to the limits it asked for.
	if len(out.Files) > lim.MaxFiles || out.Bytes > lim.MaxBytes || len(out.Skipped) > 200 {
		return filesystem.PushSet{}, errors.New("agent returned a push selection over the requested limits")
	}
	return filesystem.PushSet{Files: out.Files, Bytes: out.Bytes, Skipped: out.Skipped, Ignored: out.Ignored}, nil
}

// ReadFile reads one workspace file from the node, refusing anything larger
// than max bytes. Path containment is enforced by the node's filesystem layer.
func (r *Router) ReadFile(ctx context.Context, nodeID, botID, p string, max int64) ([]byte, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return nil, ErrNoRunner
	}
	q := url.Values{}
	q.Set("path", p)
	q.Set("download", "1")
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodGet, "/node/v1/bots/"+url.PathEscape(botID)+"/files/content?"+q.Encode(), nil, "")
	if err != nil {
		return nil, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, agentError("file read", resp)
	}
	if resp.ContentLength > max {
		return nil, filesystem.ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, filesystem.ErrTooLarge
	}
	return data, nil
}

// CompleteTransaction commits or rolls back (or abandons, while staged) a
// remote workspace transaction.
func (r *Router) CompleteTransaction(ctx context.Context, nodeID, botID, tx string, commit bool) error {
	if !r.Remote(nodeID) || r.Hub == nil {
		return ErrNoRunner
	}
	b, err := json.Marshal(agentproto.TransactionComplete{Commit: commit})
	if err != nil {
		return err
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost,
		"/node/v1/bots/"+url.PathEscape(botID)+"/transactions/"+url.PathEscape(tx), bytes.NewReader(b), "application/json")
	if err != nil {
		return offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return agentError("transaction", resp)
	}
	return nil
}

func limitQuery(limits filesystem.BackupLimits) url.Values {
	q := url.Values{}
	q.Set("max_entries", strconv.Itoa(limits.MaxEntries))
	q.Set("max_bytes", strconv.FormatInt(limits.MaxBytes, 10))
	q.Set("max_file", strconv.FormatInt(limits.MaxFile, 10))
	return q
}

func (r *Router) beginTransaction(ctx context.Context, nodeID, path string, q url.Values, src io.Reader) (string, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return "", ErrNoRunner
	}
	// A spooled backup's size is known; a streamed repository archive's is
	// not (then only the margin is required). Archives can expand: this is a
	// preflight, the node's write still fails cleanly on a full disk.
	if err := r.EnsureDiskFree(ctx, nodeID, sizeOf(src)); err != nil {
		return "", err
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost, path+"?"+q.Encode(), src, "application/gzip")
	if err != nil {
		return "", offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", agentError("archive", resp)
	}
	var out agentproto.TransactionStarted
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if out.Transaction == "" {
		return "", errors.New("agent returned no transaction")
	}
	return out.Transaction, nil
}

// offline turns a missing connection into a message users can act on.
func offline(err error) error {
	if errors.Is(err, agenthub.ErrOffline) {
		return domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	return err
}

// agentError reads an agent's error answer. Client errors (an invalid or too
// large archive, a busy or unknown transaction) carry a message meant for the
// user; anything else stays an internal error.
func agentError(what string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		msg = body.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("agent %s answered %d: %s: %w", what, resp.StatusCode, msg, domain.ErrNotFound)
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && msg != "":
		return domain.Invalid(msg)
	}
	return fmt.Errorf("agent %s answered %d: %s", what, resp.StatusCode, msg)
}

// Stats follows a container's resource samples.
type Stats interface {
	StreamStats(ctx context.Context, containerID string, fn func(domain.ResourceSample) bool) error
}

// Stdin attaches to a container's input.
type Stdin interface {
	AttachStdin(ctx context.Context, containerID string) (io.WriteCloser, error)
}

// Router routes per node. Local fields may be nil when the panel runs no
// local runner; Hub is nil when the agents module is off.
type Router struct {
	LocalNode    string
	Local        Lifecycle
	LocalConsole console.Source
	LocalStats   Stats
	LocalStdin   Stdin
	Hub          *agenthub.Hub
	Bots         interface {
		GetBot(ctx context.Context, id string) (domain.Bot, error)
	}
	// DiskMargin is the free space a node must keep beyond a payload before
	// the panel stages files there (0 = DefaultDiskMargin, negative = no
	// free-disk check).
	DiskMargin int64
}

// ErrNoRunner means nothing can run servers on the node.
var ErrNoRunner = errors.New("no runner for this node")

// Remote reports whether a node is served by an agent.
func (r *Router) Remote(nodeID string) bool { return nodeID != "" && nodeID != r.LocalNode }

func (r *Router) nodeOf(ctx context.Context, botID string) (string, error) {
	b, err := r.Bots.GetBot(ctx, botID)
	if err != nil {
		return "", err
	}
	return b.NodeID, nil
}

// Notify asks the right runner to reconcile a server.
func (r *Router) Notify(botID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	node, err := r.nodeOf(ctx, botID)
	if err != nil {
		return
	}
	if !r.Remote(node) {
		if r.Local != nil {
			r.Local.Notify(botID)
		}
		return
	}
	if r.Hub != nil {
		r.Hub.Notify(node, botID)
	}
}

// Purge removes a deleted server's containers and files on its node. An
// offline agent purges it when it reconnects (the row stays until then).
func (r *Router) Purge(ctx context.Context, botID string) error {
	node, err := r.nodeOf(ctx, botID)
	if err != nil {
		return err
	}
	if !r.Remote(node) {
		if r.Local == nil {
			return ErrNoRunner
		}
		return r.Local.Purge(ctx, botID)
	}
	if r.Hub == nil {
		return ErrNoRunner
	}
	if err := r.Hub.Call(ctx, node, http.MethodPost, "/node/v1/purge", agentproto.Notify{BotID: botID}, nil); err != nil {
		if errors.Is(err, agenthub.ErrOffline) {
			return nil // the agent tears it down after reconnecting
		}
		return err
	}
	return nil
}

// Kill SIGKILLs a server's containers on its node.
func (r *Router) Kill(ctx context.Context, botID string) error {
	node, err := r.nodeOf(ctx, botID)
	if err != nil {
		return err
	}
	if !r.Remote(node) {
		if r.Local == nil {
			return ErrNoRunner
		}
		return r.Local.Kill(ctx, botID)
	}
	if r.Hub == nil {
		return ErrNoRunner
	}
	return r.Hub.Call(ctx, node, http.MethodPost, "/node/v1/kill", agentproto.Notify{BotID: botID}, nil)
}

// Console returns the log/stdin source for a node.
func (r *Router) Console(nodeID string) console.Source {
	if !r.Remote(nodeID) {
		return r.LocalConsole
	}
	return remote{r.Hub, nodeID}
}

// StatsFor returns the statistics source for a node.
func (r *Router) StatsFor(nodeID string) Stats {
	if !r.Remote(nodeID) {
		return r.LocalStats
	}
	return remote{r.Hub, nodeID}
}

// StdinFor returns the input source for a node.
func (r *Router) StdinFor(nodeID string) Stdin {
	if !r.Remote(nodeID) {
		return r.LocalStdin
	}
	return remote{r.Hub, nodeID}
}

// QueryMinecraft pings a server on a remote node through its agent.
func (r *Router) QueryMinecraft(ctx context.Context, nodeID string, port int) (gamequery.Status, error) {
	var st gamequery.Status
	if r.Hub == nil {
		return st, ErrNoRunner
	}
	err := r.Hub.Call(ctx, nodeID, http.MethodGet, "/node/v1/query/minecraft?port="+strconv.Itoa(port), nil, &st)
	return st, err
}

// ProbePorts asks a remote node's agent whether host ports can be bound on
// ip (protocol 8). It is a check, not a reservation. Ports are sent in
// batches of agentproto.MaxProbePorts; results come back in request order.
func (r *Router) ProbePorts(ctx context.Context, nodeID, ip string, ports []int) ([]agentproto.PortProbeResult, error) {
	if r.Hub == nil {
		return nil, ErrNoRunner
	}
	out := make([]agentproto.PortProbeResult, 0, len(ports))
	for len(ports) > 0 {
		n := min(len(ports), agentproto.MaxProbePorts)
		var res agentproto.PortProbe
		if err := r.Hub.Call(ctx, nodeID, http.MethodPost, "/node/v1/ports/probe",
			agentproto.PortProbeRequest{IP: ip, Ports: ports[:n]}, &res); err != nil {
			return nil, err
		}
		if len(res.Ports) != n {
			return nil, fmt.Errorf("the node answered %d of %d port probes", len(res.Ports), n)
		}
		out = append(out, res.Ports...)
		ports = ports[n:]
	}
	return out, nil
}

// remote implements console.Source, Stats and Stdin through an agent.
type remote struct {
	hub  *agenthub.Hub
	node string
}

func (m remote) check() error {
	if m.hub == nil {
		return ErrNoRunner
	}
	return nil
}

func (m remote) Logs(ctx context.Context, cid string, since time.Time, tail int) (io.ReadCloser, error) {
	if err := m.check(); err != nil {
		return nil, err
	}
	q := url.Values{}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339Nano))
	}
	if tail > 0 {
		q.Set("tail", strconv.Itoa(tail))
	}
	resp, err := m.hub.Do(ctx, m.node, http.MethodGet, "/node/v1/containers/"+url.PathEscape(cid)+"/logs?"+q.Encode(), nil, "")
	if err != nil {
		// A disconnected agent (or one that dropped mid-request) is not a
		// vanished container: the console waits and reattaches later.
		if errors.Is(err, agenthub.ErrOffline) || !m.hub.Connected(m.node) {
			return nil, fmt.Errorf("%w: %w", console.ErrSourceOffline, err)
		}
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("agent logs answered %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (m remote) AttachStdin(ctx context.Context, cid string) (io.WriteCloser, error) {
	if err := m.check(); err != nil {
		return nil, err
	}
	if !m.hub.Connected(m.node) {
		return nil, agenthub.ErrOffline
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		resp, err := m.hub.Do(context.WithoutCancel(ctx), m.node, http.MethodPost, "/node/v1/containers/"+url.PathEscape(cid)+"/stdin", pr, "application/octet-stream")
		if err == nil {
			if resp.StatusCode >= 300 {
				err = fmt.Errorf("agent stdin answered %d", resp.StatusCode)
			}
			resp.Body.Close()
		}
		pr.CloseWithError(err)
		done <- err
	}()
	return &stdinPipe{pw: pw, done: done}, nil
}

type stdinPipe struct {
	pw   *io.PipeWriter
	done chan error
}

func (s *stdinPipe) Write(p []byte) (int, error) { return s.pw.Write(p) }

// Close ends this attachment (the process keeps its input open) and waits
// briefly for the agent to finish delivering what was written.
func (s *stdinPipe) Close() error {
	s.pw.Close()
	select {
	case err := <-s.done:
		return err
	case <-time.After(5 * time.Second):
		return nil
	}
}

func (m remote) StreamStats(ctx context.Context, cid string, fn func(domain.ResourceSample) bool) error {
	if err := m.check(); err != nil {
		return err
	}
	resp, err := m.hub.Do(ctx, m.node, http.MethodGet, "/node/v1/containers/"+url.PathEscape(cid)+"/stats", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent stats answered %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var s domain.ResourceSample
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return err
		}
		if !fn(s) {
			return nil
		}
	}
	return sc.Err()
}
