package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
)

const invitePrefix = "bpi_"

// CreateInvite issues a one-use link that shares the bot with whoever signs
// in and accepts it. Only the owner (or an administrator) may invite, like
// sharing by email. The plaintext token is returned once.
func (s *BotService) CreateInvite(ctx context.Context, actor domain.User, botID string, perms, days int) (string, domain.Invite, error) {
	b, err := s.load(ctx, actor, botID, false)
	if err != nil {
		return "", domain.Invite{}, err
	}
	perms, err = normalizePerms(perms)
	if err != nil {
		return "", domain.Invite{}, err
	}
	if days < 1 || days > 14 {
		return "", domain.Invite{}, domain.Invalid("invitations expire after 1 to 14 days")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", domain.Invite{}, err
	}
	tok := invitePrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := time.UnixMilli(s.now())
	v := domain.Invite{ID: uuid.NewString(), BotID: botID, BotName: b.Name, Permissions: perms, CreatedBy: actor.ID, CreatorName: actor.Email,
		CreatedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(time.Duration(days) * 24 * time.Hour).UnixMilli()}
	if err := s.Store.InsertInvite(ctx, v, auth.HashToken(tok), now.UnixMilli()); err != nil {
		return "", domain.Invite{}, err
	}
	return tok, v, nil
}

// ListInvites returns the bot's open invitations (owner or administrator).
func (s *BotService) ListInvites(ctx context.Context, actor domain.User, botID string) ([]domain.Invite, error) {
	if _, err := s.load(ctx, actor, botID, false); err != nil {
		return nil, err
	}
	return s.Store.ListInvites(ctx, botID, s.now())
}

// RevokeInvite deletes an open invitation.
func (s *BotService) RevokeInvite(ctx context.Context, actor domain.User, botID, id string) error {
	if _, err := s.load(ctx, actor, botID, false); err != nil {
		return err
	}
	return s.Store.DeleteInvite(ctx, botID, id)
}

func inviteHash(tok string) ([]byte, error) {
	tok = strings.TrimSpace(tok)
	if !strings.HasPrefix(tok, invitePrefix) || len(tok) > 80 {
		return nil, domain.Invalid("this invitation link is not valid")
	}
	return auth.HashToken(tok), nil
}

// PreviewInvite shows what accepting would grant, without consuming it.
func (s *BotService) PreviewInvite(ctx context.Context, actor domain.User, tok string) (domain.Invite, error) {
	h, err := inviteHash(tok)
	if err != nil {
		return domain.Invite{}, err
	}
	v, err := s.Store.InviteByHash(ctx, h, s.now())
	if err != nil {
		return domain.Invite{}, domain.Invalid("this invitation link was already used, expired or revoked")
	}
	return v, nil
}

// AcceptInvite consumes the invitation and grants its permissions to actor.
func (s *BotService) AcceptInvite(ctx context.Context, actor domain.User, tok string) (domain.Invite, error) {
	h, err := inviteHash(tok)
	if err != nil {
		return domain.Invite{}, err
	}
	inv, err := s.Store.AcceptInvite(ctx, h, actor.ID, s.now())
	if err == nil {
		link := ""
		if b, err := s.Store.GetBot(ctx, inv.BotID); err == nil {
			link = BotLink(b)
		}
		s.noticeAccess(ctx, actor, inv.CreatedBy, actorName(actor)+" accepted your invitation to "+inv.BotName, "", link)
	}
	return inv, err
}
