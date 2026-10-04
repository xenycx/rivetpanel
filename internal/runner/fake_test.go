package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// fakeDocker is an in-memory Docker with fault injection.
type fakeDocker struct {
	mu    sync.Mutex
	conts map[string]*ContainerInfo
	specs map[string]ContainerSpec
	seq   int
	tail  string // returned by Tail

	caps      Capabilities
	capsErr   error
	capsCalls int

	// fault injection
	loseCreateResponses int // creates that succeed on the "daemon" but return an error
	createErr           error
	startErr            error
	resolveErr          error
	builderExit         int64
	onCreate            func(spec ContainerSpec) // called with lock released, before creating
	onStart             func(id string)
	// listOmitsLiveStart mimics the Docker adapter, whose listing carries no
	// start time for live containers.
	listOmitsLiveStart bool

	created  []ContainerSpec
	starts   int
	removes  int
	resolved []string
	waiting  chan struct{} // if set, builder Wait blocks until closed
	events   chan Event

	health   string              // health reported for containers with a health check
	networks map[string]bool     // networks that exist
	joined   map[string][]string // container id -> networks connected after create
}

func (f *fakeDocker) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[name] = true
	return nil
}

func (f *fakeDocker) ConnectNetwork(ctx context.Context, network, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.networks[network] {
		return errors.New("no such network")
	}
	f.joined[id] = append(f.joined[id], network)
	return nil
}

func (f *fakeDocker) RemoveNetwork(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.networks, name)
	return nil
}

func newFake() *fakeDocker {
	return &fakeDocker{
		conts: map[string]*ContainerInfo{}, specs: map[string]ContainerSpec{},
		caps:   Capabilities{MemoryLimit: true, CPUQuota: true, PidsLimit: true, SwapLimit: true, CgroupV2: true},
		events: make(chan Event, 256),
		health: "healthy", networks: map[string]bool{}, joined: map[string][]string{},
	}
}

func (f *fakeDocker) Capabilities(ctx context.Context) (Capabilities, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capsCalls++
	return f.caps, f.capsErr
}

func (f *fakeDocker) ResolveImage(ctx context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, ref)
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return ref + "@sha256:" + fmt.Sprintf("%064d", len(ref)), nil
}

func (f *fakeDocker) ListManaged(ctx context.Context, botID string) ([]ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ContainerInfo
	for _, c := range f.conts {
		if botID == "" || c.Labels[LabelBot] == botID {
			cc := *c
			if f.listOmitsLiveStart && cc.Live() {
				cc.StartedAt = time.Time{}
			}
			out = append(out, cc)
		}
	}
	return out, nil
}

func (f *fakeDocker) Create(ctx context.Context, spec ContainerSpec) (string, error) {
	if f.onCreate != nil {
		f.onCreate(spec)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	for _, c := range f.conts {
		if c.Name == spec.Name {
			return "", ErrNameConflict
		}
	}
	f.seq++
	id := "c" + strconv.Itoa(f.seq)
	f.conts[id] = &ContainerInfo{ID: id, Name: spec.Name, State: "created", Labels: map[string]string{
		LabelManaged: "true", LabelBot: spec.BotID, LabelNode: spec.NodeID, LabelRole: string(spec.Role),
		LabelGeneration: strconv.FormatInt(spec.Generation, 10), LabelSpec: spec.SpecHash,
	}}
	if spec.InstallID != "" {
		f.conts[id].Labels[LabelInstall] = spec.InstallID
	}
	if spec.AddonKind != "" {
		f.conts[id].Labels[LabelAddon] = spec.AddonKind
	}
	f.specs[id] = spec
	f.created = append(f.created, spec)
	if f.loseCreateResponses > 0 {
		f.loseCreateResponses--
		return "", errors.New("timeout waiting for create response")
	}
	return id, nil
}

func (f *fakeDocker) Start(ctx context.Context, id string) error {
	if f.onStart != nil {
		f.onStart(id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conts[id]
	if !ok {
		return ErrNoContainer
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.starts++
	c.StartedAt = time.Now()
	if c.Role() == RoleBuilder {
		c.State, c.ExitCode, c.FinishedAt = "exited", int(f.builderExit), time.Now()
		return nil
	}
	c.State = "running"
	if len(f.specs[id].Health) > 0 {
		c.Health = f.health
	}
	return nil
}

func (f *fakeDocker) Stop(ctx context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conts[id]
	if !ok {
		return ErrNoContainer
	}
	if c.Live() {
		c.State, c.ExitCode, c.FinishedAt = "exited", 143, time.Now()
	}
	return nil
}

func (f *fakeDocker) Tail(ctx context.Context, id string, n int) (string, error) { return f.tail, nil }

func (f *fakeDocker) Follow(ctx context.Context, id string, w io.Writer) error {
	_, err := io.WriteString(w, f.tail)
	return err
}

func (f *fakeDocker) Kill(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conts[id]
	if !ok {
		return ErrNoContainer
	}
	if c.Live() {
		c.State, c.ExitCode, c.FinishedAt = "exited", 137, time.Now()
	}
	return nil
}

func (f *fakeDocker) Remove(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.conts[id]; !ok {
		return ErrNoContainer
	}
	delete(f.conts, id)
	f.removes++
	return nil
}

func (f *fakeDocker) Inspect(ctx context.Context, id string) (ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conts[id]
	if !ok {
		return ContainerInfo{}, ErrNoContainer
	}
	return *c, nil
}

func (f *fakeDocker) Wait(ctx context.Context, id string) (int64, error) {
	if f.waiting != nil {
		select {
		case <-f.waiting:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conts[id]
	if !ok {
		return 0, ErrNoContainer
	}
	return int64(c.ExitCode), nil
}

func (f *fakeDocker) Events(ctx context.Context) (<-chan Event, <-chan error) {
	return f.events, make(chan error)
}

// test helpers

func (f *fakeDocker) runtimeContainers(botID string) []ContainerInfo {
	all, _ := f.ListManaged(context.Background(), botID)
	var out []ContainerInfo
	for _, c := range all {
		if c.Role() == RoleRuntime {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeDocker) crash(id string, code int, oom bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.conts[id]
	c.State, c.ExitCode, c.OOMKilled, c.FinishedAt = "exited", code, oom, time.Now()
}
