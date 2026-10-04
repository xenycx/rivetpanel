package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/templates"
)

// Workspaces is the filesystem surface used for bot lifecycle.
type Workspaces interface {
	Create(botID string) error
	Remove(botID string) error
}

// Notifier tells the runner that a bot's intent changed. The persisted desired
// state is the durable record; notification only reduces latency.
type Notifier interface{ Notify(botID string) }

// Purger removes everything belonging to a bot marked deleted (containers,
// workspace, database row). It must be idempotent.
type Purger interface {
	Purge(ctx context.Context, botID string) error
}

// BotService implements bot configuration rules and ownership checks.
type BotService struct {
	Store        Store
	Catalog      *runtimes.Catalog
	Keys         *secrets.Keyring
	Workspaces   Workspaces
	Limits       Limits
	LocalNode    string
	Now          func() time.Time
	Notifier     Notifier                                // nil disables lifecycle requests
	Purger       Purger                                  // nil: workspace+row cleanup inline (no runner, no containers)
	Bus          *events.Bus                             // optional: status fan-out to live views
	Killer       Killer                                  // optional: immediate SIGKILL
	OnDelete     func(botID string)                      // optional: called after a bot is deleted (e.g. purge its backups)
	BeforeDelete func(ctx context.Context, botID string) // optional best-effort cleanup (e.g. remove the GitHub webhook)
	Files        *filesystem.Manager                     // optional: needed to seed templates
	Coord        *Coordinator                            // per-bot operation reservations (nil = none)
	AddonData    AddonData                               // nil: add-ons unavailable
	AddonRuntime AddonRuntime                            // optional: add-on state and logs
	DiscordAPI   string                                  // Discord REST base (tests); "" = discord.com
	// Stdin attaches to a running container's input for one-shot commands
	// (scheduled commands, player actions). Nil disables them.
	Stdin interface {
		AttachStdin(ctx context.Context, containerID string) (io.WriteCloser, error)
	}
	// StdinFor, when set, picks the input source for a server's node.
	StdinFor func(nodeID string) interface {
		AttachStdin(ctx context.Context, containerID string) (io.WriteCloser, error)
	}
	// RemoteNode reports whether a node is run by a rivet-agent: its servers'
	// files live on that node, not under this panel's data directory.
	RemoteNode func(nodeID string) bool
	// NodeFiles reads and writes single files of servers on remote nodes
	// (package manager, AI assistant). Nil refuses those features there.
	NodeFiles NodeFiles
	// RemoteAddons observes add-ons of servers on remote nodes.
	RemoteAddons NodeAddons
	// RemotePorts asks a remote node's agent whether host ports can be
	// bound there (a check, not a reservation). Nil skips remote probing.
	RemotePorts func(ctx context.Context, nodeID, ip string, ports []int) ([]PortState, error)
	// Notices receives access notifications (sharing, transfers, workspace
	// membership, accepted invitations); nil sends none.
	Notices *NotificationService
}

// actorName is how notifications name the account that acted.
func actorName(u domain.User) string {
	if u.DisplayName != "" {
		return u.DisplayName + " (" + u.Email + ")"
	}
	return u.Email
}

// noticeAccess tells another account about an access change.
func (s *BotService) noticeAccess(ctx context.Context, actor domain.User, userID, title, body, link string) {
	if s.Notices == nil || userID == actor.ID {
		return
	}
	s.Notices.Notify(ctx, userID, Notice{Category: domain.NotifyAccess, Title: title, Body: body, Link: link})
}

func (s *BotService) remote(nodeID string) bool { return s.RemoteNode != nil && s.RemoteNode(nodeID) }

// NodeFiles reaches the workspace of a server that runs on a remote node,
// through its rivet-agent's file routes (noderoute.Router). Path containment
// is enforced by the node's filesystem layer; callers authorize the user and
// bound every size first. A missing file is fs.ErrNotExist, a failed write
// precondition domain.ErrConflict and a stale patch filesystem.ErrPatchConflict.
type NodeFiles interface {
	Online(nodeID string) bool
	ListDir(ctx context.Context, nodeID, botID, dir string) ([]filesystem.Entry, error)
	ReadFileRevision(ctx context.Context, nodeID, botID, p string, max int64) ([]byte, string, error)
	WriteFile(ctx context.Context, nodeID, botID, p string, data []byte, ifMatch string, createOnly bool) (string, error)
	// WriteFileFrom streams exactly size bytes from src into one file.
	WriteFileFrom(ctx context.Context, nodeID, botID, p string, src io.Reader, size int64, ifMatch string, createOnly bool) (string, error)
	BeginPatch(ctx context.Context, nodeID, botID string, files []filesystem.PatchFile, maxFile, maxTotal int64) (string, map[string]string, error)
	// CreateWorkspace makes a new server's directory on the node (idempotent).
	CreateWorkspace(ctx context.Context, nodeID, botID string) error
	CompleteTransaction(ctx context.Context, nodeID, botID, tx string, commit bool) error
}

// RemoteFiles reports whether b's files live on a remote node and, if so,
// returns the access to them. It refuses cleanly when the node is offline
// (or remote file access is not configured), so callers never fall back to
// the panel's own disk for a remote server.
func (s *BotService) RemoteFiles(b domain.Bot, what string) (NodeFiles, bool, error) {
	if !s.remote(b.NodeID) {
		return nil, false, nil
	}
	if s.NodeFiles == nil {
		return nil, true, domain.Invalid(what + " is not available for servers on remote nodes on this panel")
	}
	if !s.NodeFiles.Online(b.NodeID) {
		return nil, true, domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	return s.NodeFiles, true, nil
}

// chooseNode resolves the node a new server is created on: this panel's own
// node unless an administrator picks another eligible one.
func (s *BotService) chooseNode(ctx context.Context, actor domain.User, nodeID string) (string, error) {
	if nodeID == "" {
		nodeID = s.LocalNode
	}
	if nodeID != s.LocalNode && !actor.Can(domain.PermNodesManage) {
		return "", domain.Invalid("only administrators can choose the node for a new server")
	}
	if err := s.placeable(ctx, nodeID); err != nil {
		return "", err
	}
	return nodeID, nil
}

// placeable checks that new servers may be placed on a node.
func (s *BotService) placeable(ctx context.Context, nodeID string) error {
	node, err := s.Store.GetNode(ctx, nodeID)
	if err != nil || !node.Enabled {
		return domain.Invalid("node is unavailable")
	}
	if node.Draining {
		return domain.Invalid("that node is draining and accepts no new servers")
	}
	if node.Transport != "local" && !s.remote(nodeID) {
		return domain.Invalid("node is unavailable")
	}
	return nil
}

// createWorkspace makes a new server's directory on the panel; servers on
// remote nodes get theirs on the node at first use.
func (s *BotService) createWorkspace(nodeID, id string) error {
	if s.remote(nodeID) {
		return nil
	}
	return s.Workspaces.Create(id)
}

func (s *BotService) removeWorkspace(nodeID, id string) {
	if !s.remote(nodeID) {
		_ = s.Workspaces.Remove(id)
	}
}

// FilesBlocked reports (as a BusyError) whether a deployment or restore is
// replacing the bot's files, so edits from the panel or SFTP must wait.
func (s *BotService) FilesBlocked(botID string) error { return s.Coord.Blocked(botID) }

// Killer force-stops a bot's containers without a graceful period.
type Killer interface {
	Kill(ctx context.Context, botID string) error
}

// CreateBotInput describes a new bot. Zero resource values use runtime defaults.
type CreateBotInput struct {
	Name        string
	Runtime     string
	Argv        []string
	MemoryBytes int64
	NanoCPUs    int64
	PidsLimit   int64
	NodeID      string
	WorkspaceID string  // "" = the creator's personal workspace
	SourceType  string  // manual (default) | template | github
	TemplateID  *string // set for SourceType template
	// Env are initial variables (e.g. the template's DISCORD_TOKEN), sealed
	// like any other; invalid names or values reject the whole request.
	Env map[string]string
	// BuildCommand optionally replaces the runtime's build step.
	BuildCommand string
	// Addons are attached at creation (databases, caches).
	Addons []AddonInput
}

// UpdateBotInput is a partial configuration change; nil fields are unchanged.
type UpdateBotInput struct {
	Name        *string
	Argv        *[]string
	MemoryBytes *int64
	NanoCPUs    *int64
	PidsLimit   *int64

	Runtime    *string
	Entrypoint *[]string // empty slice clears it

	NetworkEnabled *bool
	BandwidthKbps  *int64 // 0 clears
	AutoBackup     *bool

	RestartPolicy           *string
	RestartMaxAttempts      *int64
	RestartBackoffInitialMS *int64
	RestartBackoffMaxMS     *int64

	BuildCommand *string // "" clears it
}

// EnvView is the masked view of a variable.
type EnvView struct {
	Name        string
	UpdatedAtMS int64
}

func (s *BotService) now() int64 {
	if s.Now != nil {
		return s.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// Permission requirements for loadPerm besides the domain.Perm* bits.
const (
	permAny       = 0  // any access at all (owner, admin or any sub-user grant)
	permOwnerOnly = -1 // owner or administrator only
)

// Authorize returns the bot if the actor holds perm on it. Bots the actor has no
// access to at all are indistinguishable from nonexistent ones (ErrNotFound) so
// IDs cannot be probed; a sub-user lacking perm gets ErrForbidden.
func (s *BotService) Authorize(ctx context.Context, actor domain.User, id string, perm int) (domain.Bot, error) {
	return s.loadPerm(ctx, actor, id, perm, false)
}

func (s *BotService) load(ctx context.Context, actor domain.User, id string, allowDeleted bool) (domain.Bot, error) {
	return s.loadPerm(ctx, actor, id, permOwnerOnly, allowDeleted)
}

func (s *BotService) loadPerm(ctx context.Context, actor domain.User, id string, perm int, allowDeleted bool) (domain.Bot, error) {
	if u, err := uuid.Parse(id); err != nil || u.String() != id {
		return domain.Bot{}, domain.ErrNotFound
	}
	b, err := s.Store.GetBot(ctx, id)
	if err != nil {
		return domain.Bot{}, err
	}
	if b.DesiredState == domain.DesiredDeleted && !allowDeleted {
		return domain.Bot{}, domain.ErrNotFound
	}
	// An API client limited to some bots or workspaces reaches no other bot.
	if !actor.Client.AllowsBot(b.ID, b.WorkspaceID) {
		return domain.Bot{}, &domain.ScopeError{What: "bot"}
	}
	// The account's role limits every path to a bot, ownership included.
	if b.OwnerID == actor.ID && perm > 0 {
		if err := actor.BotBitDenied(perm); err != nil {
			return domain.Bot{}, err
		}
	}
	if b.OwnerID == actor.ID || actor.IsAdmin() {
		return b, nil
	}
	role, err := s.Store.BotWorkspaceRole(ctx, id, actor.ID)
	if err != nil {
		return domain.Bot{}, err
	}
	// Workspace owners and admins act as the bot's owner.
	if domain.WorkspaceRoleRank(role) >= domain.WorkspaceRoleRank(domain.WorkspaceAdmin) {
		if perm > 0 {
			if err := actor.BotBitDenied(perm); err != nil {
				return domain.Bot{}, err
			}
		}
		return b, nil
	}
	mask, err := s.Store.GetSubUserPermissions(ctx, id, actor.ID)
	if errors.Is(err, domain.ErrNotFound) && role != "" {
		mask, err = 0, nil
	}
	if err != nil {
		return domain.Bot{}, err // ErrNotFound: no grant and no membership
	}
	mask = actor.MaskBotPerms(mask | domain.WorkspaceRolePerms(role))
	if perm == permOwnerOnly || (perm != permAny && !domain.HasPerm(mask, perm)) {
		return domain.Bot{}, domain.ErrForbidden
	}
	return b, nil
}

// Permissions returns the actor's permission mask on a bot they can access.
// The account's role removes the bits it does not allow (see
// domain.User.MaskBotPerms), for owners too.
func (s *BotService) Permissions(ctx context.Context, actor domain.User, b domain.Bot) int {
	if b.OwnerID == actor.ID || actor.IsAdmin() {
		return actor.MaskBotPerms(domain.PermAll)
	}
	mask, _ := s.Store.GetSubUserPermissions(ctx, b.ID, actor.ID)
	role, _ := s.Store.BotWorkspaceRole(ctx, b.ID, actor.ID)
	mask |= domain.WorkspaceRolePerms(role)
	if mask&domain.PermFullAdmin != 0 {
		mask = domain.PermAll
	}
	return actor.MaskBotPerms(mask)
}

func (s *BotService) Create(ctx context.Context, actor domain.User, in CreateBotInput) (domain.Bot, error) {
	name, err := validateName(in.Name)
	if err != nil {
		return domain.Bot{}, err
	}
	if len(in.Env) > maxEnvVars {
		return domain.Bot{}, domain.Invalid(fmt.Sprintf("at most %d environment variables per bot", maxEnvVars))
	}
	for n, v := range in.Env {
		if err := validateEnvName(n, false); err != nil {
			return domain.Bot{}, err
		}
		if err := validateEnvValue(v); err != nil {
			return domain.Bot{}, err
		}
	}
	var seed []templates.File
	if in.TemplateID != nil {
		t, ok := templates.Get(*in.TemplateID)
		if !ok {
			return domain.Bot{}, domain.Invalid("unknown template")
		}
		var err error
		if seed, err = templates.Files(t.ID); err != nil {
			return domain.Bot{}, err
		}
		in.Runtime, in.SourceType = t.Runtime, "template"
		if in.Argv == nil && len(t.Argv) > 0 {
			in.Argv = append([]string(nil), t.Argv...)
		}
	}
	rt, ok := s.Catalog.Get(in.Runtime)
	if !ok {
		return domain.Bot{}, domain.Invalid("unknown runtime")
	}
	argv := in.Argv
	if argv == nil {
		argv = append([]string(nil), rt.DefaultArgv...)
	}
	if err := validateArgv(rt, argv); err != nil {
		return domain.Bot{}, err
	}
	mem, cpu, pids := in.MemoryBytes, in.NanoCPUs, in.PidsLimit
	if mem == 0 {
		mem = rt.Defaults.MemoryBytes
	}
	if cpu == 0 {
		cpu = rt.Defaults.NanoCPUs
	}
	if pids == 0 {
		pids = rt.Defaults.PidsLimit
	}
	if err := s.Limits.validateResources(rt, mem, cpu, pids); err != nil {
		return domain.Bot{}, err
	}
	build, err := validateBuildCommand(in.BuildCommand)
	if err != nil {
		return domain.Bot{}, err
	}
	addonMem, err := s.validateAddonInputs(in.Addons)
	if err != nil {
		return domain.Bot{}, err
	}
	if err := s.checkUserBudget(ctx, actor, 1, mem+addonMem); err != nil {
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
	// Add-ons must be able to run on the chosen node: checked for each one
	// before anything is recorded.
	if len(in.Addons) > 0 {
		if err := s.addonsAvailable(nodeID); err != nil {
			return domain.Bot{}, err
		}
		if s.remote(nodeID) {
			for _, a := range in.Addons {
				k, _ := addons.Get(strings.ToLower(strings.TrimSpace(a.Kind)))
				if err := addonSupportedOnNode(k); err != nil {
					return domain.Bot{}, err
				}
			}
		}
	}

	now := s.now()
	b := domain.Bot{
		ID: uuid.NewString(), OwnerID: actor.ID, WorkspaceID: wsID, NodeID: nodeID, Name: name, Runtime: rt.ID, ImageRef: rt.ImageRef(),
		Argv: argv, MemoryBytes: mem, NanoCPUs: cpu, PidsLimit: pids,
		DesiredState: domain.DesiredStopped, ObservedState: "stopped", CreatedAtMS: now, UpdatedAtMS: now,
		SourceType: in.SourceType, TemplateID: in.TemplateID, RestartPolicy: domain.RestartOnFailure, BuildCommand: build,
	}
	// A template on a remote node is seeded through the node's agent: refuse
	// before anything is recorded when that cannot work.
	var seedTo NodeFiles
	if len(seed) > 0 {
		if s.remote(nodeID) {
			if seedTo, _, err = s.RemoteFiles(b, "templates"); err != nil {
				return domain.Bot{}, err
			}
			if err := checkRemoteSeed(seed); err != nil {
				return domain.Bot{}, err
			}
		} else if s.Files == nil {
			return domain.Bot{}, domain.Invalid("templates are not available")
		}
	}
	// Workspace first: a leftover directory is harmless and reconcilable, whereas
	// a row without a workspace would be a broken bot.
	if err := s.createWorkspace(nodeID, b.ID); err != nil {
		return domain.Bot{}, fmt.Errorf("create workspace: %w", err)
	}
	if err := s.Store.CreateBot(ctx, b); err != nil {
		s.removeWorkspace(nodeID, b.ID)
		return domain.Bot{}, err
	}
	if len(seed) > 0 && seedTo == nil {
		if err := s.seed(b.ID, seed); err != nil {
			s.discardNew(ctx, b)
			return domain.Bot{}, fmt.Errorf("seed template: %w", err)
		}
	}
	if len(in.Env) > 0 {
		if err := s.putEnv(ctx, b, in.Env, false); err != nil {
			s.discardNew(ctx, b)
			return domain.Bot{}, err
		}
	}
	for _, a := range in.Addons {
		cur, err := s.Store.GetBot(ctx, b.ID)
		if err == nil {
			_, _, err = s.addAddon(ctx, cur, a, false, true) // budget checked above
		}
		if err != nil {
			s.discardNew(ctx, b)
			return domain.Bot{}, err
		}
	}
	// The remote template goes last, so the only step after its files are
	// swapped in on the node is the commit itself.
	if seedTo != nil {
		if err := s.seedRemote(ctx, seedTo, b, seed); err != nil {
			s.discardNew(ctx, b)
			return domain.Bot{}, fmt.Errorf("seed template on the node: %w", err)
		}
	}
	if len(in.Addons) > 0 || len(in.Env) > 0 {
		return s.Store.GetBot(ctx, b.ID)
	}
	return b, nil
}

// validateAddonInputs checks add-ons requested at creation and returns their
// total memory.
func (s *BotService) validateAddonInputs(in []AddonInput) (int64, error) {
	if len(in) == 0 {
		return 0, nil
	}
	if len(in) > maxAddonsPerBot {
		return 0, domain.Invalid(fmt.Sprintf("a bot can have at most %d add-ons", maxAddonsPerBot))
	}
	seen := map[string]bool{}
	var total int64
	for i, a := range in {
		k, ok := addons.Get(strings.ToLower(strings.TrimSpace(a.Kind)))
		if !ok {
			return 0, domain.Invalid(fmt.Sprintf("unknown add-on %q", a.Kind))
		}
		if seen[k.ID] {
			return 0, domain.Invalid(k.DisplayName + " is listed twice")
		}
		seen[k.ID] = true
		if a.MemoryBytes == 0 {
			in[i].MemoryBytes = k.DefaultMemory
		}
		if err := k.ValidateMemory(in[i].MemoryBytes); err != nil {
			return 0, domain.Invalid(err.Error())
		}
		total += in[i].MemoryBytes
	}
	return total, nil
}

func (s *BotService) Get(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	return s.loadPerm(ctx, actor, id, permAny, false)
}

// List returns the actor's bots (all bots for administrators).
func (s *BotService) List(ctx context.Context, actor domain.User) ([]domain.Bot, error) {
	var all []domain.Bot
	var err error
	if actor.IsAdmin() {
		all, err = s.Store.ListBots(ctx, "")
	} else {
		all, err = s.Store.ListBotsForUser(ctx, actor.ID)
	}
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, b := range all {
		if b.DesiredState != domain.DesiredDeleted && actor.Client.AllowsBot(b.ID, b.WorkspaceID) {
			out = append(out, b)
		}
	}
	return out, nil
}

// Update changes configuration of a stopped bot.
func (s *BotService) Update(ctx context.Context, actor domain.User, id string, in UpdateBotInput) (domain.Bot, error) {
	b, err := s.loadPerm(ctx, actor, id, domain.PermFullAdmin, false)
	if err != nil {
		return domain.Bot{}, err
	}
	old := b
	if b.IsGame() && (in.Runtime != nil || in.Argv != nil || in.Entrypoint != nil || in.BuildCommand != nil) {
		return domain.Bot{}, domain.Invalid("a game server's startup comes from its server type; change its settings under Startup")
	}
	rt, ok := s.Catalog.Get(b.Runtime)
	if !ok {
		return domain.Bot{}, fmt.Errorf("runtime %q missing from catalog", b.Runtime)
	}
	if in.Runtime != nil && *in.Runtime != b.Runtime {
		nrt, ok := s.Catalog.Get(*in.Runtime)
		if !ok {
			return domain.Bot{}, domain.Invalid("unknown runtime")
		}
		rt = nrt
		b.Runtime, b.ImageRef = nrt.ID, nrt.ImageRef()
		if in.Argv == nil { // the old command rarely fits another language
			b.Argv = append([]string(nil), nrt.DefaultArgv...)
		}
		if in.Entrypoint == nil {
			b.Entrypoint = nil
		}
	}
	if in.Name != nil {
		if b.Name, err = validateName(*in.Name); err != nil {
			return domain.Bot{}, err
		}
	}
	if in.Argv != nil {
		b.Argv = *in.Argv
	}
	if in.Entrypoint != nil {
		b.Entrypoint = append([]string(nil), *in.Entrypoint...)
	}
	if err := validateStartup(rt, b.Argv, b.Entrypoint); err != nil {
		return domain.Bot{}, err
	}
	if in.MemoryBytes != nil {
		b.MemoryBytes = *in.MemoryBytes
	}
	if in.NanoCPUs != nil {
		b.NanoCPUs = *in.NanoCPUs
	}
	if in.PidsLimit != nil {
		b.PidsLimit = *in.PidsLimit
	}
	if err := s.Limits.validateResources(rt, b.MemoryBytes, b.NanoCPUs, b.PidsLimit); err != nil {
		return domain.Bot{}, err
	}
	if in.MemoryBytes != nil {
		owner, err := s.Store.GetUserByID(ctx, b.OwnerID)
		if err != nil {
			return domain.Bot{}, err
		}
		if err := s.checkUserBudget(ctx, owner, 0, b.MemoryBytes-old.MemoryBytes); err != nil {
			return domain.Bot{}, err
		}
	}
	if in.NetworkEnabled != nil {
		if !*in.NetworkEnabled && len(b.Ports) > 0 {
			return domain.Bot{}, domain.Invalid("remove the published ports before disabling networking")
		}
		b.NetworkDisabled = !*in.NetworkEnabled
	}
	if in.BandwidthKbps != nil {
		if *in.BandwidthKbps == 0 {
			b.BandwidthKbps = nil
		} else if *in.BandwidthKbps < 8 || *in.BandwidthKbps > 10_000_000 {
			return domain.Bot{}, domain.Invalid("bandwidth must be between 8 and 10000000 kbit/s (0 for unlimited)")
		} else {
			v := *in.BandwidthKbps
			b.BandwidthKbps = &v
		}
	}
	if in.AutoBackup != nil {
		b.AutoBackupOff = !*in.AutoBackup
	}
	if err := applyRestart(&b, in); err != nil {
		return domain.Bot{}, err
	}
	if in.BuildCommand != nil {
		if b.BuildCommand, err = validateBuildCommand(*in.BuildCommand); err != nil {
			return domain.Bot{}, err
		}
	}
	if err := s.Store.UpdateBotConfig(ctx, b, s.now()); err != nil {
		return domain.Bot{}, err
	}
	return s.Store.GetBot(ctx, id)
}

func applyRestart(b *domain.Bot, in UpdateBotInput) error {
	if in.RestartPolicy != nil {
		switch *in.RestartPolicy {
		case domain.RestartNever, domain.RestartOnFailure:
			b.RestartPolicy = *in.RestartPolicy
		default:
			return domain.Invalid("restart policy must be never or on_failure")
		}
	}
	if in.RestartMaxAttempts != nil {
		if *in.RestartMaxAttempts < 0 || *in.RestartMaxAttempts > 100 {
			return domain.Invalid("max restart attempts must be between 0 (unlimited) and 100")
		}
		b.RestartMaxAttempts = *in.RestartMaxAttempts
	}
	if in.RestartBackoffInitialMS != nil {
		b.RestartBackoffInitialMS = *in.RestartBackoffInitialMS
	}
	if in.RestartBackoffMaxMS != nil {
		b.RestartBackoffMaxMS = *in.RestartBackoffMaxMS
	}
	if b.RestartBackoffInitialMS < 100 || b.RestartBackoffMaxMS > 3_600_000 || b.RestartBackoffMaxMS < b.RestartBackoffInitialMS {
		return domain.Invalid("restart backoff must satisfy 100 ms <= initial <= max <= 3600000 ms")
	}
	return nil
}

// Delete marks the bot deleted, then tears it down (containers, workspace, row).
// If cleanup fails the row stays marked deleted so a retry or the reconciler
// can finish the job.
func (s *BotService) Delete(ctx context.Context, actor domain.User, id string) error {
	b, err := s.load(ctx, actor, id, true)
	if err != nil {
		return err
	}
	if s.Purger == nil && b.ContainerID != nil {
		return domain.ErrRunnerUnavailable // a container exists but nothing can remove it
	}
	// Deletion wins over a running deployment/restore/backup: cancel it and
	// wait for it to let go of the workspace before tearing it down.
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = s.Coord.Preempt(pctx, id)
	cancel()
	if err != nil {
		return domain.Invalid("another operation is still finishing on this bot; try deleting again in a moment")
	}
	if s.BeforeDelete != nil {
		s.BeforeDelete(ctx, id)
	}
	if err := s.Store.MarkBotDeleted(ctx, id, s.now()); err != nil {
		return err
	}
	if s.Purger != nil {
		if err := s.Purger.Purge(ctx, id); err != nil {
			return err
		}
	} else {
		if err := s.Workspaces.Remove(id); err != nil {
			return fmt.Errorf("remove workspace: %w", err)
		}
		if err := s.Store.DeleteBotRow(ctx, id); err != nil {
			return err
		}
	}
	if s.OnDelete != nil {
		s.OnDelete(id)
	}
	return nil
}

// Start records intent to run the bot. Repeated requests for a bot that is
// already wanted running return the current generation unchanged.
func (s *BotService) Start(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	return s.setIntent(ctx, actor, id, domain.DesiredRunning, false)
}

// Stop records intent to stop the bot.
func (s *BotService) Stop(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	return s.setIntent(ctx, actor, id, domain.DesiredStopped, false)
}

// Restart replaces the container: it always advances the generation.
func (s *BotService) Restart(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	return s.setIntent(ctx, actor, id, domain.DesiredRunning, true)
}

func (s *BotService) setIntent(ctx context.Context, actor domain.User, id, desired string, force bool) (domain.Bot, error) {
	if s.Notifier == nil {
		return domain.Bot{}, domain.ErrRunnerUnavailable
	}
	b, err := s.loadPerm(ctx, actor, id, domain.PermPower, false)
	if err != nil {
		return domain.Bot{}, err
	}
	if desired == domain.DesiredRunning {
		if err := s.Coord.Blocked(id); err != nil {
			return domain.Bot{}, err
		}
		node, err := s.Store.GetNode(ctx, b.NodeID)
		if err != nil || !node.Enabled {
			return domain.Bot{}, domain.Invalid("node is unavailable")
		}
		if _, ok := s.Catalog.Get(b.Runtime); !ok {
			return domain.Bot{}, domain.Invalid("runtime is no longer available")
		}
		if err := s.checkRemotePorts(ctx, b); err != nil {
			return domain.Bot{}, err
		}
	}
	// A bot that is meant to run but has settled in a terminal state (a clean
	// exit, a crash with no restart left, or the restart policy said stop) needs
	// an explicit retry; "already wanted running" would otherwise ignore Start.
	// An unsettled generation (start still pending) stays idempotent.
	if desired == domain.DesiredRunning && !force && b.DesiredState == domain.DesiredRunning &&
		b.ObservedGeneration == b.Generation && (b.ObservedState == "stopped" || b.ObservedState == "failed") {
		force = true
	}
	nb, changed, err := s.Store.SetDesiredWithin(ctx, id, desired, force, s.now(), s.Limits.NodeMemoryBytes)
	if err != nil {
		return domain.Bot{}, err
	}
	if changed {
		s.Bus.Publish(events.Status{BotID: id, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
			Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
		s.Notifier.Notify(id)
	}
	return nb, nil
}

// ListEnv returns masked variables (names only).
func (s *BotService) ListEnv(ctx context.Context, actor domain.User, id string) ([]EnvView, error) {
	if _, err := s.loadPerm(ctx, actor, id, domain.PermManageEnv, false); err != nil {
		return nil, err
	}
	rows, err := s.Store.ListEnv(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]EnvView, 0, len(rows))
	for _, r := range rows {
		if strings.HasPrefix(r.Name, "RIVET_ADDON_") {
			continue // add-on passwords are shown on the Add-ons tab
		}
		out = append(out, EnvView{r.Name, r.UpdatedAtMS})
	}
	return out, nil
}

// SetEnv encrypts and upserts variables on a stopped bot.
func (s *BotService) SetEnv(ctx context.Context, actor domain.User, id string, vars map[string]string) error {
	b, err := s.loadPerm(ctx, actor, id, domain.PermManageEnv, false)
	if err != nil {
		return err
	}
	return s.putEnv(ctx, b, vars, false)
}

// putEnv validates, seals and stores variables. System callers may set the
// reserved RIVET_* names that users cannot.
func (s *BotService) putEnv(ctx context.Context, b domain.Bot, vars map[string]string, system bool) error {
	if len(vars) == 0 {
		return domain.Invalid("no variables provided")
	}
	if err := s.Coord.Blocked(b.ID); err != nil {
		return err
	}
	names := make([]string, 0, len(vars))
	for n, v := range vars {
		if err := validateEnvName(n, system); err != nil {
			return err
		}
		if err := validateEnvValue(v); err != nil {
			return err
		}
		names = append(names, n)
	}
	sort.Strings(names)
	existing, err := s.Store.ListEnv(ctx, b.ID)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range existing {
		have[e.Name] = true
	}
	total := len(existing)
	for _, n := range names {
		if !have[n] {
			total++
		}
	}
	if total > maxEnvVars {
		return domain.Invalid(fmt.Sprintf("at most %d environment variables per bot", maxEnvVars))
	}
	rows := make([]domain.EnvVar, 0, len(names))
	for _, n := range names {
		sealed, err := s.Keys.Seal(b.ID, n, []byte(vars[n]))
		if err != nil {
			return err
		}
		rows = append(rows, domain.EnvVar{BotID: b.ID, Name: n, Ciphertext: sealed.Ciphertext, Nonce: sealed.Nonce, KeyID: sealed.KeyID})
	}
	return s.Store.UpsertEnv(ctx, b.ID, rows, s.now())
}

// DeleteEnv removes one variable from a stopped bot.
func (s *BotService) DeleteEnv(ctx context.Context, actor domain.User, id, name string) error {
	if _, err := s.loadPerm(ctx, actor, id, domain.PermManageEnv, false); err != nil {
		return err
	}
	if strings.HasPrefix(name, "RIVET_ADDON_") {
		return domain.Invalid("this password belongs to an add-on; remove the add-on instead")
	}
	if strings.HasPrefix(name, "RIVET_AGREEMENT_") {
		return domain.Invalid("this records an accepted agreement and cannot be removed")
	}
	if err := s.Coord.Blocked(id); err != nil {
		return err
	}
	return s.Store.DeleteEnv(ctx, id, name, s.now())
}

// MaxCommandBytes bounds one console command.
const MaxCommandBytes = 1000

// SendCommand writes one line to a running server's or bot's console input.
// It needs the power permission, like typing in the console.
func (s *BotService) SendCommand(ctx context.Context, actor domain.User, id, line string) error {
	b, err := s.loadPerm(ctx, actor, id, domain.PermPower, false)
	if err != nil {
		return err
	}
	return s.sendCommand(ctx, b, line)
}

func (s *BotService) sendCommand(ctx context.Context, b domain.Bot, line string) error {
	line = strings.TrimSpace(line)
	if line == "" || len(line) > MaxCommandBytes || strings.ContainsAny(line, "\n\r\x00") || !utf8.ValidString(line) {
		return domain.Invalid(fmt.Sprintf("a command is one line of 1-%d bytes", MaxCommandBytes))
	}
	var src interface {
		AttachStdin(ctx context.Context, containerID string) (io.WriteCloser, error)
	} = s.Stdin
	if s.StdinFor != nil {
		src = s.StdinFor(b.NodeID)
	}
	if src == nil {
		return domain.ErrRunnerUnavailable
	}
	if b.ContainerID == nil || b.DesiredState != domain.DesiredRunning || b.ObservedState != "running" {
		return domain.Invalid("it is not running")
	}
	w, err := src.AttachStdin(ctx, *b.ContainerID)
	if err != nil {
		return fmt.Errorf("attach console: %w", err)
	}
	defer w.Close() // detaches only; the process keeps its input
	_, err = io.WriteString(w, line+"\n")
	return err
}

// DecryptEnv returns plaintext variables for container construction. It is
// not exposed over the API; only the runner calls it.
func (s *BotService) DecryptEnv(ctx context.Context, botID string) (map[string]string, error) {
	rows, err := s.Store.ListEnv(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		pt, err := s.Keys.Open(botID, r.Name, secrets.Sealed{Ciphertext: r.Ciphertext, Nonce: r.Nonce, KeyID: r.KeyID})
		if err != nil {
			return nil, fmt.Errorf("variable %s: %w", r.Name, err)
		}
		out[r.Name] = string(pt)
	}
	return out, nil
}

// Bounds for seeding a template onto a remote node in one transactional
// patch (the agent's ApplyPatch accepts at most 32 files; its own ceilings
// are agentproto.MaxPatchFile and MaxPatchTotal, which these stay below).
const (
	maxRemoteSeedFiles = 32
	maxRemoteSeedFile  = 1 << 20
	maxRemoteSeedTotal = 8 << 20
)

// checkRemoteSeed refuses a template that cannot be seeded in one patch.
func checkRemoteSeed(files []templates.File) error {
	if len(files) > maxRemoteSeedFiles {
		return domain.Invalid(fmt.Sprintf("this template has more than %d files and cannot be created on a remote node yet", maxRemoteSeedFiles))
	}
	var total int64
	for _, f := range files {
		if len(f.Data) > maxRemoteSeedFile {
			return domain.Invalid("a file of this template is too large to create on a remote node")
		}
		total += int64(len(f.Data))
	}
	if total > maxRemoteSeedTotal {
		return domain.Invalid("this template is too large to create on a remote node")
	}
	return nil
}

// seedRemote writes a template into a new server's workspace on its node as
// one create-only transaction: the node makes the directory, stages and
// swaps every file through its journal (a file that already exists is a
// conflict), and keeps the change reversible until it is committed here. A
// lost commit answer is retried once (completion is idempotent on the node);
// if it still cannot be confirmed the change is rolled back explicitly (an
// unconfirmed patch also expires to rollback on the node).
func (s *BotService) seedRemote(ctx context.Context, nf NodeFiles, b domain.Bot, files []templates.File) error {
	if err := nf.CreateWorkspace(ctx, b.NodeID, b.ID); err != nil {
		return err
	}
	patch := make([]filesystem.PatchFile, len(files))
	for i, f := range files {
		data := f.Data
		if data == nil {
			data = []byte{} // nil would delete the path
		}
		patch[i] = filesystem.PatchFile{Path: f.Path, After: data}
	}
	tx, _, err := nf.BeginPatch(ctx, b.NodeID, b.ID, patch, maxRemoteSeedFile, maxRemoteSeedTotal)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err = nf.CompleteTransaction(cctx, b.NodeID, b.ID, tx, true); err == nil {
		return nil
	}
	if err = nf.CompleteTransaction(cctx, b.NodeID, b.ID, tx, true); err == nil {
		return nil
	}
	_ = nf.CompleteTransaction(cctx, b.NodeID, b.ID, tx, false)
	return fmt.Errorf("the node did not confirm the template files: %w", err)
}

// discardNew removes a server whose creation failed partway, so nothing
// half-created is left looking usable. A local server's workspace and row
// go at once. A remote server goes through the normal delete lifecycle: it
// is marked deleted (hidden everywhere) and its agent purges it, removing
// its directory on the node and then the row; if the agent cannot do that
// now, it finishes when it reconnects. Nothing on the panel's disk is
// touched for a remote server.
func (s *BotService) discardNew(ctx context.Context, b domain.Bot) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = s.Store.MarkBotDeleted(ctx, b.ID, s.now())
	if s.remote(b.NodeID) && s.Purger != nil {
		_ = s.Purger.Purge(ctx, b.ID)
		return
	}
	s.removeWorkspace(b.NodeID, b.ID)
	_ = s.Store.DeleteBotRow(ctx, b.ID)
}

// seed writes template files into a new bot's workspace.
func (s *BotService) seed(botID string, files []templates.File) error {
	w, err := s.Files.Open(botID)
	if err != nil {
		return err
	}
	defer w.Close()
	for _, f := range files {
		if err := w.Write(f.Path, bytes.NewReader(f.Data), 1<<20); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return nil
}

// RevealEnv returns one variable's plaintext to a user with the env permission.
// Values are masked everywhere else; this is an explicit, single-value request.
func (s *BotService) RevealEnv(ctx context.Context, actor domain.User, id, name string) (string, error) {
	b, err := s.loadPerm(ctx, actor, id, domain.PermManageEnv, false)
	if err != nil {
		return "", err
	}
	rows, err := s.Store.ListEnv(ctx, b.ID)
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.Name == name {
			pt, err := s.Keys.Open(b.ID, r.Name, secrets.Sealed{Ciphertext: r.Ciphertext, Nonce: r.Nonce, KeyID: r.KeyID})
			return string(pt), err
		}
	}
	return "", domain.ErrNotFound
}

// StopOwnedBy records stopped intent for every bot a user owns (offboarding:
// disabling an account does not by itself stop its bots). Needs users.manage.
func (s *BotService) StopOwnedBy(ctx context.Context, actor domain.User, ownerID string) (int, error) {
	if !actor.Can(domain.PermUsersManage) {
		return 0, domain.ErrForbidden
	}
	if s.Notifier == nil {
		return 0, nil
	}
	all, err := s.Store.ListBots(ctx, ownerID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range all {
		if b.DesiredState != domain.DesiredRunning {
			continue
		}
		nb, changed, err := s.Store.SetDesired(ctx, b.ID, domain.DesiredStopped, false, s.now())
		if err != nil {
			return n, err
		}
		if changed {
			n++
			s.Bus.Publish(events.Status{BotID: b.ID, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
				Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
			s.Notifier.Notify(b.ID)
		}
	}
	return n, nil
}

// checkUserBudget refuses adding bots or memory beyond a user's budget.
// Administrators are exempt: they operate the host.
func (s *BotService) checkUserBudget(ctx context.Context, owner domain.User, addBots int, addMemory int64) error {
	if owner.IsAdmin() || (s.Limits.MaxBotsPerUser == 0 && s.Limits.UserMemoryBytes == 0) {
		return nil
	}
	n, mem, err := s.Store.OwnerUsage(ctx, owner.ID)
	if err != nil {
		return err
	}
	if s.Limits.MaxBotsPerUser > 0 && addBots > 0 && n+addBots > s.Limits.MaxBotsPerUser {
		return domain.Invalid(fmt.Sprintf("your account can have at most %d bots; delete one first", s.Limits.MaxBotsPerUser))
	}
	if s.Limits.UserMemoryBytes > 0 && addMemory > 0 && mem+addMemory > s.Limits.UserMemoryBytes {
		return domain.Invalid(fmt.Sprintf("your bots may use at most %d MiB of memory in total; %d MiB is already assigned",
			s.Limits.UserMemoryBytes>>20, mem>>20))
	}
	return nil
}

// Capacity is a user's usage against the configured budgets.
type Capacity struct {
	Bots, MaxBots            int
	Memory, MaxMemory        int64
	NodeRunning              int   // admins only
	NodeReserved, NodeBudget int64 // admins only
	Exempt                   bool  // administrators are not limited per user
}

// Capacity reports the actor's usage and, for administrators, the node's.
func (s *BotService) Capacity(ctx context.Context, actor domain.User) (Capacity, error) {
	n, mem, err := s.Store.OwnerUsage(ctx, actor.ID)
	if err != nil {
		return Capacity{}, err
	}
	c := Capacity{Bots: n, MaxBots: s.Limits.MaxBotsPerUser, Memory: mem, MaxMemory: s.Limits.UserMemoryBytes, Exempt: actor.IsAdmin()}
	if actor.IsAdmin() {
		c.NodeRunning, c.NodeReserved, err = s.Store.NodeReserved(ctx, s.LocalNode)
		c.NodeBudget = s.Limits.NodeMemoryBytes
	}
	return c, err
}
