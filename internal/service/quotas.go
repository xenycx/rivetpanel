package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Per-account limits. The panel-wide defaults come from the environment
// (RIVET_MAX_BOTS_PER_USER, RIVET_MAX_SITES_PER_USER, RIVET_USER_MEMORY_BYTES;
// 0 = unlimited). An administrator can give one account a quota
// (user_quotas) that replaces a default field by field and adds per-kind
// counts the panel has no default for. Administrators are exempt.
//
// Apps count until they are deleted, running or stopped: a stopped app keeps
// its place, so an account cannot create many stopped apps and then start
// them all past its limit, and starting an app never needs a check. Only
// growth is refused; a limit lowered below what an account already holds
// leaves its apps and sites alone.

// accountLimit is one resolved per-account limit. Max < 0 is unlimited.
type accountLimit struct {
	Max   int64
	Quota bool // set by the account's quota, not the panel default
}

func limitOf(q *int64, def int64) accountLimit {
	switch {
	case q != nil:
		return accountLimit{Max: *q, Quota: true}
	case def > 0:
		return accountLimit{Max: def}
	}
	return accountLimit{Max: -1}
}

// over reports whether growing by added to after breaks the limit.
func (l accountLimit) over(after, added int64) bool { return l.Max >= 0 && added > 0 && after > l.Max }

// AccountLimits are the per-account limits that apply to one owner.
type AccountLimits struct {
	Apps, Bots, Games, Web, Sites, Memory accountLimit
	Exempt                                bool // administrators
}

func (l AccountLimits) unlimited() bool {
	return l.Exempt || (l.Apps.Max < 0 && l.Bots.Max < 0 && l.Games.Max < 0 && l.Web.Max < 0 && l.Sites.Max < 0 && l.Memory.Max < 0)
}

// quotaOnly keeps only the limits the account's quota sets.
func (l AccountLimits) quotaOnly() AccountLimits {
	for _, x := range []*accountLimit{&l.Apps, &l.Bots, &l.Games, &l.Web, &l.Sites, &l.Memory} {
		if !x.Quota {
			*x = accountLimit{Max: -1}
		}
	}
	return l
}

// accountLimits resolves owner's limits: its quota where one sets a field,
// else the panel default. siteDefault is the panel's site limit (only site
// creation and capacity reports need it).
func (s *BotService) accountLimits(ctx context.Context, owner domain.User, siteDefault int) (AccountLimits, error) {
	if owner.IsAdmin() {
		return AccountLimits{Exempt: true, Apps: limitOf(nil, 0), Bots: limitOf(nil, 0), Games: limitOf(nil, 0),
			Web: limitOf(nil, 0), Sites: limitOf(nil, 0), Memory: limitOf(nil, 0)}, nil
	}
	q, err := s.Store.GetUserQuota(ctx, owner.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return AccountLimits{}, err
	}
	return AccountLimits{
		Apps:   limitOf(q.MaxApps, int64(s.Limits.MaxBotsPerUser)),
		Bots:   limitOf(q.MaxBots, 0),
		Games:  limitOf(q.MaxGames, 0),
		Web:    limitOf(q.MaxWebApps, 0),
		Sites:  limitOf(q.MaxSites, int64(siteDefault)),
		Memory: limitOf(q.MemoryBytes, s.Limits.UserMemoryBytes),
	}, nil
}

// allows refuses growing an account by added when its usage after the
// change, after, goes over a limit. Messages for the panel defaults are the
// ones accounts saw before quotas existed.
func (l AccountLimits) allows(after, added domain.OwnerUsage) error {
	if l.Exempt {
		return nil
	}
	count := func(lim accountLimit, after, added int64, what string) error {
		if !lim.over(after, added) {
			return nil
		}
		if lim.Max == 0 {
			return domain.Invalid(fmt.Sprintf("your account's quota does not allow %s; ask an administrator", what))
		}
		return domain.Invalid(fmt.Sprintf("your account's quota allows at most %d %s and %d already exist; delete one first or ask an administrator for a higher limit",
			lim.Max, what, after-added))
	}
	if l.Apps.over(after.Apps, added.Apps) && !l.Apps.Quota {
		return domain.Invalid(fmt.Sprintf("your account can have at most %d bots; delete one first", l.Apps.Max))
	}
	for _, c := range []struct {
		lim          accountLimit
		after, added int64
		what         string
	}{
		{l.Apps, after.Apps, added.Apps, "apps (bots, game servers and web apps together)"},
		{l.Bots, after.Bots, added.Bots, "bots"},
		{l.Games, after.Games, added.Games, "game servers"},
		{l.Web, after.Web, added.Web, "web apps"},
	} {
		if err := count(c.lim, c.after, c.added, c.what); err != nil {
			return err
		}
	}
	if l.Sites.over(after.Sites, added.Sites) {
		if !l.Sites.Quota {
			return domain.Invalid(fmt.Sprintf("your account can have at most %d sites; delete one first", l.Sites.Max))
		}
		if err := count(l.Sites, after.Sites, added.Sites, "sites"); err != nil {
			return err
		}
	}
	if l.Memory.over(after.Memory, added.Memory) {
		if !l.Memory.Quota {
			return domain.Invalid(fmt.Sprintf("your bots may use at most %d MiB of memory in total; %d MiB is already assigned",
				l.Memory.Max>>20, (after.Memory-added.Memory)>>20))
		}
		return domain.Invalid(fmt.Sprintf("your account's memory quota is %d MiB; %d MiB is already assigned and this change needs %d MiB more",
			l.Memory.Max>>20, (after.Memory-added.Memory)>>20, added.Memory>>20))
	}
	return nil
}

func plusUsage(a, b domain.OwnerUsage) domain.OwnerUsage {
	return domain.OwnerUsage{Apps: a.Apps + b.Apps, Bots: a.Bots + b.Bots, Games: a.Games + b.Games, Web: a.Web + b.Web,
		Sites: a.Sites + b.Sites, Memory: a.Memory + b.Memory}
}

// newApp is the usage one new app of kind with mem of memory adds.
func newApp(kind string, mem int64) domain.OwnerUsage {
	u := domain.OwnerUsage{Apps: 1, Memory: mem}
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

// checkAccount refuses growing owner's holdings by added beyond its limits.
// Creations repeat the check inside the transaction that inserts the row
// (createChecked); this early check refuses before any other work is done.
func (s *BotService) checkAccount(ctx context.Context, owner domain.User, added domain.OwnerUsage) error {
	lim, err := s.accountLimits(ctx, owner, 0)
	if err != nil || lim.unlimited() {
		return err
	}
	return s.checkWithin(ctx, owner.ID, lim, added)
}

func (s *BotService) checkWithin(ctx context.Context, ownerID string, lim AccountLimits, added domain.OwnerUsage) error {
	u, err := s.Store.AccountUsage(ctx, ownerID)
	if err != nil {
		return err
	}
	return lim.allows(plusUsage(u, added), added)
}

// createBot inserts b and checks its owner's limits in the same transaction,
// so concurrent creations cannot both take the last of a limit. extraMemory
// is memory added with the bot but not yet stored (its add-ons).
func (s *BotService) createBot(ctx context.Context, owner domain.User, b domain.Bot, extraMemory int64) error {
	lim, err := s.accountLimits(ctx, owner, 0)
	if err != nil {
		return err
	}
	if lim.unlimited() {
		return s.Store.CreateBot(ctx, b)
	}
	added := newApp(b.Kind, b.MemoryBytes+extraMemory)
	return s.Store.CreateBotChecked(ctx, b, func(u domain.OwnerUsage) error {
		u.Memory += extraMemory
		return lim.allows(u, added)
	})
}

// QuotaInput sets an account's quota. A nil field uses the panel default.
type QuotaInput struct {
	MaxApps, MaxBots, MaxGames, MaxWebApps, MaxSites *int64
	MemoryBytes                                      *int64
}

// QuotaView is an account's quota, what it holds and the panel defaults
// (0 = unlimited) that apply where the quota sets nothing.
type QuotaView struct {
	Quota    domain.UserQuota
	Usage    domain.OwnerUsage
	Defaults struct{ MaxApps, MaxSites, MemoryBytes int64 }
	Admin    bool // administrators are exempt from per-account limits
}

// UserQuota returns an account's quota and usage. Needs users.view.
func (s *BotService) UserQuota(ctx context.Context, actor domain.User, userID string, siteDefault int) (QuotaView, error) {
	if !actor.Can(domain.PermUsersView) {
		return QuotaView{}, domain.ErrForbidden
	}
	u, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return QuotaView{}, err
	}
	var v QuotaView
	if v.Quota, err = s.Store.GetUserQuota(ctx, userID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return QuotaView{}, err
	}
	v.Quota.UserID = userID
	if v.Usage, err = s.Store.AccountUsage(ctx, userID); err != nil {
		return QuotaView{}, err
	}
	v.Defaults.MaxApps, v.Defaults.MaxSites, v.Defaults.MemoryBytes = int64(s.Limits.MaxBotsPerUser), int64(siteDefault), s.Limits.UserMemoryBytes
	v.Admin = u.IsAdmin()
	return v, nil
}

// maxQuotaCount bounds quota counts to something a panel can hold.
const maxQuotaCount = 100000

// SetUserQuota replaces an account's quota; one that sets nothing is
// removed. Only administrators may change quotas: a delegated account
// manager could otherwise raise their own. Lowering a quota below what the
// account holds stops nothing; it only refuses further growth.
func (s *BotService) SetUserQuota(ctx context.Context, actor domain.User, userID string, in QuotaInput) (domain.UserQuota, error) {
	if !actor.IsAdmin() {
		return domain.UserQuota{}, domain.ErrForbidden
	}
	if _, err := s.Store.GetUserByID(ctx, userID); err != nil {
		return domain.UserQuota{}, err
	}
	for _, c := range []struct {
		v    *int64
		what string
	}{{in.MaxApps, "apps"}, {in.MaxBots, "bots"}, {in.MaxGames, "game servers"}, {in.MaxWebApps, "web apps"}, {in.MaxSites, "sites"}} {
		if c.v != nil && (*c.v < 0 || *c.v > maxQuotaCount) {
			return domain.UserQuota{}, domain.Invalid(fmt.Sprintf("the %s quota must be between 0 and %d", c.what, maxQuotaCount))
		}
	}
	if in.MemoryBytes != nil && (*in.MemoryBytes < 0 || *in.MemoryBytes > 1<<50) {
		return domain.UserQuota{}, domain.Invalid("the memory quota must be between 0 and 1 PiB")
	}
	now := s.now()
	q := domain.UserQuota{UserID: userID, MaxApps: in.MaxApps, MaxBots: in.MaxBots, MaxGames: in.MaxGames, MaxWebApps: in.MaxWebApps,
		MaxSites: in.MaxSites, MemoryBytes: in.MemoryBytes, UpdatedBy: &actor.ID, CreatedAtMS: now, UpdatedAtMS: now}
	if q.Empty() {
		return domain.UserQuota{UserID: userID}, s.Store.DeleteUserQuota(ctx, userID)
	}
	if old, err := s.Store.GetUserQuota(ctx, userID); err == nil {
		q.CreatedAtMS = old.CreatedAtMS
	}
	return q, s.Store.SetUserQuota(ctx, q)
}

// ClearUserQuota removes an account's quota (the panel defaults apply).
func (s *BotService) ClearUserQuota(ctx context.Context, actor domain.User, userID string) error {
	if !actor.IsAdmin() {
		return domain.ErrForbidden
	}
	if _, err := s.Store.GetUserByID(ctx, userID); err != nil {
		return err
	}
	return s.Store.DeleteUserQuota(ctx, userID)
}

// QuotaSummary describes a quota for the audit log ("" fields omitted).
func QuotaSummary(q domain.UserQuota) string {
	if q.Empty() {
		return "panel defaults"
	}
	var parts []string
	for _, f := range []struct {
		name string
		v    *int64
	}{{"apps", q.MaxApps}, {"bots", q.MaxBots}, {"games", q.MaxGames}, {"web_apps", q.MaxWebApps}, {"sites", q.MaxSites}, {"memory_mib", q.MemoryBytes}} {
		if f.v == nil {
			continue
		}
		v := *f.v
		if f.name == "memory_mib" {
			v >>= 20
		}
		parts = append(parts, f.name+"="+strconv.FormatInt(v, 10))
	}
	return strings.Join(parts, " ")
}

// checkTransfer refuses handing b to an account whose quota has no room for
// it. Only limits the account's quota sets apply: transfers never checked
// the panel defaults, and still do not.
func (s *BotService) checkTransfer(ctx context.Context, target domain.User, b domain.Bot) error {
	lim, err := s.accountLimits(ctx, target, 0)
	if err != nil {
		return err
	}
	if lim = lim.quotaOnly(); lim.unlimited() {
		return nil
	}
	mem := b.MemoryBytes
	for _, a := range b.Addons {
		mem += a.MemoryBytes
	}
	if err := s.checkWithin(ctx, target.ID, lim, newApp(b.Kind, mem)); err != nil {
		var v *domain.ValidationError
		if errors.As(err, &v) {
			return domain.Invalid("that account's quota has no room for this app; an administrator can raise it")
		}
		return err
	}
	return nil
}
