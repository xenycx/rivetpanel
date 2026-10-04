package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/gamequery"
	"github.com/xenycx/rivetpanel/internal/modrinth"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// GameStore is the persistence for blueprints, allocations and game servers.
type GameStore interface {
	ListBlueprints(ctx context.Context) ([]domain.Blueprint, error)
	GetBlueprint(ctx context.Context, idOrSlug string) (domain.Blueprint, error)
	GetBlueprintRevision(ctx context.Context, id string, rev int64) (domain.BlueprintRevision, error)
	ListBlueprintRevisions(ctx context.Context, id string) ([]domain.BlueprintRevision, error)
	SaveBlueprintRevision(ctx context.Context, b domain.Blueprint, yaml, sha string, nowMS int64) (domain.Blueprint, bool, error)
	SetBlueprintEnabled(ctx context.Context, id string, enabled bool, nowMS int64) error
	DeleteBlueprint(ctx context.Context, id string) error
	SetInstallState(ctx context.Context, botID, state string, imageChoice *string, nowMS int64) error
	UpdateGameServer(ctx context.Context, b domain.Bot, nowMS int64) error
	ListBotAllocations(ctx context.Context, botID string) ([]domain.Allocation, error)
	ListAllocations(ctx context.Context, nodeID string) ([]domain.Allocation, error)
	CreateAllocations(ctx context.Context, nodeID, ip string, ports []int, notes string, nowMS int64) ([]domain.Allocation, error)
	DeleteAllocation(ctx context.Context, id string) error
	SetAllocationAlias(ctx context.Context, id, alias, notes string) error
	AllocateForBot(ctx context.Context, botID, nodeID string, count, preferred int, contiguous, autoCreate bool, autoIP string,
		usable func(port int) bool, nowMS int64) ([]domain.Allocation, error)
	ReleaseAllocation(ctx context.Context, botID, allocID string) error
	SetPrimaryAllocation(ctx context.Context, botID, allocID string) error
}

// GameService manages game servers on top of the shared bot lifecycle.
type GameService struct {
	Bots      *BotService
	Store     GameStore
	Providers *blueprint.Providers
	Builtins  fs.FS // embedded first-party blueprints
	// LocalPortFree reports whether a port can be bound on the local node; nil
	// probes with a listener. Tests replace it.
	LocalPortFree func(port int) bool
	// Modrinth browses mods and plugins (nil = the public API).
	Modrinth *modrinth.Client
	// Files writes installed mods and plugins into the server's files.
	Files interface {
		WriteFile(botID, rel string, r io.Reader, max int64) error
	}
	// RemoteQuery pings a game server on a remote node through its agent.
	RemoteQuery func(ctx context.Context, nodeID string, port int) (gamequery.Status, error)
	// SpoolDir holds a mod or plugin for a remote server while its download
	// is verified, before it is sent to the node ("" = the system temporary
	// directory). Each file is removed as soon as the install ends.
	SpoolDir string
}

// BlueprintView is a blueprint with its current revision parsed.
type BlueprintView struct {
	domain.Blueprint
	Spec blueprint.Spec
}

// GameServerInput creates a game server.
type GameServerInput struct {
	Name        string
	Blueprint   string // id or slug
	Variables   map[string]string
	ImageChoice string // "" = automatic
	MemoryBytes int64
	NanoCPUs    int64
	Agreements  []string
	WorkspaceID string
	NodeID      string
}

// GameVariable is a variable with its current value, for the startup page.
type GameVariable struct {
	blueprint.Variable
	Value string
}

func (g *GameService) now() int64 { return g.Bots.now() }

// SyncBuiltins stores every embedded blueprint, adding a revision when one
// changed. Existing servers keep the revision they pinned.
func (g *GameService) SyncBuiltins(ctx context.Context) error {
	if g.Builtins == nil {
		return nil
	}
	all, err := blueprint.LoadBuiltin(g.Builtins)
	if err != nil {
		return err
	}
	for _, b := range all {
		if _, _, err := g.Store.SaveBlueprintRevision(ctx, domain.Blueprint{Slug: b.Spec.Slug, Name: b.Spec.Name,
			Category: b.Spec.Category, Description: b.Spec.Description, Source: "builtin"},
			string(b.Raw), blueprint.Hash(b.Raw), g.now()); err != nil {
			return fmt.Errorf("blueprint %s: %w", b.Spec.Slug, err)
		}
	}
	return nil
}

func (g *GameService) view(ctx context.Context, b domain.Blueprint) (BlueprintView, error) {
	rev, err := g.Store.GetBlueprintRevision(ctx, b.ID, b.CurrentRevision)
	if err != nil {
		return BlueprintView{}, err
	}
	s, err := blueprint.Parse([]byte(rev.SpecYAML))
	if err != nil {
		return BlueprintView{}, err
	}
	return BlueprintView{Blueprint: b, Spec: s}, nil
}

// ListBlueprints returns blueprints offered for new servers (administrators
// also see disabled ones).
func (g *GameService) ListBlueprints(ctx context.Context, actor domain.User) ([]BlueprintView, error) {
	all, err := g.Store.ListBlueprints(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BlueprintView, 0, len(all))
	for _, b := range all {
		if !b.Enabled && !actor.Can(domain.PermBlueprintsManage) {
			continue
		}
		v, err := g.view(ctx, b)
		if err != nil {
			continue // an unparsable revision is hidden rather than breaking the list
		}
		out = append(out, v)
	}
	return out, nil
}

// GetBlueprint returns one blueprint with its current revision.
func (g *GameService) GetBlueprint(ctx context.Context, actor domain.User, idOrSlug string) (BlueprintView, error) {
	b, err := g.Store.GetBlueprint(ctx, idOrSlug)
	if err != nil {
		return BlueprintView{}, err
	}
	if !b.Enabled && !actor.Can(domain.PermBlueprintsManage) {
		return BlueprintView{}, domain.ErrNotFound
	}
	return g.view(ctx, b)
}

// Versions lists the versions a variable offers through its provider.
func (g *GameService) Versions(ctx context.Context, actor domain.User, idOrSlug, env string) ([]blueprint.Version, error) {
	v, err := g.GetBlueprint(ctx, actor, idOrSlug)
	if err != nil {
		return nil, err
	}
	vr, ok := v.Spec.Variable(env)
	if !ok || vr.Versions == nil {
		return nil, domain.ErrNotFound
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := g.Providers.Versions(cctx, vr.Versions.Name, vr.Versions.Project)
	if err != nil {
		return nil, domain.Invalid("the version list could not be loaded right now; type a version instead")
	}
	return list, nil
}

// PortState is one port's answer from a remote node's port probe.
type PortState struct {
	Port   int
	Free   bool
	Reason string // why the port cannot be bound, in words
}

// checkRemotePorts refuses to start a stopped game server on a remote node
// when one of its allocated ports cannot be bound there, instead of letting
// Docker fail on the node. A running server holds its own ports, so only a
// settled stopped or failed server is probed. A node that cannot answer
// (offline) does not block the start: the request is recorded and the node
// acts on it when it reconnects.
func (s *BotService) checkRemotePorts(ctx context.Context, b domain.Bot) error {
	if !b.IsGame() || len(b.Allocations) == 0 || s.RemotePorts == nil || !s.remote(b.NodeID) {
		return nil
	}
	if b.ObservedState != "stopped" && b.ObservedState != "failed" {
		return nil
	}
	byIP := map[string][]int{}
	var ips []string
	for _, a := range b.Allocations {
		if _, ok := byIP[a.IP]; !ok {
			ips = append(ips, a.IP)
		}
		byIP[a.IP] = append(byIP[a.IP], a.Port)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, ip := range ips {
		res, err := s.RemotePorts(cctx, b.NodeID, ip, byIP[ip])
		if err != nil {
			return nil // offline or unreachable: the node reports the outcome
		}
		for _, r := range res {
			if !r.Free {
				return domain.Invalid(fmt.Sprintf("port %d on %s is %s; stop whatever uses it or choose another allocation under Network before starting",
					r.Port, ip, r.Reason))
			}
		}
	}
	return nil
}

// allocationProbe returns the port filter for an automatic allocation on
// nodeID starting at from. Local nodes bind-test each port. Remote nodes are
// asked through their agent in batches, the first one before the allocation
// transaction starts; probeErr reports a node that could not answer. Nil
// filter: no probing is available (no agent route wired).
func (g *GameService) allocationProbe(ctx context.Context, nodeID, ip string, from int) (usable func(int) bool, probeErr func() error) {
	none := func() error { return nil }
	if nodeID == g.Bots.LocalNode {
		return g.portFree, none
	}
	if g.Bots.RemotePorts == nil || !g.Bots.remote(nodeID) {
		return nil, none
	}
	if from < 1024 {
		from = 25565 // as AllocateForBot
	}
	known := map[int]PortState{}
	var failed error
	fetch := func(start int) {
		var ports []int
		for p := start; p <= 65535 && len(ports) < 64; p++ {
			ports = append(ports, p)
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		res, err := g.Bots.RemotePorts(cctx, nodeID, ip, ports)
		if err != nil {
			failed = err
			return
		}
		for _, r := range res {
			known[r.Port] = r
		}
	}
	fetch(from)
	usable = func(p int) bool {
		if failed != nil {
			return false
		}
		r, ok := known[p]
		if !ok {
			fetch(p)
			if r, ok = known[p]; !ok {
				return false
			}
		}
		return r.Free
	}
	probeErr = func() error {
		if failed == nil {
			return nil
		}
		return domain.Invalid("the node could not check which ports are free (" + failed.Error() + "); try again when it is online")
	}
	return usable, probeErr
}

func (g *GameService) portFree(port int) bool {
	if g.LocalPortFree != nil {
		return g.LocalPortFree(port)
	}
	for _, network := range []string{"tcp", "udp"} {
		addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
		if network == "tcp" {
			l, err := net.Listen(network, addr)
			if err != nil {
				return false
			}
			l.Close()
		} else {
			c, err := net.ListenPacket(network, addr)
			if err != nil {
				return false
			}
			c.Close()
		}
	}
	return true
}

func checkVariables(s blueprint.Spec, in map[string]string, onlyEditable bool) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range in {
		vr, ok := s.Variable(k)
		if !ok {
			return nil, domain.Invalid(fmt.Sprintf("%s is not a setting of this server type", k))
		}
		if onlyEditable && !vr.Editable {
			return nil, domain.Invalid(fmt.Sprintf("%s cannot be changed", vr.Name))
		}
		v = strings.TrimSpace(v)
		if err := vr.Check(v); err != nil {
			return nil, domain.Invalid(fmt.Sprintf("%s %s", vr.Name, err.Error()))
		}
		out[k] = v
	}
	return out, nil
}

// CreateServer creates a game server from a blueprint. It is installed on its
// first start.
func (g *GameService) CreateServer(ctx context.Context, actor domain.User, in GameServerInput) (domain.Bot, error) {
	s := g.Bots
	name, err := validateName(in.Name)
	if err != nil {
		return domain.Bot{}, err
	}
	bpRow, err := g.Store.GetBlueprint(ctx, in.Blueprint)
	if err != nil || !bpRow.Enabled {
		return domain.Bot{}, domain.Invalid("unknown server type")
	}
	bp, err := g.view(ctx, bpRow)
	if err != nil {
		return domain.Bot{}, err
	}
	spec := bp.Spec
	vars, err := checkVariables(spec, in.Variables, true)
	if err != nil {
		return domain.Bot{}, err
	}
	all := spec.Defaults()
	for k, v := range vars {
		all[k] = v
	}
	accepted := map[string]bool{}
	for _, a := range in.Agreements {
		accepted[a] = true
	}
	for _, a := range spec.Agreements {
		if !accepted[a.ID] {
			return domain.Bot{}, domain.Invalid(a.Text + " This is required to create the server.")
		}
		all[runner.AgreementVar(a.ID)] = "accepted"
	}
	if in.ImageChoice != "" {
		if _, ok := spec.Image(in.ImageChoice); !ok {
			return domain.Bot{}, domain.Invalid("unknown image")
		}
	}
	// Defaults come from the blueprint, capped by the panel's limits.
	mem, cpu := in.MemoryBytes, in.NanoCPUs
	if mem == 0 {
		mem = min(spec.Resources.MemoryMB<<20, s.Limits.MaxMemoryBytes)
	}
	if cpu == 0 {
		cpu = min(int64(spec.Resources.CPUs*1e9), s.Limits.MaxNanoCPUs)
	}
	minMem := max(s.Limits.MinMemoryBytes, spec.Resources.MinMemoryMB<<20)
	if mem < minMem || mem > s.Limits.MaxMemoryBytes {
		return domain.Bot{}, domain.Invalid(fmt.Sprintf("memory must be between %d MiB and %d MiB for %s", minMem>>20, s.Limits.MaxMemoryBytes>>20, spec.Name))
	}
	if cpu < s.Limits.MinNanoCPUs || cpu > s.Limits.MaxNanoCPUs {
		return domain.Bot{}, domain.Invalid(fmt.Sprintf("CPU must be between %.2f and %.2f cores", float64(s.Limits.MinNanoCPUs)/1e9, float64(s.Limits.MaxNanoCPUs)/1e9))
	}
	if err := s.checkUserBudget(ctx, actor, 1, mem); err != nil {
		return domain.Bot{}, err
	}
	wsID, err := s.creatableWorkspace(ctx, actor, in.WorkspaceID)
	if err != nil {
		return domain.Bot{}, err
	}
	nodeID, err := s.chooseNode(ctx, actor, in.NodeID)
	if err != nil {
		return domain.Bot{}, err
	}
	image := spec.Images[0]
	if in.ImageChoice != "" {
		image, _ = spec.Image(in.ImageChoice)
	}
	entry, argv := blueprint.StartupArgv(spec.Startup.Command)
	now := g.now()
	b := domain.Bot{
		ID: uuid.NewString(), OwnerID: actor.ID, WorkspaceID: wsID, NodeID: nodeID, Name: name,
		Kind: domain.KindGame, Runtime: spec.Runtime, BlueprintID: bpRow.ID, BlueprintRevision: bpRow.CurrentRevision,
		ImageChoice: in.ImageChoice, InstallState: domain.InstallPending, ImageRef: image.Ref,
		Argv: argv, Entrypoint: entry, MemoryBytes: mem, NanoCPUs: cpu, PidsLimit: spec.Resources.Pids,
		DesiredState: domain.DesiredStopped, ObservedState: "stopped", CreatedAtMS: now, UpdatedAtMS: now,
		SourceType: "manual", RestartPolicy: domain.RestartOnFailure,
	}
	if err := s.createWorkspace(nodeID, b.ID); err != nil {
		return domain.Bot{}, fmt.Errorf("create workspace: %w", err)
	}
	if err := s.Store.CreateBot(ctx, b); err != nil {
		s.removeWorkspace(nodeID, b.ID)
		return domain.Bot{}, err
	}
	undo := func() {
		_ = s.Store.MarkBotDeleted(ctx, b.ID, g.now())
		s.removeWorkspace(nodeID, b.ID)
		_ = s.Store.DeleteBotRow(ctx, b.ID)
	}
	usable, probeErr := g.allocationProbe(ctx, nodeID, "0.0.0.0", spec.Ports.Default)
	if err := probeErr(); err != nil {
		undo()
		return domain.Bot{}, err
	}
	if _, err := g.Store.AllocateForBot(ctx, b.ID, nodeID, 1+spec.Ports.Extra, spec.Ports.Default, spec.Ports.Contiguous, true, "0.0.0.0", usable, now); err != nil {
		undo()
		if perr := probeErr(); perr != nil {
			return domain.Bot{}, perr
		}
		return domain.Bot{}, err
	}
	if err := s.putEnv(ctx, b, all, true); err != nil {
		undo()
		return domain.Bot{}, err
	}
	return s.Store.GetBot(ctx, b.ID)
}

func (g *GameService) loadGame(ctx context.Context, actor domain.User, id string, perm int) (domain.Bot, error) {
	b, err := g.Bots.loadPerm(ctx, actor, id, perm, false)
	if err != nil {
		return b, err
	}
	if !b.IsGame() {
		return b, domain.ErrNotFound
	}
	return b, nil
}

func (g *GameService) spec(ctx context.Context, b domain.Bot) (blueprint.Spec, error) {
	rev, err := g.Store.GetBlueprintRevision(ctx, b.BlueprintID, b.BlueprintRevision)
	if err != nil {
		return blueprint.Spec{}, err
	}
	return blueprint.Parse([]byte(rev.SpecYAML))
}

func requireStopped(b domain.Bot) error {
	if b.DesiredState != domain.DesiredStopped || (b.ObservedState != "stopped" && b.ObservedState != "failed" && b.ObservedState != "unknown") {
		return domain.Invalid("stop the server first")
	}
	return nil
}

// GameDetail is everything the startup page shows.
type GameDetail struct {
	Spec            blueprint.Spec
	Blueprint       domain.Blueprint
	Variables       []GameVariable
	UpdateAvailable bool
}

// Detail returns the server's blueprint, variables and update state.
func (g *GameService) Detail(ctx context.Context, actor domain.User, id string) (GameDetail, error) {
	b, err := g.loadGame(ctx, actor, id, permAny)
	if err != nil {
		return GameDetail{}, err
	}
	s, err := g.spec(ctx, b)
	if err != nil {
		return GameDetail{}, err
	}
	bp, err := g.Store.GetBlueprint(ctx, b.BlueprintID)
	if err != nil {
		return GameDetail{}, err
	}
	vals, err := g.Bots.DecryptEnv(ctx, b.ID)
	if err != nil {
		return GameDetail{}, err
	}
	out := GameDetail{Spec: s, Blueprint: bp, UpdateAvailable: bp.CurrentRevision > b.BlueprintRevision}
	for _, v := range s.Variables {
		val, ok := vals[v.Env]
		if !ok {
			val = v.Default
		}
		out.Variables = append(out.Variables, GameVariable{Variable: v, Value: val})
	}
	return out, nil
}

// SetVariables changes editable settings of a stopped server. Changing a
// setting marked "reinstall" (such as the version) reinstalls the server
// software on the next start; files and worlds are kept.
func (g *GameService) SetVariables(ctx context.Context, actor domain.User, id string, in map[string]string) (domain.Bot, error) {
	b, err := g.loadGame(ctx, actor, id, domain.PermManageEnv)
	if err != nil {
		return b, err
	}
	if err := requireStopped(b); err != nil {
		return b, err
	}
	s, err := g.spec(ctx, b)
	if err != nil {
		return b, err
	}
	vars, err := checkVariables(s, in, true)
	if err != nil {
		return b, err
	}
	if len(vars) == 0 {
		return b, domain.Invalid("no settings provided")
	}
	cur, err := g.Bots.DecryptEnv(ctx, b.ID)
	if err != nil {
		return b, err
	}
	reinstall := false
	for k, v := range vars {
		vr, _ := s.Variable(k)
		if vr.Reinstall && cur[k] != v {
			reinstall = true
		}
	}
	if err := g.Bots.putEnv(ctx, b, vars, false); err != nil {
		return b, err
	}
	if reinstall {
		if err := g.Store.SetInstallState(ctx, b.ID, domain.InstallPending, nil, g.now()); err != nil {
			return b, err
		}
	}
	return g.Bots.Store.GetBot(ctx, b.ID)
}

// Reinstall runs the installation again on the next start.
func (g *GameService) Reinstall(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	b, err := g.loadGame(ctx, actor, id, domain.PermFullAdmin)
	if err != nil {
		return b, err
	}
	if err := requireStopped(b); err != nil {
		return b, err
	}
	b.InstallState = domain.InstallPending
	if err := g.Store.UpdateGameServer(ctx, b, g.now()); err != nil {
		return b, err
	}
	return g.Bots.Store.GetBot(ctx, b.ID)
}

// SetImage chooses the container image ("" = automatic, decided at the next
// installation).
func (g *GameService) SetImage(ctx context.Context, actor domain.User, id, label string) (domain.Bot, error) {
	b, err := g.loadGame(ctx, actor, id, domain.PermFullAdmin)
	if err != nil {
		return b, err
	}
	if err := requireStopped(b); err != nil {
		return b, err
	}
	s, err := g.spec(ctx, b)
	if err != nil {
		return b, err
	}
	im := s.Images[0]
	if label != "" {
		var ok bool
		if im, ok = s.Image(label); !ok {
			return b, domain.Invalid("unknown image")
		}
	}
	b.ImageChoice, b.ImageRef = label, im.Ref
	if label == "" && b.InstallState == domain.InstallInstalled {
		b.InstallState = domain.InstallPending // automatic selection happens during installation
	}
	if err := g.Store.UpdateGameServer(ctx, b, g.now()); err != nil {
		return b, err
	}
	return g.Bots.Store.GetBot(ctx, b.ID)
}

// UpgradeBlueprint moves a stopped server to its blueprint's newest revision.
func (g *GameService) UpgradeBlueprint(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	b, err := g.loadGame(ctx, actor, id, domain.PermFullAdmin)
	if err != nil {
		return b, err
	}
	if err := requireStopped(b); err != nil {
		return b, err
	}
	bp, err := g.Store.GetBlueprint(ctx, b.BlueprintID)
	if err != nil {
		return b, err
	}
	if bp.CurrentRevision == b.BlueprintRevision {
		return b, nil
	}
	v, err := g.view(ctx, bp)
	if err != nil {
		return b, err
	}
	b.BlueprintRevision = bp.CurrentRevision
	b.Entrypoint, b.Argv = blueprint.StartupArgv(v.Spec.Startup.Command)
	if _, ok := v.Spec.Image(b.ImageChoice); !ok {
		b.ImageChoice = ""
	}
	b.ImageRef = gameImageRef(v.Spec, b.ImageChoice)
	if err := g.Store.UpdateGameServer(ctx, b, g.now()); err != nil {
		return b, err
	}
	// New settings start at their defaults.
	cur, err := g.Bots.DecryptEnv(ctx, b.ID)
	if err != nil {
		return b, err
	}
	missing := map[string]string{}
	for _, vr := range v.Spec.Variables {
		if _, ok := cur[vr.Env]; !ok {
			missing[vr.Env] = vr.Default
		}
	}
	if len(missing) > 0 {
		if err := g.Bots.putEnv(ctx, b, missing, false); err != nil {
			return b, err
		}
	}
	return g.Bots.Store.GetBot(ctx, b.ID)
}

func gameImageRef(s blueprint.Spec, label string) string {
	if im, ok := s.Image(label); ok {
		return im.Ref
	}
	return s.Images[0].Ref
}

// AddAllocation gives a stopped server one more allocation from its node.
func (g *GameService) AddAllocation(ctx context.Context, actor domain.User, id string) ([]domain.Allocation, error) {
	b, err := g.loadGame(ctx, actor, id, domain.PermFullAdmin)
	if err != nil {
		return nil, err
	}
	if err := requireStopped(b); err != nil {
		return nil, err
	}
	if len(b.Allocations) >= 11 {
		return nil, domain.Invalid("a server can have at most 11 allocations")
	}
	start := 25565
	if p, ok := b.PrimaryAllocation(); ok {
		start = p.Port + 1
	}
	usable, probeErr := g.allocationProbe(ctx, b.NodeID, "0.0.0.0", start)
	if err := probeErr(); err != nil {
		return nil, err
	}
	out, err := g.Store.AllocateForBot(ctx, b.ID, b.NodeID, 1, start, false, true, "0.0.0.0", usable, g.now())
	if err != nil {
		if perr := probeErr(); perr != nil {
			return nil, perr
		}
		return nil, err
	}
	return out, g.bump(ctx, b)
}

func (g *GameService) bump(ctx context.Context, b domain.Bot) error {
	return g.Store.UpdateGameServer(ctx, b, g.now())
}

// RemoveAllocation returns a non-primary allocation to the pool.
func (g *GameService) RemoveAllocation(ctx context.Context, actor domain.User, id, allocID string) error {
	b, err := g.loadGame(ctx, actor, id, domain.PermFullAdmin)
	if err != nil {
		return err
	}
	if err := requireStopped(b); err != nil {
		return err
	}
	if err := g.Store.ReleaseAllocation(ctx, b.ID, allocID); err != nil {
		return err
	}
	return g.bump(ctx, b)
}

// SetPrimaryAllocation chooses the allocation passed as SERVER_PORT.
func (g *GameService) SetPrimaryAllocation(ctx context.Context, actor domain.User, id, allocID string) error {
	b, err := g.loadGame(ctx, actor, id, domain.PermFullAdmin)
	if err != nil {
		return err
	}
	if err := requireStopped(b); err != nil {
		return err
	}
	return g.Store.SetPrimaryAllocation(ctx, b.ID, allocID)
}

// Query asks a running server for its public status (players, version).
func (g *GameService) Query(ctx context.Context, actor domain.User, id string) (gamequery.Status, error) {
	b, err := g.loadGame(ctx, actor, id, permAny)
	if err != nil {
		return gamequery.Status{}, err
	}
	s, err := g.spec(ctx, b)
	if err != nil {
		return gamequery.Status{}, err
	}
	p, ok := queryAllocation(s, b)
	if s.Query == "" || !ok || b.ObservedState != "running" {
		return gamequery.Status{}, nil
	}
	host := p.IP
	if host == "0.0.0.0" || host == "" || host == "::" {
		host = "127.0.0.1"
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if g.Bots.remote(b.NodeID) {
		if g.RemoteQuery == nil || s.Query != "minecraft-java" {
			// Steam A2S through an agent is not implemented (the node
			// protocol only carries the Minecraft ping).
			return gamequery.Status{}, nil
		}
		st, err := g.RemoteQuery(cctx, b.NodeID, p.Port) // the agent pings the published port
		if err != nil {
			return gamequery.Status{}, nil
		}
		return st, nil
	}
	addr := net.JoinHostPort(host, strconv.Itoa(p.Port))
	var st gamequery.Status
	if s.Query == "steam" {
		st, err = gamequery.SteamA2S(cctx, addr)
	} else {
		st, err = gamequery.MinecraftJava(cctx, addr)
	}
	if err != nil {
		return gamequery.Status{}, nil // starting, or not answering: simply offline
	}
	return st, nil
}

// queryAllocation is the allocation a blueprint's query targets: the
// primary one, or the n-th additional allocation in ascending port order
// (the same order as SERVER_PORT_<n>).
func queryAllocation(s blueprint.Spec, b domain.Bot) (domain.Allocation, bool) {
	if s.QueryAllocation == 0 {
		return b.PrimaryAllocation()
	}
	var extra []domain.Allocation
	for _, a := range b.Allocations {
		if !a.Primary {
			extra = append(extra, a)
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Port < extra[j].Port })
	if s.QueryAllocation > len(extra) {
		return domain.Allocation{}, false
	}
	return extra[s.QueryAllocation-1], true
}

// --- Mods and plugins ---

func (g *GameService) modrinth() *modrinth.Client {
	if g.Modrinth != nil {
		return g.Modrinth
	}
	return &modrinth.Client{}
}

// gameVersion is the Minecraft version mods must support: the installed
// version, else the configured one (unless "latest").
func (g *GameService) gameVersion(ctx context.Context, b domain.Bot, s blueprint.Spec) string {
	if b.InstalledVersion != "" {
		return b.InstalledVersion
	}
	if s.JavaFrom != "" {
		if vals, err := g.Bots.DecryptEnv(ctx, b.ID); err == nil {
			if v := vals[s.JavaFrom]; v != "" && v != "latest" {
				return v
			}
		}
	}
	return ""
}

// AddonContext is what the mod and plugin browser needs to know.
type AddonContext struct {
	Source      blueprint.AddonSource
	GameVersion string
}

func (g *GameService) addonSource(ctx context.Context, actor domain.User, id string, perm int) (domain.Bot, blueprint.Spec, error) {
	b, err := g.loadGame(ctx, actor, id, perm)
	if err != nil {
		return b, blueprint.Spec{}, err
	}
	s, err := g.spec(ctx, b)
	if err != nil {
		return b, s, err
	}
	if s.Addons == nil {
		return b, s, domain.Invalid("this server type has no mod or plugin browser")
	}
	return b, s, nil
}

// AddonSearch searches Modrinth for mods or plugins that fit the server.
func (g *GameService) AddonSearch(ctx context.Context, actor domain.User, id, query string, offset int) (AddonContext, []modrinth.Hit, int, error) {
	b, s, err := g.addonSource(ctx, actor, id, permAny)
	if err != nil {
		return AddonContext{}, nil, 0, err
	}
	ac := AddonContext{Source: *s.Addons, GameVersion: g.gameVersion(ctx, b, s)}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	hits, total, err := g.modrinth().Search(cctx, strings.TrimSpace(query), s.Addons.Loaders, ac.GameVersion, offset)
	if err != nil {
		return ac, nil, 0, domain.Invalid("Modrinth could not be reached right now")
	}
	return ac, hits, total, nil
}

// AddonVersions lists a project's versions that fit the server.
func (g *GameService) AddonVersions(ctx context.Context, actor domain.User, id, project string) ([]modrinth.Version, error) {
	b, s, err := g.addonSource(ctx, actor, id, permAny)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	vs, err := g.modrinth().Versions(cctx, project, s.Addons.Loaders, g.gameVersion(ctx, b, s))
	if errors.Is(err, modrinth.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, domain.Invalid("Modrinth could not be reached right now")
	}
	if len(vs) > 30 {
		vs = vs[:30]
	}
	return vs, nil
}

// AddonInstallResult reports an installed file and dependencies it needs.
type AddonInstallResult struct {
	Filename string
	Path     string
	Required []string // project IDs of required dependencies
}

// AddonInstall downloads a version's primary file into the server's mods or
// plugins folder after checking that it fits the server's loader. versionID
// "" picks the newest fitting version.
func (g *GameService) AddonInstall(ctx context.Context, actor domain.User, id, project, versionID string) (AddonInstallResult, error) {
	b, s, err := g.addonSource(ctx, actor, id, domain.PermEditFiles)
	if err != nil {
		return AddonInstallResult{}, err
	}
	nf, remote, err := g.Bots.RemoteFiles(b, "Installing from Modrinth")
	if err != nil {
		return AddonInstallResult{}, err
	}
	if !remote && g.Files == nil {
		return AddonInstallResult{}, domain.Invalid("server files are not available")
	}
	if err := g.Bots.FilesBlocked(b.ID); err != nil {
		return AddonInstallResult{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var v modrinth.Version
	if versionID != "" {
		if v, err = g.modrinth().Version(cctx, versionID); err != nil {
			return AddonInstallResult{}, domain.Invalid("that version was not found on Modrinth")
		}
		if project != "" && v.ProjectID != project {
			return AddonInstallResult{}, domain.Invalid("that version belongs to another project")
		}
	} else {
		vs, err := g.modrinth().Versions(cctx, project, s.Addons.Loaders, g.gameVersion(ctx, b, s))
		if err != nil || len(vs) == 0 {
			return AddonInstallResult{}, domain.Invalid("no version of it fits this server's loader and Minecraft version")
		}
		v = vs[0]
	}
	fits := false
	for _, l := range v.Loaders {
		for _, want := range s.Addons.Loaders {
			fits = fits || l == want
		}
	}
	if !fits {
		return AddonInstallResult{}, domain.Invalid("that version does not support this server's loader (" + strings.Join(s.Addons.Loaders, ", ") + ")")
	}
	f, ok := v.PrimaryFile()
	if !ok || !modrinth.ValidFilename(f.Filename) {
		return AddonInstallResult{}, domain.Invalid("that version has no installable jar file")
	}
	rel := s.Addons.Dir + "/" + f.Filename
	if remote {
		if err := g.installRemote(cctx, nf, b, rel, f); err != nil {
			return AddonInstallResult{}, err
		}
	} else {
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(g.modrinth().Fetch(cctx, f, pw)) }()
		if err := g.Files.WriteFile(b.ID, rel, pr, modrinth.MaxFileBytes); err != nil {
			pr.CloseWithError(err)
			if strings.Contains(err.Error(), "checksum") {
				return AddonInstallResult{}, domain.Invalid("the download did not match its published checksum; nothing was installed")
			}
			return AddonInstallResult{}, domain.Invalid("the download did not complete; nothing was installed")
		}
	}
	out := AddonInstallResult{Filename: f.Filename, Path: rel}
	for _, d := range v.Dependencies {
		if d.DependencyType == "required" && d.ProjectID != "" {
			out.Required = append(out.Required, d.ProjectID)
		}
	}
	return out, nil
}

// installRemote installs a mod or plugin on a server's remote node. The
// panel downloads the file into a private temporary spool and checks its
// host, size and SHA-512 completely before a single byte is sent, so the
// node only ever receives a verified file. (Streaming straight through would
// let the node finish writing a file of the expected length before the
// checksum could be compared.) The verified file is then streamed to the
// node, which writes it to a temporary file and renames it into place; the
// size the node reports back must match.
func (g *GameService) installRemote(ctx context.Context, nf NodeFiles, b domain.Bot, rel string, f modrinth.File) error {
	tmp, err := os.CreateTemp(g.SpoolDir, "rivetpanel-modrinth-*")
	if err != nil {
		return fmt.Errorf("spool download: %w", err)
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	if err := g.modrinth().Fetch(ctx, f, tmp); err != nil {
		if strings.Contains(err.Error(), "checksum") {
			return domain.Invalid("the download did not match its published checksum; nothing was installed")
		}
		return domain.Invalid("the download did not complete; nothing was installed")
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rev, err := nf.WriteFileFrom(ctx, b.NodeID, b.ID, rel, tmp, size, "", false)
	if err != nil {
		var inv *domain.ValidationError
		if errors.As(err, &inv) {
			return err
		}
		if errors.Is(err, filesystem.ErrTooLarge) {
			return domain.Invalid("the file is larger than the node accepts; nothing was installed")
		}
		return fmt.Errorf("send the file to the node: %w", err)
	}
	if n, ok := filesystem.RevisionSize(rev); !ok || n != size {
		return fmt.Errorf("the node did not confirm the installed file (%s)", rev)
	}
	return nil
}

// --- Administration ---

// requirePerm refuses an actor without the administration permission.
func requirePerm(actor domain.User, perm string) error {
	if !actor.Can(perm) {
		return domain.ErrForbidden
	}
	return nil
}

// ImportBlueprint stores a custom blueprint (new slug) or a new revision of
// an existing custom one.
func (g *GameService) ImportBlueprint(ctx context.Context, actor domain.User, yaml []byte) (domain.Blueprint, bool, error) {
	if err := requirePerm(actor, domain.PermBlueprintsManage); err != nil {
		return domain.Blueprint{}, false, err
	}
	s, err := blueprint.Parse(yaml)
	if err != nil {
		return domain.Blueprint{}, false, domain.Invalid(err.Error())
	}
	return g.Store.SaveBlueprintRevision(ctx, domain.Blueprint{Slug: s.Slug, Name: s.Name, Category: s.Category,
		Description: s.Description, Source: "custom"}, string(yaml), blueprint.Hash(yaml), g.now())
}

// ConvertEgg turns an uploaded Pterodactyl egg into a blueprint draft for
// review. Nothing is stored: the administrator saves the reviewed YAML with
// ImportBlueprint. The egg is untrusted; see blueprint.ConvertEgg.
func (g *GameService) ConvertEgg(ctx context.Context, actor domain.User, egg []byte) (blueprint.EggDraft, error) {
	if err := requirePerm(actor, domain.PermBlueprintsManage); err != nil {
		return blueprint.EggDraft{}, err
	}
	d, err := blueprint.ConvertEgg(egg)
	if err != nil {
		return blueprint.EggDraft{}, domain.Invalid(err.Error())
	}
	if d.Error == "" {
		if s, err := blueprint.Parse([]byte(d.YAML)); err == nil {
			if b, err := g.Store.GetBlueprint(ctx, s.Slug); err == nil {
				if b.Source == "builtin" {
					d.Warnings = append(d.Warnings, "A built-in server type already uses the slug "+s.Slug+"; change the slug before saving.")
				} else {
					d.Warnings = append(d.Warnings, "A custom server type already uses the slug "+s.Slug+"; saving adds a new revision to it. Change the slug to create a separate type.")
				}
			}
		}
	}
	return d, nil
}

// ExportBlueprint returns a revision's exact YAML (0 = current).
func (g *GameService) ExportBlueprint(ctx context.Context, actor domain.User, idOrSlug string, rev int64) (domain.BlueprintRevision, error) {
	b, err := g.GetBlueprint(ctx, actor, idOrSlug)
	if err != nil {
		return domain.BlueprintRevision{}, err
	}
	if rev == 0 {
		rev = b.CurrentRevision
	}
	return g.Store.GetBlueprintRevision(ctx, b.ID, rev)
}

// Revisions lists a blueprint's revisions.
func (g *GameService) Revisions(ctx context.Context, actor domain.User, idOrSlug string) ([]domain.BlueprintRevision, error) {
	if err := requirePerm(actor, domain.PermBlueprintsManage); err != nil {
		return nil, err
	}
	b, err := g.Store.GetBlueprint(ctx, idOrSlug)
	if err != nil {
		return nil, err
	}
	return g.Store.ListBlueprintRevisions(ctx, b.ID)
}

// SetBlueprintEnabled offers or hides a blueprint for new servers.
func (g *GameService) SetBlueprintEnabled(ctx context.Context, actor domain.User, id string, enabled bool) error {
	if err := requirePerm(actor, domain.PermBlueprintsManage); err != nil {
		return err
	}
	return g.Store.SetBlueprintEnabled(ctx, id, enabled, g.now())
}

// DeleteBlueprint removes an unused custom blueprint.
func (g *GameService) DeleteBlueprint(ctx context.Context, actor domain.User, id string) error {
	if err := requirePerm(actor, domain.PermBlueprintsManage); err != nil {
		return err
	}
	err := g.Store.DeleteBlueprint(ctx, id)
	if errors.Is(err, domain.ErrConflict) {
		return domain.Invalid("only unused custom blueprints can be deleted; disable it instead")
	}
	return err
}

// ParsePorts reads "25565", "25565-25570" and comma-separated mixtures (at
// most 1,000 ports).
func ParsePorts(s string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || a < 1024 || b > 65535 || a > b {
			return nil, domain.Invalid(fmt.Sprintf("%q is not a port or range between 1024 and 65535", part))
		}
		for p := a; p <= b; p++ {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			if len(out) > 1000 {
				return nil, domain.Invalid("at most 1,000 ports at a time")
			}
		}
	}
	if len(out) == 0 {
		return nil, domain.Invalid("no ports given")
	}
	sort.Ints(out)
	return out, nil
}

// CreateAllocations adds ports on a node (administrators).
func (g *GameService) CreateAllocations(ctx context.Context, actor domain.User, nodeID, ip, ports, notes string) ([]domain.Allocation, error) {
	if err := requirePerm(actor, domain.PermAllocations); err != nil {
		return nil, err
	}
	if nodeID == "" {
		nodeID = g.Bots.LocalNode
	}
	if _, err := g.Bots.Store.GetNode(ctx, nodeID); err != nil {
		return nil, domain.Invalid("unknown node")
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "0.0.0.0"
	}
	if net.ParseIP(ip) == nil {
		return nil, domain.Invalid("the address must be an IP address such as 0.0.0.0")
	}
	if len(notes) > 200 {
		return nil, domain.Invalid("notes must be at most 200 characters")
	}
	list, err := ParsePorts(ports)
	if err != nil {
		return nil, err
	}
	return g.Store.CreateAllocations(ctx, nodeID, ip, list, notes, g.now())
}

// ListAllocations returns a node's allocations (administrators).
func (g *GameService) ListAllocations(ctx context.Context, actor domain.User, nodeID string) ([]domain.Allocation, error) {
	if err := requirePerm(actor, domain.PermAllocations); err != nil {
		return nil, err
	}
	return g.Store.ListAllocations(ctx, nodeID)
}

// DeleteAllocation removes an unused allocation (administrators).
func (g *GameService) DeleteAllocation(ctx context.Context, actor domain.User, id string) error {
	if err := requirePerm(actor, domain.PermAllocations); err != nil {
		return err
	}
	err := g.Store.DeleteAllocation(ctx, id)
	if errors.Is(err, domain.ErrConflict) {
		return domain.Invalid("this allocation belongs to a server; remove it from the server first")
	}
	return err
}

// SetAllocationAlias sets the address shown to players (administrators).
func (g *GameService) SetAllocationAlias(ctx context.Context, actor domain.User, id, alias, notes string) error {
	if err := requirePerm(actor, domain.PermAllocations); err != nil {
		return err
	}
	alias = strings.TrimSpace(alias)
	if len(alias) > 253 || strings.ContainsAny(alias, " /\\\x00") || len(notes) > 200 {
		return domain.Invalid("the alias must be a host name or address")
	}
	return g.Store.SetAllocationAlias(ctx, id, alias, notes)
}
