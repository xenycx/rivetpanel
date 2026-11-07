package service

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/distribution/reference"
	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/lazyre"
	"github.com/xenycx/rivetpanel/internal/runtimes"
)

// ContainerPolicy is the administrator's policy for container apps: which
// registries images may come from and how large they may be. The runner
// checks it again at every start, so a narrowed policy applies to existing
// apps too.
type ContainerPolicy struct {
	Registries    []string
	MaxImageBytes int64
}

// Enabled reports whether any registry is allowed.
func (p ContainerPolicy) Enabled() bool { return len(p.Registries) > 0 }

// Normalize parses an image reference and returns its full form (registry,
// repository and a tag, ":latest" when none is given) after checking the
// registry against the policy.
func (p ContainerPolicy) Normalize(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 512 {
		return "", domain.Invalid("give an image such as nginx:1.27 or ghcr.io/owner/app:v1")
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", domain.Invalid(fmt.Sprintf("%q is not an image reference such as nginx:1.27 or ghcr.io/owner/app:v1", ref))
	}
	host := reference.Domain(named)
	ok := false
	for _, r := range p.Registries {
		if strings.EqualFold(r, host) {
			ok = true
			break
		}
	}
	if !ok {
		if len(p.Registries) == 0 {
			return "", domain.Invalid("container apps are turned off on this panel: no image registry is allowed")
		}
		return "", domain.Invalid(fmt.Sprintf("images from %s are not allowed on this panel (allowed: %s)", host, strings.Join(p.Registries, ", ")))
	}
	return reference.TagNameOnly(named).String(), nil
}

// Check refuses an image the policy does not allow (the runner's hook).
func (p ContainerPolicy) Check(ref string) error {
	_, err := p.Normalize(ref)
	return err
}

// ContainerStore is the storage container apps and stacks need.
type ContainerStore interface {
	PutContainerConfig(ctx context.Context, botID string, c domain.ContainerConfig) error
	SetContainerImage(ctx context.Context, botID, image string, c domain.ContainerConfig, nowMS int64) error
	CreateStack(ctx context.Context, s domain.Stack) error
	GetStack(ctx context.Context, id string) (domain.Stack, error)
	ListStacks(ctx context.Context) ([]domain.Stack, error)
	StackMembers(ctx context.Context, stackID string) ([]string, error)
	RenameStack(ctx context.Context, id, name string, nowMS int64) error
	DeleteStack(ctx context.Context, id string) error
	UsedBotHostPorts(ctx context.Context) (map[int]bool, error)
}

// ImagePuller pulls a container app's image again at its next start, so a
// moving tag such as :latest picks up a newer image.
type ImagePuller interface {
	RequestPull(botID string)
}

// ContainerService creates and changes container apps and stacks.
type ContainerService struct {
	Bots   *BotService
	Store  ContainerStore
	Policy ContainerPolicy
	Puller ImagePuller // nil without a local runner
}

// ContainerInput is a new container app.
type ContainerInput struct {
	Name        string
	WorkspaceID string
	Image       string
	// Command and Entrypoint override the image's (empty keeps the image's).
	Command    []string
	Entrypoint []string
	User       string
	Writable   []string
	Volumes    []domain.Volume
	Ports      []PortInput
	Env        map[string]string
	// MemoryBytes and NanoCPUs default to 512 MiB and one core.
	MemoryBytes     int64
	NanoCPUs        int64
	NetworkDisabled bool
	RestartNever    bool
}

const (
	maxContainerPaths = 8
	maxStackServices  = 20
)

var (
	volumeNameRe    = lazyre.New(`^[a-z0-9][a-z0-9_.-]{0,62}$`)
	containerPathRe = lazyre.New(`^/[A-Za-z0-9._@+-]+(/[A-Za-z0-9._@+-]+)*$`)
	numericUserRe   = lazyre.New(`^[0-9]{1,10}:[0-9]{1,10}$`)
	serviceNameRe   = lazyre.New(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
)

// Paths an app cannot claim: the kernel's and Docker's own mounts.
var reservedContainerPaths = []string{"/proc", "/sys", "/dev", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf"}

func checkContainerPath(p, what string) (string, error) {
	p = strings.TrimSpace(p)
	clean := path.Clean(p)
	if p == "" || !containerPathRe.MatchString(p) || clean != p || len(p) > 200 {
		return "", domain.Invalid(fmt.Sprintf("%s %q must be an absolute folder such as /var/cache/app", what, p))
	}
	for _, r := range reservedContainerPaths {
		if p == r || strings.HasPrefix(p, r+"/") {
			return "", domain.Invalid(fmt.Sprintf("%s %q is managed by Docker and cannot be changed", what, p))
		}
	}
	return p, nil
}

// checkContainerUser accepts "" (the panel's container user) or a numeric
// uid:gid that is not root.
func checkContainerUser(u string) (string, error) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", nil
	}
	if !numericUserRe.MatchString(u) {
		return "", domain.Invalid("user must be a numeric uid:gid such as 1000:1000; user names are not supported")
	}
	a, b, _ := strings.Cut(u, ":")
	uid, err1 := strconv.ParseInt(a, 10, 32)
	gid, err2 := strconv.ParseInt(b, 10, 32)
	if err1 != nil || err2 != nil || uid <= 0 || gid <= 0 {
		return "", domain.Invalid("containers cannot run as root: give a uid and gid above 0")
	}
	return u, nil
}

// checkContainerConfig validates and cleans a container's paths, volumes and
// user.
func checkContainerConfig(c domain.ContainerConfig) (domain.ContainerConfig, error) {
	var err error
	if c.User, err = checkContainerUser(c.User); err != nil {
		return c, err
	}
	if len(c.Writable) > maxContainerPaths || len(c.Volumes) > maxContainerPaths {
		return c, domain.Invalid(fmt.Sprintf("at most %d writable paths and %d volumes", maxContainerPaths, maxContainerPaths))
	}
	seen := map[string]string{}
	claim := func(p, what string) error {
		for q, w := range seen {
			if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return domain.Invalid(fmt.Sprintf("%s %s overlaps %s %s", what, p, w, q))
			}
		}
		seen[p] = what
		return nil
	}
	writable := make([]string, 0, len(c.Writable))
	for _, p := range c.Writable {
		if p, err = checkContainerPath(p, "writable path"); err != nil {
			return c, err
		}
		if p == "/tmp" {
			continue // always writable
		}
		if err := claim(p, "writable path"); err != nil {
			return c, err
		}
		writable = append(writable, p)
	}
	names := map[string]bool{}
	volumes := make([]domain.Volume, 0, len(c.Volumes))
	for _, v := range c.Volumes {
		v.Name = strings.TrimSpace(v.Name)
		if !volumeNameRe.MatchString(v.Name) {
			return c, domain.Invalid(fmt.Sprintf("volume name %q must be lowercase letters, digits, dots, dashes or underscores", v.Name))
		}
		if names[v.Name] {
			return c, domain.Invalid(fmt.Sprintf("volume %s is listed twice", v.Name))
		}
		names[v.Name] = true
		if v.Path, err = checkContainerPath(v.Path, "volume path"); err != nil {
			return c, err
		}
		if v.Path == "/tmp" {
			return c, domain.Invalid("/tmp is always an in-memory folder; mount the volume elsewhere")
		}
		if err := claim(v.Path, "volume"); err != nil {
			return c, err
		}
		volumes = append(volumes, v)
	}
	c.Writable, c.Volumes = writable, volumes
	return c, nil
}

// checkContainerCommand checks an optional command and entrypoint: the image
// decides which programs exist, so only their shape is checked.
func checkContainerCommand(argv, entry []string) error {
	if len(argv) > maxArgvItems || len(entry) > maxArgvItems {
		return domain.Invalid(fmt.Sprintf("a command can have at most %d items", maxArgvItems))
	}
	for i, a := range append(append([]string(nil), entry...), argv...) {
		if len(a) > maxArgvItemLen || strings.ContainsRune(a, 0) || !utf8.ValidString(a) {
			return domain.Invalid(fmt.Sprintf("item %d is too long or contains invalid characters", i))
		}
	}
	if len(entry) > 0 && entry[0] == "" {
		return domain.Invalid("the entrypoint's program cannot be empty")
	}
	return nil
}

// Create creates a standalone container app. It runs on the panel's own
// host and starts stopped.
func (c *ContainerService) Create(ctx context.Context, actor domain.User, in ContainerInput) (domain.Bot, error) {
	return c.create(ctx, actor, in, domain.ContainerConfig{}, nil)
}

func (c *ContainerService) create(ctx context.Context, actor domain.User, in ContainerInput, stack domain.ContainerConfig, budgeted *bool) (domain.Bot, error) {
	s := c.Bots
	if !actor.Can(domain.PermBotsCreate) {
		return domain.Bot{}, domain.ErrForbidden
	}
	name, err := validateName(in.Name)
	if err != nil {
		return domain.Bot{}, err
	}
	image, err := c.Policy.Normalize(in.Image)
	if err != nil {
		return domain.Bot{}, err
	}
	if err := checkContainerCommand(in.Command, in.Entrypoint); err != nil {
		return domain.Bot{}, err
	}
	cfg, err := checkContainerConfig(domain.ContainerConfig{User: in.User, Writable: in.Writable, Volumes: in.Volumes,
		StackID: stack.StackID, Service: stack.Service, DependsOn: stack.DependsOn})
	if err != nil {
		return domain.Bot{}, err
	}
	for n, v := range in.Env {
		if err := validateEnvName(n, false); err != nil {
			return domain.Bot{}, err
		}
		if err := validateEnvValue(v); err != nil {
			return domain.Bot{}, err
		}
	}
	rt := runtimes.ForImage(image)
	mem, cpu := in.MemoryBytes, in.NanoCPUs
	if mem == 0 {
		mem = rt.Defaults.MemoryBytes
	}
	if cpu == 0 {
		cpu = rt.Defaults.NanoCPUs
	}
	nodeID := s.LocalNode // container apps run on the panel's own host
	lim := s.LimitsFor(ctx, nodeID)
	mem, cpu = min(mem, lim.MaxMemoryBytes), min(cpu, lim.MaxNanoCPUs)
	if err := lim.validateResources(rt, mem, cpu, rt.Defaults.PidsLimit); err != nil {
		return domain.Bot{}, err
	}
	wsID, err := s.creatableWorkspace(ctx, actor, in.WorkspaceID)
	if err != nil {
		return domain.Bot{}, err
	}
	if budgeted == nil || !*budgeted {
		if err := s.checkWorkspace(ctx, actor, wsID, newApp(domain.KindContainer, mem, cpu)); err != nil {
			return domain.Bot{}, err
		}
	}
	argv := in.Command
	if argv == nil {
		argv = []string{}
	}
	policy := domain.RestartOnFailure
	if in.RestartNever {
		policy = domain.RestartNever
	}
	now := s.now()
	b := domain.Bot{
		ID: uuid.NewString(), OwnerID: actor.ID, WorkspaceID: wsID, NodeID: nodeID, Name: name,
		Kind: domain.KindContainer, Runtime: runtimes.ImageRuntimeID, ImageRef: image,
		Argv: argv, Entrypoint: in.Entrypoint, MemoryBytes: mem, NanoCPUs: cpu, PidsLimit: rt.Defaults.PidsLimit,
		DesiredState: domain.DesiredStopped, ObservedState: "stopped", CreatedAtMS: now, UpdatedAtMS: now,
		SourceType: "manual", RestartPolicy: policy, NetworkDisabled: in.NetworkDisabled,
	}
	if err := s.createWorkspace(nodeID, b.ID); err != nil {
		return domain.Bot{}, fmt.Errorf("create workspace: %w", err)
	}
	if err := s.createBot(ctx, actor, b, 0); err != nil {
		s.removeWorkspace(nodeID, b.ID)
		return domain.Bot{}, err
	}
	if err := c.Store.PutContainerConfig(ctx, b.ID, cfg); err != nil {
		s.discardNew(ctx, b)
		return domain.Bot{}, err
	}
	if len(in.Env) > 0 {
		if err := s.putEnv(ctx, b, in.Env, false); err != nil {
			s.discardNew(ctx, b)
			return domain.Bot{}, err
		}
	}
	if len(in.Ports) > 0 {
		ports, err := c.autoPorts(ctx, in.Ports)
		if err != nil {
			s.discardNew(ctx, b)
			return domain.Bot{}, err
		}
		if _, err := s.SetPorts(ctx, actor, b.ID, ports); err != nil {
			s.discardNew(ctx, b)
			return domain.Bot{}, err
		}
	}
	return s.Store.GetBot(ctx, b.ID)
}

// ContainerUpdate changes a container app's image settings; nil fields stay.
type ContainerUpdate struct {
	Image    *string
	User     *string
	Writable *[]string
	Volumes  *[]domain.Volume
}

// Update changes a container app's image settings. The change starts a new
// generation: a running container is replaced with the new settings.
// Removing a volume keeps its folder in the app's files.
func (c *ContainerService) Update(ctx context.Context, actor domain.User, id string, in ContainerUpdate) (domain.Bot, error) {
	s := c.Bots
	b, err := s.loadPerm(ctx, actor, id, domain.PermFullAdmin, false)
	if err != nil {
		return domain.Bot{}, err
	}
	if !b.IsContainer() {
		return domain.Bot{}, domain.Invalid("only container apps have image settings")
	}
	cfg := domain.ContainerConfig{}
	if b.Container != nil {
		cfg = *b.Container
	}
	image := b.ImageRef
	if in.Image != nil {
		if image, err = c.Policy.Normalize(*in.Image); err != nil {
			return domain.Bot{}, err
		}
	}
	if in.User != nil {
		cfg.User = *in.User
	}
	if in.Writable != nil {
		cfg.Writable = *in.Writable
	}
	if in.Volumes != nil {
		cfg.Volumes = *in.Volumes
	}
	if cfg, err = checkContainerConfig(cfg); err != nil {
		return domain.Bot{}, err
	}
	if err := c.Store.SetContainerImage(ctx, b.ID, image, cfg, s.now()); err != nil {
		return domain.Bot{}, err
	}
	if s.Notifier != nil {
		s.Notifier.Notify(b.ID)
	}
	return s.Store.GetBot(ctx, b.ID)
}

// Pull makes a container app pull its image again and, when it runs,
// restarts it on the newly pulled image.
func (c *ContainerService) Pull(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	s := c.Bots
	b, err := s.loadPerm(ctx, actor, id, domain.PermPower, false)
	if err != nil {
		return domain.Bot{}, err
	}
	if !b.IsContainer() {
		return domain.Bot{}, domain.Invalid("only container apps pull their own image")
	}
	if c.Puller == nil || s.Notifier == nil {
		return domain.Bot{}, domain.ErrRunnerUnavailable
	}
	c.Puller.RequestPull(b.ID)
	if b.DesiredState == domain.DesiredRunning {
		return s.Restart(ctx, actor, b.ID)
	}
	return b, nil
}

// errStackMember refuses moving one service of a stack on its own.
var errStackMember = domain.Invalid("this container belongs to a stack; its services stay together in the stack's workspace")

// stackMember reports whether a bot is a service of a stack.
func stackMember(b domain.Bot) bool { return b.Container != nil && b.Container.StackID != "" }

// autoPorts gives every port without a host port a free one from the
// published-port range.
func (c *ContainerService) autoPorts(ctx context.Context, in []PortInput) ([]PortInput, error) {
	out := append([]PortInput(nil), in...)
	need := false
	for _, p := range out {
		need = need || p.HostPort == 0
	}
	if !need {
		return out, nil
	}
	used, err := c.Store.UsedBotHostPorts(ctx)
	if err != nil {
		return nil, err
	}
	s := c.Bots
	if s.LocalPublished != nil {
		if pub, err := s.LocalPublished(ctx); err == nil {
			for _, p := range pub {
				used[p.HostPort] = true
			}
		}
	}
	for _, p := range out {
		used[p.HostPort] = true
	}
	lo, hi := s.Limits.PortMin, s.Limits.PortMax
	if lo == 0 && hi == 0 {
		lo, hi = 20000, 29999
	}
	next := lo
	for i := range out {
		if out[i].HostPort != 0 {
			continue
		}
		for next <= hi && used[next] {
			next++
		}
		if next > hi {
			return nil, domain.Invalid(fmt.Sprintf("no free host port is left in %d-%d", lo, hi))
		}
		out[i].HostPort, used[next] = next, true
	}
	return out, nil
}
