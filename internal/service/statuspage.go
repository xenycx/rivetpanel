package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Status page settings and limits.
const (
	SetStatusEnabled = "status_enabled"
	SetStatusTitle   = "status_title"
	SetStatusIntro   = "status_intro"

	// StatusRetentionDays is how many UTC days of samples are kept (and
	// shown as bars).
	StatusRetentionDays = 90
	// StatusSampleInterval is how often component states are sampled.
	StatusSampleInterval = 5 * time.Minute
	// statusRecentDays is how long resolved incidents stay on the page.
	statusRecentDays = 14

	statusMaxComponents = 50
	statusUpdateMax     = 5000
)

// StatusStore persists the status page.
type StatusStore interface {
	ListStatusComponents(ctx context.Context) ([]domain.StatusComponent, error)
	GetStatusComponent(ctx context.Context, id string) (domain.StatusComponent, error)
	PutStatusComponent(ctx context.Context, c domain.StatusComponent) error
	DeleteStatusComponent(ctx context.Context, id string) error
	ListStatusIncidents(ctx context.Context, closedSince int64, limit int) ([]domain.StatusIncident, error)
	GetStatusIncident(ctx context.Context, id string) (domain.StatusIncident, error)
	SaveStatusIncident(ctx context.Context, i domain.StatusIncident, upd *domain.StatusIncidentUpdate) error
	DeleteStatusIncident(ctx context.Context, id string) error
	AddStatusSamples(ctx context.Context, day int64, states map[string]string) error
	StatusDays(ctx context.Context, sinceDay int64) ([]domain.StatusDay, error)
	PruneStatusSamples(ctx context.Context, beforeDay int64) (int64, error)
	Settings(ctx context.Context) (map[string]domain.Setting, error)
	PutSettings(ctx context.Context, set []domain.Setting, nowMS int64) error
	GetBot(ctx context.Context, id string) (domain.Bot, error)
	GetHealthProbe(ctx context.Context, botID string) (domain.HealthProbe, error)
	ListNodes(ctx context.Context) ([]domain.Node, error)
}

// StatusService runs the public status page. Component states come from
// data the panel already has (the panel answering, the agent connection of
// a node, a bot's observed state and health probe) plus manual incidents
// and maintenance. The public view carries only administrator-chosen names
// and descriptions, states, uptime and incident text: never the source's
// id, real name, owner, address or error messages.
type StatusService struct {
	Store StatusStore
	Bots  *BotService // access checks when choosing bots
	// NodeOnline reports whether a remote node's agent is connected; nil
	// means remote nodes are reported as unknown.
	NodeOnline func(nodeID string) bool
	Log        *slog.Logger
	Now        func() time.Time
	// RetentionDays returns how many days of samples are kept (at least
	// StatusRetentionDays, which the public page shows); nil keeps
	// StatusRetentionDays.
	RetentionDays func() int
}

func (s *StatusService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func dayOf(t time.Time) int64 { return t.UTC().Unix() / 86400 }

// StatusConfig is the page configuration.
type StatusConfig struct {
	Enabled bool
	Title   string
	Intro   string
}

// Config returns the page configuration.
func (s *StatusService) Config(ctx context.Context) (StatusConfig, error) {
	set, err := s.Store.Settings(ctx)
	if err != nil {
		return StatusConfig{}, err
	}
	c := StatusConfig{Enabled: set[SetStatusEnabled].Value == "1", Title: set[SetStatusTitle].Value, Intro: set[SetStatusIntro].Value}
	if c.Title == "" {
		c.Title = "Service status"
	}
	return c, nil
}

func (s *StatusService) manager(actor domain.User) error {
	if !actor.Can(domain.PermStatusManage) {
		return domain.Denied(actor, domain.PermStatusManage)
	}
	return nil
}

// SetConfig changes the page configuration.
func (s *StatusService) SetConfig(ctx context.Context, actor domain.User, c StatusConfig) (StatusConfig, error) {
	if err := s.manager(actor); err != nil {
		return c, err
	}
	var err error
	if c.Title, err = cleanLine(c.Title, 100, "title", false); err != nil {
		return c, err
	}
	if utf8.RuneCountInString(c.Intro) > 1000 {
		return c, domain.Invalid("the introduction is too long (1,000 characters at most)")
	}
	c.Intro = strings.TrimSpace(c.Intro)
	en := ""
	if c.Enabled {
		en = "1"
	}
	if err := s.Store.PutSettings(ctx, []domain.Setting{{Key: SetStatusEnabled, Value: en}, {Key: SetStatusTitle, Value: c.Title},
		{Key: SetStatusIntro, Value: c.Intro}}, s.now().UnixMilli()); err != nil {
		return c, err
	}
	return s.Config(ctx)
}

// ---- Component states ----

// stateContext is what one round of state computation reads once.
type stateContext struct {
	nodes     map[string]domain.Node
	incidents []domain.StatusIncident
	now       time.Time
}

func (s *StatusService) loadStateContext(ctx context.Context) (stateContext, error) {
	sc := stateContext{nodes: map[string]domain.Node{}, now: s.now()}
	ns, err := s.Store.ListNodes(ctx)
	if err != nil {
		return sc, err
	}
	for _, n := range ns {
		sc.nodes[n.ID] = n
	}
	sc.incidents, err = s.Store.ListStatusIncidents(ctx, sc.now.AddDate(0, 0, -statusRecentDays).UnixMilli(), 200)
	return sc, err
}

// nodeState is the automatic state of a node.
func (s *StatusService) nodeState(sc stateContext, id string) string {
	n, ok := sc.nodes[id]
	switch {
	case !ok:
		return domain.StateUnknown
	case id == domain.LocalNodeID || !n.IsAgent():
		return domain.StateOperational // served by this panel process
	case !n.Enabled:
		return domain.StateMajor
	case s.NodeOnline == nil:
		return domain.StateUnknown
	case s.NodeOnline(id):
		return domain.StateOperational
	}
	return domain.StateMajor
}

// botState is the automatic state of a bot or game server.
func (s *StatusService) botState(ctx context.Context, sc stateContext, id string) string {
	b, err := s.Store.GetBot(ctx, id)
	if err != nil || b.DesiredState == domain.DesiredDeleted {
		return domain.StateUnknown
	}
	if b.NodeID != "" && b.NodeID != domain.LocalNodeID {
		if ns := s.nodeState(sc, b.NodeID); ns != domain.StateOperational {
			return domain.StateUnknown // the node cannot tell us
		}
	}
	switch b.ObservedState {
	case "running":
		if p, err := s.Store.GetHealthProbe(ctx, b.ID); err == nil && p.Kind != "" && p.Status == "unhealthy" {
			return domain.StateDegraded
		}
		return domain.StateOperational
	case "starting", "building", "stopping":
		return domain.StateDegraded
	case "unknown":
		return domain.StateUnknown
	}
	return domain.StateMajor // stopped or failed: not serving
}

func impactState(impact string) string {
	switch impact {
	case "minor":
		return domain.StateDegraded
	case "major":
		return domain.StatePartial
	case "critical":
		return domain.StateMajor
	}
	return ""
}

// maintenanceActive reports whether maintenance applies now: marked in
// progress, or scheduled and inside its window.
func maintenanceActive(i domain.StatusIncident, now int64) bool {
	if i.Kind != domain.MaintenanceKind || i.Closed() {
		return false
	}
	if i.Status == domain.MaintenanceActive {
		return true
	}
	return i.StartsAtMS != nil && *i.StartsAtMS <= now && (i.EndsAtMS == nil || now < *i.EndsAtMS)
}

// componentState is the automatic state with manual incidents applied:
// active maintenance wins; an open incident raises the state to its impact.
func (s *StatusService) componentState(ctx context.Context, sc stateContext, c domain.StatusComponent) string {
	var st string
	switch c.Kind {
	case domain.ComponentPanel:
		st = domain.StateOperational // this code runs, so the panel answers
	case domain.ComponentNode:
		st = s.nodeState(sc, c.RefID)
	case domain.ComponentBot:
		st = s.botState(ctx, sc, c.RefID)
	default:
		st = domain.StateUnknown
	}
	now := sc.now.UnixMilli()
	for _, i := range sc.incidents {
		if !slices.Contains(i.ComponentIDs, c.ID) {
			continue
		}
		if maintenanceActive(i, now) {
			return domain.StateMaintenance
		}
		if i.Kind == domain.IncidentKind && !i.Closed() {
			if is := impactState(i.Impact); is != "" && domain.StateRank(is) > domain.StateRank(st) {
				st = is
			}
		}
	}
	return st
}

// Sample records the current state of every component and drops counts
// older than the retention period.
func (s *StatusService) Sample(ctx context.Context) error {
	comps, err := s.Store.ListStatusComponents(ctx)
	if err != nil {
		return err
	}
	sc, err := s.loadStateContext(ctx)
	if err != nil {
		return err
	}
	states := make(map[string]string, len(comps))
	for _, c := range comps {
		states[c.ID] = s.componentState(ctx, sc, c)
	}
	today := dayOf(sc.now)
	if len(states) > 0 {
		if err := s.Store.AddStatusSamples(ctx, today, states); err != nil {
			return err
		}
	}
	keep := int64(StatusRetentionDays)
	if s.RetentionDays != nil {
		keep = max(keep, int64(s.RetentionDays()))
	}
	_, err = s.Store.PruneStatusSamples(ctx, today-keep+1)
	return err
}

// RunSampler samples every interval until ctx ends (sampling only while
// the page is enabled).
func (s *StatusService) RunSampler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = StatusSampleInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if c, err := s.Config(ctx); err == nil && c.Enabled {
			if err := s.Sample(ctx); err != nil && ctx.Err() == nil && s.Log != nil {
				s.Log.Warn("status page sample", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- Public view ----

// StatusDayView is one bar of a component's history.
type StatusDayView struct {
	Date   string   // YYYY-MM-DD (UTC)
	State  string   // operational | degraded | partial_outage | major_outage | maintenance | none
	Uptime *float64 // percent, nil without samples
}

// StatusComponentView is a component as the public sees it.
type StatusComponentView struct {
	Component domain.StatusComponent
	State     string
	Uptime    *float64 // percent over the retention period
	Days      []StatusDayView
}

// StatusPage is the whole public page.
type StatusPage struct {
	Config     StatusConfig
	Overall    string
	Components []StatusComponentView
	Incidents  []domain.StatusIncident // open incidents, maintenance not completed, recently closed
	UpdatedMS  int64
}

func dayView(d domain.StatusDay) (string, *float64) {
	up := d.Operational + d.Degraded
	total := up + d.Outage
	if total == 0 {
		if d.Maintenance > 0 {
			return domain.StateMaintenance, nil
		}
		return "none", nil
	}
	pct := math.Round(float64(up)/float64(total)*10000) / 100
	switch {
	case d.Outage == 0 && d.Degraded == 0:
		return domain.StateOperational, &pct
	case d.Outage == 0:
		return domain.StateDegraded, &pct
	case pct >= 95:
		return domain.StatePartial, &pct
	}
	return domain.StateMajor, &pct
}

// Page builds the page. ErrNotFound when the page is disabled.
func (s *StatusService) Page(ctx context.Context) (StatusPage, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return StatusPage{}, err
	}
	if !cfg.Enabled {
		return StatusPage{}, domain.ErrNotFound
	}
	return s.build(ctx, cfg)
}

func (s *StatusService) build(ctx context.Context, cfg StatusConfig) (StatusPage, error) {
	comps, err := s.Store.ListStatusComponents(ctx)
	if err != nil {
		return StatusPage{}, err
	}
	sc, err := s.loadStateContext(ctx)
	if err != nil {
		return StatusPage{}, err
	}
	today := dayOf(sc.now)
	first := today - StatusRetentionDays + 1
	days, err := s.Store.StatusDays(ctx, first)
	if err != nil {
		return StatusPage{}, err
	}
	byComp := map[string]map[int64]domain.StatusDay{}
	for _, d := range days {
		if byComp[d.ComponentID] == nil {
			byComp[d.ComponentID] = map[int64]domain.StatusDay{}
		}
		byComp[d.ComponentID][d.Day] = d
	}
	page := StatusPage{Config: cfg, Overall: domain.StateOperational, UpdatedMS: sc.now.UnixMilli()}
	anyMaint := false
	for _, c := range comps {
		v := StatusComponentView{Component: c, State: s.componentState(ctx, sc, c)}
		var up, total int
		for d := first; d <= today; d++ {
			x := byComp[c.ID][d]
			st, pct := dayView(x)
			v.Days = append(v.Days, StatusDayView{Date: time.Unix(d*86400, 0).UTC().Format("2006-01-02"), State: st, Uptime: pct})
			up += x.Operational + x.Degraded
			total += x.Operational + x.Degraded + x.Outage
		}
		if total > 0 {
			p := math.Round(float64(up)/float64(total)*10000) / 100
			v.Uptime = &p
		}
		if v.State == domain.StateMaintenance {
			anyMaint = true
		} else if domain.StateRank(v.State) > domain.StateRank(page.Overall) {
			page.Overall = v.State
		}
		page.Components = append(page.Components, v)
	}
	if page.Overall == domain.StateOperational && anyMaint {
		page.Overall = domain.StateMaintenance
	}
	page.Incidents = sc.incidents
	return page, nil
}

// ---- Management ----

// StatusSource names what a component follows, for the management view.
type StatusSource struct {
	Kind    string
	ID      string
	Name    string
	Missing bool
}

// StatusManageView is the management page.
type StatusManageView struct {
	Page    StatusPage
	Sources map[string]StatusSource // by component id
}

// Manage returns the page (whether enabled or not) with component sources.
func (s *StatusService) Manage(ctx context.Context, actor domain.User) (StatusManageView, error) {
	if err := s.manager(actor); err != nil {
		return StatusManageView{}, err
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		return StatusManageView{}, err
	}
	p, err := s.build(ctx, cfg)
	if err != nil {
		return StatusManageView{}, err
	}
	// The management view lists every incident of the last 90 days.
	if all, err := s.Store.ListStatusIncidents(ctx, s.now().AddDate(0, 0, -StatusRetentionDays).UnixMilli(), 200); err == nil {
		p.Incidents = all
	}
	v := StatusManageView{Page: p, Sources: map[string]StatusSource{}}
	nodes, _ := s.Store.ListNodes(ctx)
	for _, c := range p.Components {
		src := StatusSource{Kind: c.Component.Kind, ID: c.Component.RefID}
		switch c.Component.Kind {
		case domain.ComponentPanel:
			src.Name = "This panel"
		case domain.ComponentNode:
			src.Missing = true
			for _, n := range nodes {
				if n.ID == c.Component.RefID {
					src.Name, src.Missing = n.Name, false
				}
			}
		case domain.ComponentBot:
			b, err := s.Store.GetBot(ctx, c.Component.RefID)
			if err != nil || b.DesiredState == domain.DesiredDeleted {
				src.Missing = true
			} else if actor.IsAdmin() || s.Bots == nil {
				src.Name = b.Name
			} else if _, err := s.Bots.Get(ctx, actor, b.ID); err == nil {
				src.Name = b.Name
			}
		}
		v.Sources[c.Component.ID] = src
	}
	return v, nil
}

// StatusCandidate is something a manager may add as a component.
type StatusCandidate struct {
	Kind string
	ID   string
	Name string
}

// Candidates lists sources the actor may add: the panel, nodes (with
// nodes.manage) and bots the actor can access (every bot for built-in
// administrators).
func (s *StatusService) Candidates(ctx context.Context, actor domain.User) ([]StatusCandidate, error) {
	if err := s.manager(actor); err != nil {
		return nil, err
	}
	out := []StatusCandidate{{Kind: domain.ComponentPanel, Name: "This panel"}}
	if actor.Can(domain.PermNodesManage) {
		ns, err := s.Store.ListNodes(ctx)
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			out = append(out, StatusCandidate{Kind: domain.ComponentNode, ID: n.ID, Name: n.Name})
		}
	}
	if s.Bots != nil {
		bs, err := s.Bots.List(ctx, actor)
		if err != nil {
			return nil, err
		}
		for _, b := range bs {
			out = append(out, StatusCandidate{Kind: domain.ComponentBot, ID: b.ID, Name: b.Name})
		}
	}
	return out, nil
}

// StatusComponentInput creates or changes a component (Kind and RefID only
// on create).
type StatusComponentInput struct {
	Kind        string
	RefID       string
	Name        *string
	Description *string
	Position    *int
}

// SaveComponent creates (id == "") or updates a component.
func (s *StatusService) SaveComponent(ctx context.Context, actor domain.User, id string, in StatusComponentInput) (domain.StatusComponent, error) {
	if err := s.manager(actor); err != nil {
		return domain.StatusComponent{}, err
	}
	now := s.now().UnixMilli()
	var c domain.StatusComponent
	if id == "" {
		comps, err := s.Store.ListStatusComponents(ctx)
		if err != nil {
			return c, err
		}
		if len(comps) >= statusMaxComponents {
			return c, domain.Invalid(fmt.Sprintf("a status page shows at most %d components", statusMaxComponents))
		}
		c = domain.StatusComponent{ID: uuid.NewString(), Kind: in.Kind, RefID: in.RefID, CreatedAtMS: now, Position: len(comps)}
		if err := s.checkSource(ctx, actor, in.Kind, in.RefID); err != nil {
			return c, err
		}
		if in.Name == nil {
			return c, domain.Invalid("a public name is required")
		}
	} else {
		if !validUUID(id) {
			return c, domain.ErrNotFound
		}
		cur, err := s.Store.GetStatusComponent(ctx, id)
		if err != nil {
			return cur, err
		}
		c = cur
	}
	var err error
	if in.Name != nil {
		if c.Name, err = cleanLine(*in.Name, 100, "public name", true); err != nil {
			return c, err
		}
	}
	if in.Description != nil {
		if c.Description, err = cleanLine(*in.Description, 300, "description", false); err != nil {
			return c, err
		}
	}
	if in.Position != nil {
		if *in.Position < -100000 || *in.Position > 100000 {
			return c, domain.Invalid("position is out of range")
		}
		c.Position = *in.Position
	}
	c.UpdatedAtMS = now
	if err := s.Store.PutStatusComponent(ctx, c); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return c, domain.Invalid("this is already on the status page")
		}
		return c, err
	}
	return c, nil
}

// checkSource validates a new component's source and that the actor may
// publish its state.
func (s *StatusService) checkSource(ctx context.Context, actor domain.User, kind, ref string) error {
	switch kind {
	case domain.ComponentPanel:
		if ref != "" {
			return domain.Invalid("the panel component takes no source id")
		}
	case domain.ComponentNode:
		if !actor.Can(domain.PermNodesManage) {
			return domain.Denied(actor, domain.PermNodesManage)
		}
		ns, err := s.Store.ListNodes(ctx)
		if err != nil {
			return err
		}
		for _, n := range ns {
			if n.ID == ref {
				return nil
			}
		}
		return domain.Invalid("unknown node")
	case domain.ComponentBot:
		if s.Bots == nil || !validUUID(ref) {
			return domain.Invalid("unknown bot or server")
		}
		if _, err := s.Bots.Get(ctx, actor, ref); err != nil {
			if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden) {
				return domain.Invalid("unknown bot or server")
			}
			return err
		}
	default:
		return domain.Invalid("kind must be panel, node or bot")
	}
	return nil
}

// DeleteComponent removes a component and its history.
func (s *StatusService) DeleteComponent(ctx context.Context, actor domain.User, id string) (domain.StatusComponent, error) {
	if err := s.manager(actor); err != nil {
		return domain.StatusComponent{}, err
	}
	if !validUUID(id) {
		return domain.StatusComponent{}, domain.ErrNotFound
	}
	c, err := s.Store.GetStatusComponent(ctx, id)
	if err != nil {
		return c, err
	}
	return c, s.Store.DeleteStatusComponent(ctx, id)
}

// IncidentInput creates or changes an incident. On create Kind, Title,
// Status and Message are required; Message (with Status) adds a timeline
// entry.
type IncidentInput struct {
	Kind         string
	Title        *string
	Impact       *string
	Status       *string
	ComponentIDs *[]string
	StartsAtMS   *int64
	EndsAtMS     *int64
	ClearWindow  bool
	Message      string
}

func statusesFor(kind string) []string {
	if kind == domain.MaintenanceKind {
		return domain.MaintenanceStatuses
	}
	return domain.IncidentStatuses
}

// SaveIncident creates (id == "") or changes an incident. A status change
// is recorded in the timeline (with Message, or a default text).
func (s *StatusService) SaveIncident(ctx context.Context, actor domain.User, id string, in IncidentInput) (domain.StatusIncident, error) {
	if err := s.manager(actor); err != nil {
		return domain.StatusIncident{}, err
	}
	now := s.now().UnixMilli()
	var i domain.StatusIncident
	if id == "" {
		if !slices.Contains(domain.IncidentKinds, in.Kind) {
			return i, domain.Invalid("kind must be incident or maintenance")
		}
		i = domain.StatusIncident{ID: uuid.NewString(), Kind: in.Kind, Impact: "minor", CreatedBy: &actor.ID, CreatedAtMS: now}
		if in.Kind == domain.MaintenanceKind {
			i.Impact, i.Status = "none", domain.MaintenanceScheduled
		} else {
			i.Status = "investigating"
		}
		if in.Title == nil {
			return i, domain.Invalid("title is required")
		}
		if strings.TrimSpace(in.Message) == "" {
			return i, domain.Invalid("a first update message is required")
		}
	} else {
		if !validUUID(id) {
			return i, domain.ErrNotFound
		}
		cur, err := s.Store.GetStatusIncident(ctx, id)
		if err != nil {
			return cur, err
		}
		i = cur
	}
	var err error
	if in.Title != nil {
		if i.Title, err = cleanLine(*in.Title, 200, "title", true); err != nil {
			return i, err
		}
	}
	if in.Impact != nil {
		if !slices.Contains(domain.IncidentImpacts, *in.Impact) {
			return i, domain.Invalid("impact must be none, minor, major or critical")
		}
		i.Impact = *in.Impact
	}
	if in.ComponentIDs != nil {
		comps, err := s.Store.ListStatusComponents(ctx)
		if err != nil {
			return i, err
		}
		var ids []string
		for _, cid := range *in.ComponentIDs {
			if !slices.ContainsFunc(comps, func(c domain.StatusComponent) bool { return c.ID == cid }) {
				return i, domain.Invalid("unknown component")
			}
			if !slices.Contains(ids, cid) {
				ids = append(ids, cid)
			}
		}
		i.ComponentIDs = ids
	}
	if in.ClearWindow {
		i.StartsAtMS, i.EndsAtMS = nil, nil
	}
	if in.StartsAtMS != nil {
		v := *in.StartsAtMS
		i.StartsAtMS = &v
	}
	if in.EndsAtMS != nil {
		v := *in.EndsAtMS
		i.EndsAtMS = &v
	}
	if i.StartsAtMS != nil && i.EndsAtMS != nil && *i.EndsAtMS <= *i.StartsAtMS {
		return i, domain.Invalid("the maintenance window must end after it starts")
	}
	if i.Kind == domain.MaintenanceKind && id == "" && i.StartsAtMS == nil {
		return i, domain.Invalid("scheduled maintenance needs a start time")
	}
	var upd *domain.StatusIncidentUpdate
	changed := false
	if in.Status != nil && *in.Status != i.Status {
		if !slices.Contains(statusesFor(i.Kind), *in.Status) {
			return i, domain.Invalid("status must be one of " + strings.Join(statusesFor(i.Kind), ", "))
		}
		i.Status, changed = *in.Status, true
	} else if in.Status != nil && id == "" {
		if !slices.Contains(statusesFor(i.Kind), *in.Status) {
			return i, domain.Invalid("status must be one of " + strings.Join(statusesFor(i.Kind), ", "))
		}
	}
	msg := strings.TrimSpace(strings.ReplaceAll(in.Message, "\r\n", "\n"))
	if utf8.RuneCountInString(msg) > statusUpdateMax {
		return i, domain.Invalid("the update is too long (5,000 characters at most)")
	}
	if msg == "" && changed {
		msg = "Status changed to " + strings.ReplaceAll(i.Status, "_", " ") + "."
	}
	if msg != "" {
		upd = &domain.StatusIncidentUpdate{ID: uuid.NewString(), IncidentID: i.ID, Status: i.Status, Body: msg, AuthorID: &actor.ID, CreatedAtMS: now}
	}
	if i.Closed() {
		if i.ResolvedAtMS == nil {
			i.ResolvedAtMS = &now
		}
	} else {
		i.ResolvedAtMS = nil
	}
	i.UpdatedAtMS = now
	if err := s.Store.SaveStatusIncident(ctx, i, upd); err != nil {
		return i, err
	}
	return s.Store.GetStatusIncident(ctx, i.ID)
}

// DeleteIncident removes an incident and its timeline.
func (s *StatusService) DeleteIncident(ctx context.Context, actor domain.User, id string) (domain.StatusIncident, error) {
	if err := s.manager(actor); err != nil {
		return domain.StatusIncident{}, err
	}
	if !validUUID(id) {
		return domain.StatusIncident{}, domain.ErrNotFound
	}
	i, err := s.Store.GetStatusIncident(ctx, id)
	if err != nil {
		return i, err
	}
	return i, s.Store.DeleteStatusIncident(ctx, id)
}
