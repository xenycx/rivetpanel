package api

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
	"github.com/xenycx/rivetpanel/internal/service"
)

// accountInviteDTO is a pending invitation as administrators see it: the
// role and setup the account will get, with the names of what they refer
// to ("" when a role, node or workspace was deleted since).
type accountInviteDTO struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	Role           string    `json:"role"`
	RoleID         string    `json:"role_id"`
	RoleName       string    `json:"role_name"`
	CreatedByEmail string    `json:"created_by_email"`
	Quota          *quotaDTO `json:"quota"`
	NodeID         string    `json:"node_id"`
	NodeName       string    `json:"node_name"`
	WorkspaceID    string    `json:"workspace_id"`
	WorkspaceName  string    `json:"workspace_name"`
	WorkspaceRole  string    `json:"workspace_role"`
	CreatedAtMS    int64     `json:"created_at_ms"`
	ExpiresAtMS    int64     `json:"expires_at_ms"`
}

func accountInviteOut(v domain.AccountInvite) accountInviteDTO {
	out := accountInviteDTO{ID: v.ID, Email: v.Email, Role: v.Role, RoleID: v.RoleID, RoleName: v.RoleName,
		CreatedByEmail: v.CreatedByEmail, NodeID: v.NodeID, NodeName: v.NodeName,
		WorkspaceID: v.WorkspaceID, WorkspaceName: v.WorkspaceName, WorkspaceRole: v.WorkspaceRole,
		CreatedAtMS: v.CreatedAtMS, ExpiresAtMS: v.ExpiresAtMS}
	if v.Quota != nil {
		q := toQuota(*v.Quota)
		out.Quota = &q
	}
	return out
}

// inviteAuditTarget summarizes an invitation for the audit log.
func inviteAuditTarget(v domain.AccountInvite) string {
	who := v.Email
	if who == "" {
		who = "link"
	}
	limits, node, workspace := "none", "default", "none"
	if v.Quota != nil {
		limits = service.QuotaSummary(*v.Quota)
	}
	if v.NodeID != "" {
		node = v.NodeName
	}
	if v.WorkspaceID != "" {
		workspace = v.WorkspaceName + "/" + v.WorkspaceRole
	}
	return who + " role=" + v.RoleName + " limits=" + limits + " node=" + node + " workspace=" + workspace
}

func (s *panel) registrationStatus(c fiber.Ctx) error {
	enabled := false
	if s.settings != nil {
		if e, err := s.settings.Effective(c.Context()); err == nil {
			enabled = e.AllowSignup
		}
	}
	providers := []string{}
	if enabled && s.oauth != nil {
		for _, p := range []string{domain.ProviderGitHub, domain.ProviderDiscord} {
			if s.oauth.Enabled(p) {
				providers = append(providers, p)
			}
		}
	}
	return c.JSON(fiber.Map{"enabled": enabled, "providers": providers})
}

// previewAccountInvite tells the holder of a link what the account will be:
// the address it is for, the role's name and the workspace it joins. Limits
// and placement are the panel's business and stay private.
func (s *panel) previewAccountInvite(c fiber.Ctx) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.auth.PreviewAccountInvite(c.Context(), in.Token)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": v.ID, "email": v.Email, "role": v.Role, "role_name": v.RoleName,
		"workspace_name": v.WorkspaceName, "created_at_ms": v.CreatedAtMS, "expires_at_ms": v.ExpiresAtMS})
}

func (s *panel) registerAccount(c fiber.Ctx) error {
	if s.settings == nil {
		return domain.Invalid("registration is disabled")
	}
	e, err := s.settings.Effective(c.Context())
	if err != nil {
		return err
	}
	if !e.AllowSignup {
		return domain.Invalid("registration is disabled")
	}
	var in struct{ Token, Email, Password string }
	if err := decode(c, &in); err != nil {
		return err
	}
	u, inv, err := s.auth.RegisterWithInvite(c.Context(), in.Token, in.Email, in.Password)
	if err != nil {
		return err
	}
	if s.audit != nil {
		ip := c.IP()
		s.audit.Record(c.Context(), domain.AuditEvent{Action: "account.register", ActorID: &u.ID, ActorLabel: &u.Email,
			SubjectUserID: &u.ID, Target: &inv.ID, Outcome: "ok", IP: &ip})
	}
	sess, err := s.auth.IssueSessionFor(c.Context(), u, deviceLabel(c.Get(fiber.HeaderUserAgent)))
	if err != nil {
		return err
	}
	s.setSessionCookie(c, sess.Token, time.UnixMilli(sess.ExpiresAtMS))
	// New accounts start unverified; send the first link right away when the
	// panel can (a failure is not an error: the account can ask again).
	if s.verify != nil && s.verify.Available(c.Context()) {
		_ = s.verify.Send(c.Context(), u)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"user": toUser(u), "csrf_token": sess.CSRF})
}

func (s *panel) createAccountInvite(c fiber.Ctx) error {
	var in struct {
		Email, Role   string
		ExpiresInDays int       `json:"expires_in_days"`
		SendEmail     bool      `json:"send_email"`
		Quota         *quotaDTO `json:"quota"`
		NodeID        string    `json:"node_id"`
		WorkspaceID   string    `json:"workspace_id"`
		WorkspaceRole string    `json:"workspace_role"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	input := service.AccountInviteInput{Email: in.Email, Role: in.Role, Days: in.ExpiresInDays,
		NodeID: in.NodeID, WorkspaceID: in.WorkspaceID, WorkspaceRole: in.WorkspaceRole}
	if in.Quota != nil {
		input.Quota = &service.QuotaInput{MaxApps: in.Quota.MaxApps, MaxBots: in.Quota.MaxBots, MaxGames: in.Quota.MaxGames,
			MaxWebApps: in.Quota.MaxWebApps, MaxSites: in.Quota.MaxSites, MemoryBytes: in.Quota.MemoryBytes, NanoCPUs: in.Quota.NanoCPUs}
	}
	v, tok, err := s.auth.CreateAccountInvite(c.Context(), currentUser(c), input)
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, inviteAuditTarget(v))
	c.Set(fiber.HeaderCacheControl, "no-store")
	// The invitation exists whether or not the email goes out, and its link is
	// returned either way; the email result is reported next to it.
	emailed, emailErr := false, ""
	if in.SendEmail {
		emailed, emailErr = s.emailInvite(c, v, tok, in.ExpiresInDays)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"invite": accountInviteOut(v), "token": tok, "path": "/register#" + tok, "emailed": emailed, "email_error": emailErr})
}

func (s *panel) listAccountInvites(c fiber.Ctx) error {
	vs, err := s.auth.ListAccountInvites(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]accountInviteDTO, len(vs))
	for i, v := range vs {
		out[i] = accountInviteOut(v)
	}
	return c.JSON(fiber.Map{"invites": out})
}

func (s *panel) deleteAccountInvite(c fiber.Ctx) error {
	if err := s.auth.DeleteAccountInvite(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// emailInvite mails the invitation link to the invited address and returns
// whether it was started, or a sentence explaining why not.
func (s *panel) emailInvite(c fiber.Ctx, v domain.AccountInvite, tok string, days int) (bool, string) {
	if v.Email == "" {
		return false, "this invitation has no email address to send to"
	}
	if !s.mail.Enabled(c.Context()) {
		return false, "email is not set up (Panel settings); copy the link instead"
	}
	e, err := s.settings.Effective(c.Context())
	if err != nil || e.PublicURL == "" {
		return false, "the panel address is not set (Panel settings), so the link cannot be mailed"
	}
	if !s.mail.Queue(v.Email, mail.Invitation(e.PublicURL+"/register#"+tok, v.RoleName, v.WorkspaceName, days), "invitation") {
		return false, "the email could not be started; copy the link instead"
	}
	return true, ""
}
