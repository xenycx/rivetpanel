package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Workspace limits. Every workspace gets the panel's default limits (Panel
// settings → Default limits, else RIVET_MAX_BOTS_PER_USER,
// RIVET_MAX_SITES_PER_USER and RIVET_USER_MEMORY_BYTES; unlimited where
// neither sets one). An administrator can give one workspace its own limits
// (workspace_quotas) that replace a default field by field. Every member's
// apps and sites in a workspace count together. Workspaces owned by
// administrators are exempt.
//
// Apps count until they are deleted, running or stopped: a stopped app keeps
// its place, so a workspace cannot hold many stopped apps and then start
// them all past its limit, and starting an app never needs a check. Only
// growth is refused; a limit lowered below what a workspace already holds
// leaves its apps and sites alone.

// Default limit settings (Panel settings → Default limits). An absent or
// empty value falls back to the environment, then to no limit; a number is
// the limit and 0 allows none.
const (
	SetLimitApps           = "limit_max_apps"
	SetLimitBots           = "limit_max_bots"
	SetLimitGames          = "limit_max_games"
	SetLimitWeb            = "limit_max_web_apps"
	SetLimitSites          = "limit_max_sites"
	SetLimitMemory         = "limit_memory_bytes"
	SetLimitCPU            = "limit_nano_cpus"
	SetLimitTeamWorkspaces = "limit_team_workspaces"
)

// LimitSettingKeys lists the default limit settings, in display order.
var LimitSettingKeys = []string{SetLimitApps, SetLimitBots, SetLimitGames, SetLimitWeb, SetLimitSites, SetLimitMemory, SetLimitCPU, SetLimitTeamWorkspaces}

// workspaceLimit is one resolved limit. Max < 0 is unlimited.
type workspaceLimit struct {
	Max   int64
	Quota bool // set by the workspace's own limits, not the panel default
}

func limitOf(q *int64, def int64) workspaceLimit {
	switch {
	case q != nil:
		return workspaceLimit{Max: *q, Quota: true}
	case def >= 0:
		return workspaceLimit{Max: def}
	}
	return workspaceLimit{Max: -1}
}

// over reports whether growing by added to after breaks the limit.
func (l workspaceLimit) over(after, added int64) bool {
	return l.Max >= 0 && added > 0 && after > l.Max
}

// WorkspaceLimits are the limits that apply to one workspace.
type WorkspaceLimits struct {
	Name                                       string // the workspace's, for messages
	Apps, Bots, Games, Web, Sites, Memory, CPU workspaceLimit
	Exempt                                     bool // owned by an administrator
}

func (l WorkspaceLimits) unlimited() bool {
	if l.Exempt {
		return true
	}
	for _, x := range []workspaceLimit{l.Apps, l.Bots, l.Games, l.Web, l.Sites, l.Memory, l.CPU} {
		if x.Max >= 0 {
			return false
		}
	}
	return true
}

// LimitDefaults are the panel's default limits for every workspace (-1 =
// none) and where each comes from: "panel" (Panel settings), "env" (the
// environment file) or "" (no default).
type LimitDefaults struct {
	Apps, Bots, Games, Web, Sites, Memory, CPU int64
	TeamWorkspaces                             int64 // team workspaces one account may own
	Source                                     map[string]string
}

// LimitDefaults reads the default limits: Panel settings first, then the
// environment. Changes apply to the next check; nothing is cached.
func (s *BotService) LimitDefaults(ctx context.Context) (LimitDefaults, error) {
	stored, err := s.Store.Settings(ctx)
	if err != nil {
		return LimitDefaults{}, err
	}
	d := LimitDefaults{Source: map[string]string{}}
	pick := func(dst *int64, key string, env int64) {
		if st, ok := stored[key]; ok && st.Value != "" {
			if v, err := strconv.ParseInt(st.Value, 10, 64); err == nil && v >= 0 {
				*dst, d.Source[key] = v, "panel"
				return
			}
		}
		if env > 0 {
			*dst, d.Source[key] = env, "env"
			return
		}
		*dst = -1
	}
	pick(&d.Apps, SetLimitApps, int64(s.Limits.MaxBotsPerUser))
	pick(&d.Bots, SetLimitBots, 0)
	pick(&d.Games, SetLimitGames, 0)
	pick(&d.Web, SetLimitWeb, 0)
	pick(&d.Sites, SetLimitSites, int64(s.Limits.MaxSitesPerUser))
	pick(&d.Memory, SetLimitMemory, s.Limits.UserMemoryBytes)
	pick(&d.CPU, SetLimitCPU, 0)
	if pick(&d.TeamWorkspaces, SetLimitTeamWorkspaces, 0); d.TeamWorkspaces < 0 {
		d.TeamWorkspaces = MaxOwnedWorkspaces
	}
	return d, nil
}

// workspaceLimits resolves a workspace's limits: its own where they set a
// field, else the panel default.
func (s *BotService) workspaceLimits(ctx context.Context, w domain.Workspace) (WorkspaceLimits, error) {
	owner, err := s.Store.GetUserByID(ctx, w.OwnerID)
	if err != nil {
		return WorkspaceLimits{}, err
	}
	none := workspaceLimit{Max: -1}
	if owner.IsAdmin() {
		return WorkspaceLimits{Name: w.Name, Exempt: true, Apps: none, Bots: none, Games: none, Web: none, Sites: none, Memory: none, CPU: none}, nil
	}
	q, err := s.Store.GetWorkspaceQuota(ctx, w.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return WorkspaceLimits{}, err
	}
	d, err := s.LimitDefaults(ctx)
	if err != nil {
		return WorkspaceLimits{}, err
	}
	return WorkspaceLimits{
		Name:   w.Name,
		Apps:   limitOf(q.MaxApps, d.Apps),
		Bots:   limitOf(q.MaxBots, d.Bots),
		Games:  limitOf(q.MaxGames, d.Games),
		Web:    limitOf(q.MaxWebApps, d.Web),
		Sites:  limitOf(q.MaxSites, d.Sites),
		Memory: limitOf(q.MemoryBytes, d.Memory),
		CPU:    limitOf(q.NanoCPUs, d.CPU),
	}, nil
}

func cores(n int64) string { return strconv.FormatFloat(float64(n)/1e9, 'f', -1, 64) }

// allows refuses growing a workspace by added when its usage after the
// change, after, goes over a limit. Messages name the workspace, since one
// account may create in several.
func (l WorkspaceLimits) allows(after, added domain.WorkspaceUsage) error {
	if l.Exempt {
		return nil
	}
	ws := fmt.Sprintf("the workspace %q", l.Name)
	for _, c := range []struct {
		lim          workspaceLimit
		after, added int64
		what         string
	}{
		{l.Apps, after.Apps, added.Apps, "apps (bots, game servers and web apps together)"},
		{l.Bots, after.Bots, added.Bots, "bots"},
		{l.Games, after.Games, added.Games, "game servers"},
		{l.Web, after.Web, added.Web, "web apps"},
		{l.Sites, after.Sites, added.Sites, "sites"},
	} {
		if !c.lim.over(c.after, c.added) {
			continue
		}
		if c.lim.Max == 0 {
			return domain.Invalid(fmt.Sprintf("%s does not allow %s; ask an administrator", ws, c.what))
		}
		return domain.Invalid(fmt.Sprintf("%s allows at most %d %s and %d already exist; delete one first or ask an administrator for a higher limit",
			ws, c.lim.Max, c.what, c.after-c.added))
	}
	if l.Memory.over(after.Memory, added.Memory) {
		return domain.Invalid(fmt.Sprintf("%s may use at most %d MiB of memory; %d MiB is already assigned and this change needs %d MiB more",
			ws, l.Memory.Max>>20, (after.Memory-added.Memory)>>20, added.Memory>>20))
	}
	if l.CPU.over(after.CPU, added.CPU) {
		return domain.Invalid(fmt.Sprintf("%s may use at most %s CPU cores; %s are already assigned and this change needs %s more",
			ws, cores(l.CPU.Max), cores(after.CPU-added.CPU), cores(added.CPU)))
	}
	return nil
}

func plusUsage(a, b domain.WorkspaceUsage) domain.WorkspaceUsage {
	return domain.WorkspaceUsage{Apps: a.Apps + b.Apps, Bots: a.Bots + b.Bots, Games: a.Games + b.Games, Web: a.Web + b.Web,
		Sites: a.Sites + b.Sites, Memory: a.Memory + b.Memory, CPU: a.CPU + b.CPU}
}

// newApp is the usage one new app of kind with mem of memory and cpu adds.
func newApp(kind string, mem, cpu int64) domain.WorkspaceUsage {
	u := domain.WorkspaceUsage{Apps: 1, Memory: mem, CPU: cpu}
	switch kind {
	case domain.KindGame:
		u.Games = 1
	case domain.KindWeb:
		u.Web = 1
	default:
		u.Bots = 1
	}
	return u
}

// appUsage is what an existing app adds to the workspace that holds it.
func appUsage(b domain.Bot) domain.WorkspaceUsage {
	mem := b.MemoryBytes
	for _, a := range b.Addons {
		mem += a.MemoryBytes
	}
	return newApp(b.Kind, mem, b.NanoCPUs)
}

// checkWorkspace refuses growing a workspace by added beyond its limits.
// Creations repeat the check inside the transaction that inserts the row
// (createBot, CreateSiteChecked); this early check refuses before any other
// work is done.
func (s *BotService) checkWorkspace(ctx context.Context, workspaceID string, added domain.WorkspaceUsage) error {
	w, err := s.Store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	lim, err := s.workspaceLimits(ctx, w)
	if err != nil || lim.unlimited() {
		return err
	}
	u, err := s.Store.WorkspaceUsage(ctx, workspaceID)
	if err != nil {
		return err
	}
	return lim.allows(plusUsage(u, added), added)
}

// createBot inserts b and checks its workspace's limits in the same
// transaction, so concurrent creations cannot both take the last of a
// limit. extraMemory is memory added with the bot but not yet stored (its
// add-ons).
func (s *BotService) createBot(ctx context.Context, b domain.Bot, extraMemory int64) error {
	w, err := s.Store.GetWorkspace(ctx, b.WorkspaceID)
	if err != nil {
		return err
	}
	lim, err := s.workspaceLimits(ctx, w)
	if err != nil {
		return err
	}
	if lim.unlimited() {
		return s.Store.CreateBot(ctx, b)
	}
	added := newApp(b.Kind, b.MemoryBytes+extraMemory, b.NanoCPUs)
	return s.Store.CreateBotChecked(ctx, b, func(u domain.WorkspaceUsage) error {
		u.Memory += extraMemory
		return lim.allows(u, added)
	})
}

// siteCheck returns the in-transaction check for one new site in a
// workspace (nil when it has no limits), after checking once up front.
func (s *BotService) siteCheck(ctx context.Context, workspaceID string) (func(domain.WorkspaceUsage) error, error) {
	w, err := s.Store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	lim, err := s.workspaceLimits(ctx, w)
	if err != nil || lim.unlimited() {
		return nil, err
	}
	oneSite := domain.WorkspaceUsage{Sites: 1}
	if err := s.checkWorkspace(ctx, workspaceID, oneSite); err != nil {
		return nil, err
	}
	return func(u domain.WorkspaceUsage) error { return lim.allows(u, oneSite) }, nil
}

// QuotaInput sets a workspace's limits. A nil field uses the panel default.
type QuotaInput struct {
	MaxApps, MaxBots, MaxGames, MaxWebApps, MaxSites *int64
	MemoryBytes, NanoCPUs                            *int64
}

// QuotaView is a workspace's own limits, what it holds and the limits that
// apply (its own or the panel default; Max < 0 = unlimited).
type QuotaView struct {
	Workspace domain.Workspace
	Quota     domain.WorkspaceQuota
	Usage     domain.WorkspaceUsage
	Limits    WorkspaceLimits
	Defaults  LimitDefaults
}

func (s *BotService) quotaView(ctx context.Context, w domain.Workspace) (QuotaView, error) {
	v := QuotaView{Workspace: w}
	var err error
	if v.Quota, err = s.Store.GetWorkspaceQuota(ctx, w.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return QuotaView{}, err
	}
	v.Quota.WorkspaceID = w.ID
	if v.Usage, err = s.Store.WorkspaceUsage(ctx, w.ID); err != nil {
		return QuotaView{}, err
	}
	if v.Limits, err = s.workspaceLimits(ctx, w); err != nil {
		return QuotaView{}, err
	}
	v.Defaults, err = s.LimitDefaults(ctx)
	return v, err
}

// WorkspaceQuota returns a workspace's limits and usage to administrators
// (workspaces.view).
func (s *BotService) WorkspaceQuota(ctx context.Context, actor domain.User, workspaceID string) (QuotaView, error) {
	if !viewsWorkspaces(actor) {
		return QuotaView{}, domain.ErrForbidden
	}
	w, err := s.Store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return QuotaView{}, err
	}
	return s.quotaView(ctx, w)
}

// WorkspaceLimitsFor returns a workspace's limits and usage to any of its
// members (read-only).
func (s *BotService) WorkspaceLimitsFor(ctx context.Context, actor domain.User, workspaceID string) (QuotaView, error) {
	if _, err := s.workspaceRole(ctx, actor, workspaceID); err != nil {
		return QuotaView{}, err
	}
	w, err := s.Store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return QuotaView{}, err
	}
	return s.quotaView(ctx, w)
}

// ListWorkspaceQuotas returns every workspace's own limits (workspaces.view).
func (s *BotService) ListWorkspaceQuotas(ctx context.Context, actor domain.User) (map[string]domain.WorkspaceQuota, error) {
	if !viewsWorkspaces(actor) {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListWorkspaceQuotas(ctx)
}

// maxQuotaCount bounds quota counts to something a panel can hold.
const maxQuotaCount = 100000

func validateQuota(in QuotaInput) error {
	for _, c := range []struct {
		v    *int64
		what string
	}{{in.MaxApps, "apps"}, {in.MaxBots, "bots"}, {in.MaxGames, "game servers"}, {in.MaxWebApps, "web apps"}, {in.MaxSites, "sites"}} {
		if c.v != nil && (*c.v < 0 || *c.v > maxQuotaCount) {
			return domain.Invalid(fmt.Sprintf("the %s limit must be between 0 and %d", c.what, maxQuotaCount))
		}
	}
	if in.MemoryBytes != nil && (*in.MemoryBytes < 0 || *in.MemoryBytes > 1<<50) {
		return domain.Invalid("the memory limit must be between 0 and 1 PiB")
	}
	if in.NanoCPUs != nil && (*in.NanoCPUs < 0 || *in.NanoCPUs > 100000*1e9) {
		return domain.Invalid("the CPU limit must be between 0 and 100000 cores")
	}
	return nil
}

// SetWorkspaceQuota replaces a workspace's limits; limits that set nothing
// are removed. Only administrators may change them: a delegated manager
// could otherwise raise their own. Lowering a limit below what the
// workspace holds stops nothing; it only refuses further growth.
func (s *BotService) SetWorkspaceQuota(ctx context.Context, actor domain.User, workspaceID string, in QuotaInput) (domain.WorkspaceQuota, error) {
	if !actor.IsAdmin() {
		return domain.WorkspaceQuota{}, domain.ErrForbidden
	}
	if _, err := s.Store.GetWorkspace(ctx, workspaceID); err != nil {
		return domain.WorkspaceQuota{}, err
	}
	if err := validateQuota(in); err != nil {
		return domain.WorkspaceQuota{}, err
	}
	now := s.now()
	q := domain.WorkspaceQuota{WorkspaceID: workspaceID, MaxApps: in.MaxApps, MaxBots: in.MaxBots, MaxGames: in.MaxGames,
		MaxWebApps: in.MaxWebApps, MaxSites: in.MaxSites, MemoryBytes: in.MemoryBytes, NanoCPUs: in.NanoCPUs,
		UpdatedBy: &actor.ID, CreatedAtMS: now, UpdatedAtMS: now}
	if q.Empty() {
		return domain.WorkspaceQuota{WorkspaceID: workspaceID}, s.Store.DeleteWorkspaceQuota(ctx, workspaceID)
	}
	if old, err := s.Store.GetWorkspaceQuota(ctx, workspaceID); err == nil {
		q.CreatedAtMS = old.CreatedAtMS
	}
	return q, s.Store.SetWorkspaceQuota(ctx, q)
}

// ClearWorkspaceQuota removes a workspace's limits (the panel defaults apply).
func (s *BotService) ClearWorkspaceQuota(ctx context.Context, actor domain.User, workspaceID string) error {
	if !actor.IsAdmin() {
		return domain.ErrForbidden
	}
	if _, err := s.Store.GetWorkspace(ctx, workspaceID); err != nil {
		return err
	}
	return s.Store.DeleteWorkspaceQuota(ctx, workspaceID)
}

// QuotaSummary describes limits for the audit log (unset fields omitted).
func QuotaSummary(q domain.WorkspaceQuota) string {
	if q.Empty() {
		return "panel defaults"
	}
	var parts []string
	for _, f := range []struct {
		name string
		v    *int64
	}{{"apps", q.MaxApps}, {"bots", q.MaxBots}, {"games", q.MaxGames}, {"web_apps", q.MaxWebApps}, {"sites", q.MaxSites},
		{"memory_mib", q.MemoryBytes}, {"cpus", q.NanoCPUs}} {
		if f.v == nil {
			continue
		}
		v := strconv.FormatInt(*f.v, 10)
		switch f.name {
		case "memory_mib":
			v = strconv.FormatInt(*f.v>>20, 10)
		case "cpus":
			v = cores(*f.v)
		}
		parts = append(parts, f.name+"="+v)
	}
	return strings.Join(parts, " ")
}

// LimitsInput replaces the default limits; a nil field removes the panel
// setting (the environment, else no limit, applies again).
type LimitsInput struct {
	MaxApps, MaxBots, MaxGames, MaxWebApps, MaxSites *int64
	MemoryBytes, NanoCPUs, TeamWorkspaces            *int64
}

// SetLimitDefaults stores the default limits (built-in administrators). They
// apply to the next check without a restart.
func (s *BotService) SetLimitDefaults(ctx context.Context, actor domain.User, in LimitsInput) error {
	if !actor.IsAdmin() {
		return domain.ErrForbidden
	}
	if err := validateQuota(QuotaInput{in.MaxApps, in.MaxBots, in.MaxGames, in.MaxWebApps, in.MaxSites, in.MemoryBytes, in.NanoCPUs}); err != nil {
		return err
	}
	if v := in.TeamWorkspaces; v != nil && (*v < 0 || *v > 1000) {
		return domain.Invalid("the team workspace limit must be between 0 and 1000")
	}
	var set []domain.Setting
	for _, f := range []struct {
		key string
		v   *int64
	}{{SetLimitApps, in.MaxApps}, {SetLimitBots, in.MaxBots}, {SetLimitGames, in.MaxGames}, {SetLimitWeb, in.MaxWebApps},
		{SetLimitSites, in.MaxSites}, {SetLimitMemory, in.MemoryBytes}, {SetLimitCPU, in.NanoCPUs}, {SetLimitTeamWorkspaces, in.TeamWorkspaces}} {
		st := domain.Setting{Key: f.key} // empty: removed
		if f.v != nil {
			st.Value = strconv.FormatInt(*f.v, 10)
		}
		set = append(set, st)
	}
	return s.Store.PutSettings(ctx, set, s.now())
}

// checkMove refuses moving app b into a workspace with no room for it.
func (s *BotService) checkMove(ctx context.Context, b domain.Bot, workspaceID string) error {
	if b.WorkspaceID == workspaceID {
		return nil
	}
	return s.checkWorkspace(ctx, workspaceID, appUsage(b))
}

// checkTransfer refuses handing b to an account whose personal workspace,
// where a transferred app goes, has no room for it.
func (s *BotService) checkTransfer(ctx context.Context, target domain.User, b domain.Bot) error {
	w, err := s.Store.PersonalWorkspace(ctx, target.ID)
	if err != nil {
		return err
	}
	if err := s.checkMove(ctx, b, w.ID); err != nil {
		var v *domain.ValidationError
		if errors.As(err, &v) {
			return domain.Invalid("that account's personal workspace has no room for this app: " + v.Error())
		}
		return err
	}
	return nil
}
