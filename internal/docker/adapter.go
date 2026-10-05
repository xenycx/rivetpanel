// Package docker adapts the official Docker Go SDK to the runner's Docker interface.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
)

const (
	opTimeout   = 30 * time.Second
	pullTimeout = 15 * time.Minute
)

// Adapter implements runner.Docker on top of the official SDK.
type Adapter struct {
	cli   *client.Client
	paths PathMap // bind sources as the Docker host sees them (see SetPathMap)
	// ncpu and memTotal cache the daemon host's CPU count and memory (0 =
	// not read yet).
	ncpu, memTotal atomic.Int64
}

var _ runner.Docker = (*Adapter)(nil)

// New connects to the Docker Engine at host (e.g. unix:///var/run/docker.sock)
// with API version negotiation. Connecting is lazy; Capabilities verifies it.
func New(host string) (*Adapter, error) {
	cli, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Adapter{cli: cli}, nil
}

// Close releases the client.
func (a *Adapter) Close() error { return a.cli.Close() }

func within(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func (a *Adapter) Capabilities(ctx context.Context) (runner.Capabilities, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	info, err := a.cli.Info(ctx)
	if err != nil {
		return runner.Capabilities{}, err
	}
	c := runner.Capabilities{
		ServerVersion: info.ServerVersion, CgroupV2: info.CgroupVersion == "2",
		MemoryLimit: info.MemoryLimit, CPUQuota: info.CPUCfsQuota, PidsLimit: info.PidsLimit, SwapLimit: info.SwapLimit,
	}
	for _, o := range info.SecurityOptions {
		if strings.Contains(o, "name=rootless") {
			c.Rootless = true
		}
	}
	// A rootless daemon reports the controllers as available but silently skips
	// applying limits unless it uses the systemd cgroup driver.
	if c.Rootless && info.CgroupDriver != "systemd" {
		c.Problem = "rootless Docker must use the systemd cgroup driver, otherwise resource limits are not enforced"
	}
	return c, nil
}

// ResolveImage returns an immutable reference for ref, pulling it when absent.
func (a *Adapter) ResolveImage(ctx context.Context, ref string) (string, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	inspect := func() (image.InspectResponse, error) {
		ictx, cancel := within(ctx, opTimeout)
		defer cancel()
		return a.cli.ImageInspect(ictx, ref)
	}
	img, err := inspect()
	if errdefs.IsNotFound(err) {
		if err := a.pull(ctx, ref); err != nil {
			return "", err
		}
		img, err = inspect()
	}
	if err != nil {
		return "", err
	}
	if _, ok := named.(reference.Digested); ok {
		return named.String(), nil // already immutable; presence verified above
	}
	repo := reference.TrimNamed(named).Name()
	for _, rd := range img.RepoDigests {
		if r, err := reference.ParseNormalizedNamed(rd); err == nil && r.Name() == repo {
			return rd, nil
		}
	}
	// Locally built or re-tagged images have no registry digest; the image ID is
	// still an immutable content address.
	if img.ID == "" {
		return "", errors.New("image has no identifier")
	}
	return img.ID, nil
}

func (a *Adapter) pull(ctx context.Context, ref string) error {
	ctx, cancel := within(ctx, pullTimeout)
	defer cancel()
	rc, err := a.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	defer rc.Close()
	// The stream reports registry errors as JSON messages, not HTTP failures.
	if err := jsonmessage.DisplayJSONMessagesStream(rc, io.Discard, 0, false, nil); err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	return nil
}

func labelFilter(botID string) filters.Args {
	f := filters.NewArgs(filters.Arg("label", runner.LabelManaged+"=true"))
	if botID != "" {
		f.Add("label", runner.LabelBot+"="+botID)
	}
	return f
}

// PublishedPorts lists the host ports published by every running container
// on this Docker host, not only managed ones: another panel or any other tool
// may hold a port this panel is about to use.
func (a *Adapter) PublishedPorts(ctx context.Context) ([]runner.PublishedPort, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	list, err := a.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []runner.PublishedPort
	for _, c := range list {
		name := c.ID
		if len(name) > 12 {
			name = name[:12]
		}
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			out = append(out, runner.PublishedPort{HostIP: p.IP, HostPort: int(p.PublicPort), Proto: p.Type,
				ContainerID: c.ID, Container: name, BotID: c.Labels[runner.LabelBot]})
		}
	}
	return out, nil
}

func (a *Adapter) ListManaged(ctx context.Context, botID string) ([]runner.ContainerInfo, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	list, err := a.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: labelFilter(botID)})
	if err != nil {
		return nil, err
	}
	out := make([]runner.ContainerInfo, 0, len(list))
	for _, c := range list {
		info := runner.ContainerInfo{ID: c.ID, Labels: c.Labels, State: c.State}
		if len(c.Names) > 0 {
			info.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		if !info.Live() && info.State != "created" {
			// exit details are only available from inspect
			if full, err := a.inspect(ctx, c.ID); err == nil {
				info = full
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (a *Adapter) inspect(ctx context.Context, id string) (runner.ContainerInfo, error) {
	r, err := a.cli.ContainerInspect(ctx, id)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runner.ContainerInfo{}, runner.ErrNoContainer
		}
		return runner.ContainerInfo{}, err
	}
	info := runner.ContainerInfo{ID: r.ID, Name: strings.TrimPrefix(r.Name, "/")}
	if r.Config != nil {
		info.Labels = r.Config.Labels
	}
	if s := r.State; s != nil {
		info.State, info.ExitCode, info.OOMKilled = s.Status, s.ExitCode, s.OOMKilled
		info.StartedAt, _ = time.Parse(time.RFC3339Nano, s.StartedAt)
		info.FinishedAt, _ = time.Parse(time.RFC3339Nano, s.FinishedAt)
		if s.Health != nil {
			info.Health = string(s.Health.Status)
		}
	}
	return info, nil
}

func (a *Adapter) Inspect(ctx context.Context, id string) (runner.ContainerInfo, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	return a.inspect(ctx, id)
}

// HostResources returns the Docker host's CPU count and total memory as the
// daemon reports them (read once, then cached).
func (a *Adapter) HostResources(ctx context.Context) (cpus int, memBytes int64, err error) {
	if n := a.ncpu.Load(); n > 0 {
		return int(n), a.memTotal.Load(), nil
	}
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	info, err := a.cli.Info(ctx)
	if err != nil {
		return 0, 0, err
	}
	if info.NCPU <= 0 {
		return 0, 0, errors.New("the Docker daemon reported no CPUs")
	}
	a.memTotal.Store(info.MemTotal)
	a.ncpu.Store(int64(info.NCPU))
	return info.NCPU, info.MemTotal, nil
}

// fitCPUs lowers a CPU limit above the host's CPU count to that count:
// Docker refuses such a container outright ("range of CPUs is from 0.01 to
// N"), which would leave the server unable to start. Servers saved before
// limits followed each node's hardware can carry such a limit.
func (a *Adapter) fitCPUs(ctx context.Context, nano int64) int64 {
	cpus, _, err := a.HostResources(ctx)
	if err != nil || nano <= int64(cpus)*1e9 {
		return nano
	}
	return int64(cpus) * 1e9
}

func (a *Adapter) Create(ctx context.Context, spec runner.ContainerSpec) (string, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	spec = a.translateSpec(spec)
	spec.NanoCPUs = a.fitCPUs(ctx, spec.NanoCPUs)
	resp, err := a.cli.ContainerCreate(ctx, ContainerConfig(spec), HostConfig(spec), NetworkingConfig(spec), nil, spec.Name)
	if err != nil {
		if errdefs.IsConflict(err) {
			return "", runner.ErrNameConflict
		}
		return "", err
	}
	return resp.ID, nil
}

func (a *Adapter) Start(ctx context.Context, id string) error {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	err := a.cli.ContainerStart(ctx, id, container.StartOptions{})
	if errdefs.IsNotFound(err) {
		return runner.ErrNoContainer
	}
	return err
}

func (a *Adapter) Stop(ctx context.Context, id string, timeout time.Duration) error {
	ctx, cancel := within(ctx, timeout+20*time.Second)
	defer cancel()
	secs := int(timeout.Seconds())
	err := a.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &secs})
	if errdefs.IsNotFound(err) {
		return runner.ErrNoContainer
	}
	return err
}

// Tail returns the last n lines of a container's output, stdout and stderr
// merged (the container must have Tty=false, which the runner guarantees).
func (a *Adapter) Tail(ctx context.Context, id string, n int) (string, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	rc, err := a.cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(n)})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "", runner.ErrNoContainer
		}
		return "", err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := stdcopy.StdCopy(&buf, &buf, io.LimitReader(rc, 256<<10)); err != nil {
		return buf.String(), nil // partial output is still useful
	}
	return buf.String(), nil
}

// Follow copies a container's output to w until it stops or ctx ends.
func (a *Adapter) Follow(ctx context.Context, id string, w io.Writer) error {
	rc, err := a.cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runner.ErrNoContainer
		}
		return err
	}
	defer rc.Close()
	_, err = stdcopy.StdCopy(w, w, rc)
	return err
}

// Kill sends SIGKILL to a container immediately.
func (a *Adapter) Kill(ctx context.Context, id string) error {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	err := a.cli.ContainerKill(ctx, id, "SIGKILL")
	if errdefs.IsNotFound(err) {
		return runner.ErrNoContainer
	}
	if err != nil && strings.Contains(err.Error(), "is not running") {
		return nil // already dead: the goal is met
	}
	return err
}

// Signal sends a stop signal (for a graceful stop) to a container's main
// process. Only the signals blueprints may name are accepted.
func (a *Adapter) Signal(ctx context.Context, id, sig string) error {
	switch sig {
	case "SIGINT", "SIGTERM", "SIGQUIT", "SIGHUP":
	default:
		return fmt.Errorf("signal %q is not allowed", sig)
	}
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	err := a.cli.ContainerKill(ctx, id, sig)
	if errdefs.IsNotFound(err) {
		return runner.ErrNoContainer
	}
	return err
}

func (a *Adapter) Remove(ctx context.Context, id string) error {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	err := a.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if errdefs.IsNotFound(err) {
		return runner.ErrNoContainer
	}
	return err
}

func (a *Adapter) Wait(ctx context.Context, id string) (int64, error) {
	respCh, errCh := a.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case r := <-respCh:
		if r.Error != nil {
			return r.StatusCode, errors.New(r.Error.Message)
		}
		return r.StatusCode, nil
	case err := <-errCh:
		if errdefs.IsNotFound(err) {
			return 0, runner.ErrNoContainer
		}
		return 0, err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Events streams lifecycle events for managed containers. The returned
// channels close when ctx ends; an error on the second channel means the
// stream broke and the caller must resubscribe and resync.
func (a *Adapter) Events(ctx context.Context) (<-chan runner.Event, <-chan error) {
	msgs, errs := a.cli.Events(ctx, events.ListOptions{Filters: filters.NewArgs(
		filters.Arg("type", string(events.ContainerEventType)),
		filters.Arg("label", runner.LabelManaged+"=true"),
	)})
	out := make(chan runner.Event, 64)
	outErr := make(chan error, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-msgs:
				if !ok {
					return
				}
				ev := runner.Event{Role: runner.Role(m.Actor.Attributes[runner.LabelRole]), NodeID: m.Actor.Attributes[runner.LabelNode], InstallID: m.Actor.Attributes[runner.LabelInstall], BotID: m.Actor.Attributes[runner.LabelBot], ContainerID: m.Actor.ID, Action: string(m.Action)}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			case err := <-errs:
				if err != nil && ctx.Err() == nil {
					outErr <- err
				}
				return
			}
		}
	}()
	return out, outErr
}

// ContainerConfig maps a spec to the SDK's container configuration. The
// argument vector is passed as the entrypoint (exec form): no shell is involved.
func ContainerConfig(s runner.ContainerSpec) *container.Config {
	entry, cmd := s.Argv, []string{}
	if len(s.Entrypoint) > 0 {
		entry, cmd = s.Entrypoint, s.Argv
	}
	if s.KeepImageEntrypoint {
		entry, cmd = nil, s.Argv
	}
	workdir := ""
	if s.WorkspaceHostPath != "" {
		workdir = "/workspace"
	}
	labels := withInstall(map[string]string{
		runner.LabelManaged:    "true",
		runner.LabelBot:        s.BotID,
		runner.LabelNode:       s.NodeID,
		runner.LabelRole:       string(s.Role),
		runner.LabelGeneration: strconv.FormatInt(s.Generation, 10),
		runner.LabelSpec:       s.SpecHash,
	}, s.InstallID)
	if s.AddonKind != "" {
		labels[runner.LabelAddon] = s.AddonKind
	}
	var health *container.HealthConfig
	if len(s.Health) > 0 {
		health = &container.HealthConfig{Test: append([]string{"CMD"}, s.Health...), Interval: 5 * time.Second,
			Timeout: 5 * time.Second, Retries: 5, StartPeriod: 120 * time.Second}
	}
	exposed := nat.PortSet{}
	for _, p := range s.Ports {
		exposed[nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, p.Proto))] = struct{}{}
	}
	return &container.Config{
		ExposedPorts: exposed,
		Image:        s.Image,
		Entrypoint:   entry,
		Cmd:          cmd,
		Env:          s.Env,
		WorkingDir:   workdir,
		User:         s.User,
		OpenStdin:    s.OpenStdin, // process input, not a shell
		StdinOnce:    false,
		Tty:          false,
		Labels:       labels,
		Healthcheck:  health,
	}
}

// NetworkingConfig gives a container its host-name aliases on a
// user-defined primary network (add-ons are reached by their kind).
func NetworkingConfig(s runner.ContainerSpec) *network.NetworkingConfig {
	if len(s.NetworkAliases) == 0 {
		return nil
	}
	return &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
		s.Network: {Aliases: append([]string(nil), s.NetworkAliases...)},
	}}
}

func withInstall(labels map[string]string, id string) map[string]string {
	if id != "" {
		labels[runner.LabelInstall] = id
	}
	return labels
}

// HostConfig applies the mandatory isolation and resource policy.
func HostConfig(s runner.ContainerSpec) *container.HostConfig {
	pids := s.PidsLimit
	bindings := nat.PortMap{}
	for _, p := range s.Ports {
		k := nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, p.Proto))
		bindings[k] = append(bindings[k], nat.PortBinding{HostIP: p.HostIP, HostPort: strconv.Itoa(p.HostPort)})
	}
	var mounts []mount.Mount
	bind := func(src, dst string) {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: src, Target: dst,
			BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate, CreateMountpoint: false}})
	}
	if s.WorkspaceHostPath != "" {
		bind(s.WorkspaceHostPath, "/workspace")
	}
	for _, m := range s.Mounts {
		bind(m.Source, m.Target)
	}
	tmpfs := map[string]string{"/tmp": fmt.Sprintf("rw,nosuid,nodev,size=%d", s.TmpfsBytes)}
	for _, p := range s.ExtraTmpfs {
		tmpfs[p] = fmt.Sprintf("rw,nosuid,nodev,size=%d", min(s.TmpfsBytes, 16<<20))
	}
	return &container.HostConfig{
		PortBindings:   bindings,
		Mounts:         mounts,
		NetworkMode:    container.NetworkMode(s.Network),
		ReadonlyRootfs: true,
		Tmpfs:          tmpfs,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"}, // default seccomp profile is retained
		Privileged:     false,
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyDisabled}, // the runner owns restarts
		LogConfig:      container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		Resources: container.Resources{
			Memory:     s.MemoryBytes,
			MemorySwap: s.MemoryBytes, // equal to memory: no additional swap
			NanoCPUs:   s.NanoCPUs,
			PidsLimit:  &pids,
		},
	}
}

// EnsureNetwork creates an internal bridge network unless it exists. An
// internal network has no route out: add-ons on it cannot reach the internet.
func (a *Adapter) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	if _, err := a.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	_, err := a.cli.NetworkCreate(ctx, name, network.CreateOptions{Driver: "bridge", Internal: true, Labels: labels})
	if errdefs.IsConflict(err) {
		return nil // created concurrently
	}
	return err
}

// ConnectNetwork attaches a container to a network (idempotent).
func (a *Adapter) ConnectNetwork(ctx context.Context, net, id string) error {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	err := a.cli.NetworkConnect(ctx, net, id, nil)
	if err != nil && (errdefs.IsConflict(err) || strings.Contains(err.Error(), "already exists")) {
		return nil
	}
	return err
}

// RemoveNetwork deletes a network; a missing one is not an error.
func (a *Adapter) RemoveNetwork(ctx context.Context, name string) error {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	err := a.cli.NetworkRemove(ctx, name)
	if errdefs.IsNotFound(err) {
		return nil
	}
	return err
}

var _ runner.Networker = (*Adapter)(nil)

// Logs follows a container's output as Docker's multiplexed stream, with
// timestamps. The container must have Tty=false (the runner guarantees it).
func (a *Adapter) Logs(ctx context.Context, id string, since time.Time, tail int) (io.ReadCloser, error) {
	opts := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true}
	if !since.IsZero() {
		opts.Since = since.UTC().Format(time.RFC3339Nano)
	} else if tail > 0 {
		opts.Tail = strconv.Itoa(tail)
	}
	rc, err := a.cli.ContainerLogs(ctx, id, opts)
	if err != nil && errdefs.IsNotFound(err) {
		return nil, runner.ErrNoContainer
	}
	return rc, err
}

// LogRange returns a container's output between since and until (Docker's
// multiplexed stream with timestamps) without following it, so the stream
// ends at until. The log archive uses it to capture console output.
func (a *Adapter) LogRange(ctx context.Context, id string, since, until time.Time) (io.ReadCloser, error) {
	opts := container.LogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true}
	if !since.IsZero() {
		opts.Since = since.UTC().Format(time.RFC3339Nano)
	}
	if !until.IsZero() {
		opts.Until = until.UTC().Format(time.RFC3339Nano)
	}
	rc, err := a.cli.ContainerLogs(ctx, id, opts)
	if err != nil && errdefs.IsNotFound(err) {
		return nil, runner.ErrNoContainer
	}
	return rc, err
}

// stdinHandle writes to a container's stdin. Close only drops our connection:
// the container runs with StdinOnce=false, so the process keeps its stdin open
// and a later client can attach again. CloseWrite is deliberately never used
// because it would signal EOF to the bot.
type stdinHandle struct{ resp types.HijackedResponse }

func (h stdinHandle) Write(p []byte) (int, error) { return h.resp.Conn.Write(p) }
func (h stdinHandle) Close() error                { h.resp.Close(); return nil }

// AttachStdin opens a write-only attachment to the container's stdin.
func (a *Adapter) AttachStdin(ctx context.Context, id string) (io.WriteCloser, error) {
	resp, err := a.cli.ContainerAttach(ctx, id, container.AttachOptions{Stream: true, Stdin: true, Stdout: false, Stderr: false})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, runner.ErrNoContainer
		}
		return nil, err
	}
	return stdinHandle{resp}, nil
}

// SampleFromStats converts a Docker stats frame to a ResourceSample. CPU uses
// the delta against the previous frame Docker embeds; memory excludes
// reclaimable page cache the way `docker stats` does.
func SampleFromStats(s container.StatsResponse) domain.ResourceSample {
	out := domain.ResourceSample{MemLimitBytes: int64(s.MemoryStats.Limit), PIDs: int64(s.PidsStats.Current)}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	online := float64(s.CPUStats.OnlineCPUs)
	if online == 0 {
		online = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	// Docker's first streamed frame has no previous sample (precpu is zero):
	// report 0 instead of a bogus spike.
	if s.PreCPUStats.SystemUsage > 0 && cpuDelta > 0 && sysDelta > 0 && online > 0 {
		out.CPUCores = cpuDelta / sysDelta * online
	}
	used := s.MemoryStats.Usage
	for _, k := range []string{"inactive_file", "total_inactive_file"} { // cgroup v2, v1
		if v, ok := s.MemoryStats.Stats[k]; ok && v < used {
			used -= v
			break
		}
	}
	out.MemUsedBytes = int64(used)
	for _, n := range s.Networks {
		out.NetRxBytes += int64(n.RxBytes)
		out.NetTxBytes += int64(n.TxBytes)
	}
	return out
}

// StreamStats follows a container's Docker stats stream (about one frame per
// second) until fn returns false, the container stops, or ctx ends.
func (a *Adapter) StreamStats(ctx context.Context, id string, fn func(domain.ResourceSample) bool) error {
	resp, err := a.cli.ContainerStats(ctx, id, true)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runner.ErrNoContainer
		}
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var s container.StatsResponse
		if err := dec.Decode(&s); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !fn(SampleFromStats(s)) {
			return nil
		}
	}
}
