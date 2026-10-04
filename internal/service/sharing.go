package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/xenycx/rivetpanel/internal/auth"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
)

func normalizePerms(p int) (int, error) {
	if p < 1 || p > domain.PermAll {
		return 0, domain.Invalid("choose at least one valid permission")
	}
	if p&domain.PermFullAdmin != 0 {
		return domain.PermAll, nil // full admin implies everything
	}
	return p, nil
}

// ListSubUsers returns who a bot is shared with (owner/administrator only).
func (s *BotService) ListSubUsers(ctx context.Context, actor domain.User, botID string) ([]domain.SubUser, error) {
	if _, err := s.load(ctx, actor, botID, false); err != nil {
		return nil, err
	}
	return s.Store.ListSubUsers(ctx, botID)
}

// ShareBot grants (or updates) a registered user's permissions on a bot.
// Only the owner or an administrator may share; a grant never exceeds what
// the permission set names, and the owner cannot be added.
func (s *BotService) ShareBot(ctx context.Context, actor domain.User, botID, email string, perms int) (domain.SubUser, error) {
	if _, err := s.load(ctx, actor, botID, false); err != nil {
		return domain.SubUser{}, err
	}
	perms, err := normalizePerms(perms)
	if err != nil {
		return domain.SubUser{}, err
	}
	norm, err := NormalizeEmail(email)
	if err != nil {
		return domain.SubUser{}, err
	}
	target, err := s.Store.GetUserByEmail(ctx, norm)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && target.Disabled) {
		return domain.SubUser{}, domain.Invalid("no active registered user has that email")
	}
	if err != nil {
		return domain.SubUser{}, err
	}
	now := s.now()
	su := domain.SubUser{BotID: botID, UserID: target.ID, Email: target.Email, Permissions: perms,
		InvitedBy: &actor.ID, CreatedAtMS: now, UpdatedAtMS: now}
	if err := s.Store.SetSubUser(ctx, su); err != nil {
		return domain.SubUser{}, err
	}
	if b, err := s.Store.GetBot(ctx, botID); err == nil {
		s.noticeAccess(ctx, actor, target.ID, actorName(actor)+" shared "+b.Name+" with you", "", BotLink(b))
	}
	return su, nil
}

// UnshareBot revokes a user's access. A sub-user may remove themselves.
func (s *BotService) UnshareBot(ctx context.Context, actor domain.User, botID, userID string) error {
	if actor.ID == userID {
		if _, err := s.Get(ctx, actor, botID); err != nil {
			return err
		}
	} else if _, err := s.load(ctx, actor, botID, false); err != nil {
		return err
	}
	return s.Store.RemoveSubUser(ctx, botID, userID)
}

// RotateTelemetryKey issues a new bot-to-panel telemetry key, invalidating the
// old one, and stores it as the bot's RIVET_TELEMETRY_KEY environment
// variable (sealed like other variables) so the bot can read it. The bot must
// be stopped, like every configuration change. The plaintext is returned once.
func (s *BotService) RotateTelemetryKey(ctx context.Context, actor domain.User, id, publicURL string) (string, error) {
	b, err := s.loadPerm(ctx, actor, id, domain.PermFullAdmin, false)
	if err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := telemetryKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	vars := map[string]string{"RIVET_TELEMETRY_KEY": key}
	if publicURL != "" {
		vars["RIVET_URL"] = publicURL
	}
	if err := s.putEnv(ctx, b, vars, true); err != nil {
		return "", err
	}
	if err := s.Store.SetBotTelemetryKey(ctx, b.ID, auth.HashToken(key), s.now()); err != nil {
		return "", err
	}
	return key, nil
}

// RevokeTelemetryKey disables the bot's telemetry key.
func (s *BotService) RevokeTelemetryKey(ctx context.Context, actor domain.User, id string) error {
	b, err := s.loadPerm(ctx, actor, id, domain.PermFullAdmin, false)
	if err != nil {
		return err
	}
	return s.Store.SetBotTelemetryKey(ctx, b.ID, nil, s.now())
}

// Kill is the emergency stop: it records stopped intent (so the runner does not
// restart the bot) and immediately SIGKILLs its containers.
func (s *BotService) Kill(ctx context.Context, actor domain.User, id string) (domain.Bot, error) {
	if s.Killer == nil || s.Notifier == nil {
		return domain.Bot{}, domain.ErrRunnerUnavailable
	}
	b, err := s.loadPerm(ctx, actor, id, domain.PermPower, false)
	if err != nil {
		return domain.Bot{}, err
	}
	nb, changed, err := s.Store.SetDesired(ctx, b.ID, domain.DesiredStopped, false, s.now())
	if err != nil {
		return domain.Bot{}, err
	}
	if changed {
		s.Bus.Publish(events.Status{BotID: b.ID, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
			Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
	}
	if err := s.Killer.Kill(ctx, b.ID); err != nil {
		return nb, fmt.Errorf("kill: %w", err)
	}
	return nb, nil
}

// PortInput is a requested port mapping.
type PortInput struct {
	ContainerPort int
	HostPort      int
	Protocol      string
	HostIP        string
}

// SetPorts replaces a bot's published ports. Only the owner or an
// administrator may expose host ports; the bot must be stopped.
func (s *BotService) SetPorts(ctx context.Context, actor domain.User, id string, in []PortInput) (domain.Bot, error) {
	b, err := s.load(ctx, actor, id, false)
	if err != nil {
		return domain.Bot{}, err
	}
	if len(in) > 0 && b.IsGame() {
		// A game server publishes its allocations (TCP and UDP); bot port
		// mappings would never be applied and would only hold host ports.
		return domain.Bot{}, domain.Invalid("game servers publish their allocations; add a port under Network → Allocations")
	}
	if len(in) > 16 {
		return domain.Bot{}, domain.Invalid("at most 16 port mappings")
	}
	if len(in) > 0 && b.NetworkDisabled {
		return domain.Bot{}, domain.Invalid("enable networking before publishing ports")
	}
	lo, hi := s.Limits.PortMin, s.Limits.PortMax
	if lo == 0 && hi == 0 {
		lo, hi = 20000, 29999
	}
	now := s.now()
	seenC, seenH := map[string]bool{}, map[string]bool{}
	ports := make([]domain.BotPort, 0, len(in))
	for _, p := range in {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		ip := p.HostIP
		if ip == "" {
			ip = "127.0.0.1"
		}
		switch {
		case proto != "tcp" && proto != "udp":
			return domain.Bot{}, domain.Invalid("protocol must be tcp or udp")
		case p.ContainerPort < 1 || p.ContainerPort > 65535:
			return domain.Bot{}, domain.Invalid("container port must be between 1 and 65535")
		case p.HostPort < lo || p.HostPort > hi:
			return domain.Bot{}, domain.Invalid(fmt.Sprintf("host port must be between %d and %d", lo, hi))
		case ip != "127.0.0.1" && !(ip == "0.0.0.0" && s.Limits.PortPublicBind):
			return domain.Bot{}, domain.Invalid("host address must be 127.0.0.1 (public binding is disabled by the administrator)")
		}
		ck, hk := fmt.Sprintf("%d/%s", p.ContainerPort, proto), fmt.Sprintf("%s:%d/%s", ip, p.HostPort, proto)
		if seenC[ck] || seenH[hk] {
			return domain.Bot{}, domain.Invalid("duplicate port mapping")
		}
		seenC[ck], seenH[hk] = true, true
		ports = append(ports, domain.BotPort{BotID: b.ID, ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: proto, HostIP: ip, CreatedAtMS: now})
	}
	if err := s.Store.SetBotPorts(ctx, b.ID, b.Generation, ports, now); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.Bot{}, domain.Invalid("a host port is already used by another bot")
		}
		return domain.Bot{}, err
	}
	return s.Store.GetBot(ctx, b.ID)
}

// TransferBot hands a bot to another active account (owner or administrator
// only). A running deployment is cancelled first and the GitHub link is
// removed (with its webhook, best effort): the link uses the previous owner's
// GitHub token, which must never pass silently to someone else. The new owner
// links the repository again with their own account.
func (s *BotService) TransferBot(ctx context.Context, actor domain.User, botID, email string, keepAccess bool) (removedRepo bool, err error) {
	b, err := s.load(ctx, actor, botID, false)
	if err != nil {
		return false, err
	}
	norm, err := NormalizeEmail(email)
	if err != nil {
		return false, err
	}
	target, err := s.Store.GetUserByEmail(ctx, norm)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && target.Disabled) {
		return false, domain.Invalid("no active registered user has that email")
	}
	if err != nil {
		return false, err
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_ = s.Coord.Preempt(pctx, b.ID)
	cancel()
	if s.BeforeDelete != nil { // removes the GitHub webhook with the old token
		s.BeforeDelete(ctx, b.ID)
	}
	removed, err := s.Store.TransferBot(ctx, b.ID, target.ID, keepAccess, s.now())
	if err == nil {
		s.noticeAccess(ctx, actor, target.ID, actorName(actor)+" transferred "+b.Name+" to you", "You are now its owner.", BotLink(b))
	}
	return removed, err
}
