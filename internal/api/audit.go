package api

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// keyAuditTarget lets a handler replace the recorded target with a better
// description of what it changed (for example a role's name and permissions).
const keyAuditTarget ctxKey = 101

// auditRoute names what a changing request did. The target is a name taken
// from the route or query (a variable name, file path, backup id), never a
// value or file content.
type auditRoute struct {
	action string
	target func(c fiber.Ctx) string
}

func param(n string) func(fiber.Ctx) string { return func(c fiber.Ctx) string { return c.Params(n) } }
func query(n string) func(fiber.Ctx) string { return func(c fiber.Ctx) string { return c.Query(n) } }

var auditRoutes = map[string]auditRoute{
	"POST /api/v1/bots":                                       {"bot.create", nil},
	"POST /api/v1/games":                                      {"game.create", nil},
	"POST /api/v1/bots/:id/command":                           {"console.command", nil},
	"POST /api/v1/bots/:id/game/addons":                       {"game.addon_install", nil},
	"PUT /api/v1/bots/:id/game/variables":                     {"game.variables", nil},
	"PUT /api/v1/bots/:id/game/jvm-args":                      {"game.jvm_args", nil},
	"POST /api/v1/bots/:id/game/reinstall":                    {"game.reinstall", nil},
	"PUT /api/v1/bots/:id/game/image":                         {"game.image", nil},
	"POST /api/v1/bots/:id/game/upgrade":                      {"game.upgrade", nil},
	"POST /api/v1/bots/:id/allocations":                       {"game.allocation_add", nil},
	"DELETE /api/v1/bots/:id/allocations/:aid":                {"game.allocation_remove", param("aid")},
	"PUT /api/v1/bots/:id/allocations/:aid/primary":           {"game.allocation_primary", param("aid")},
	"POST /api/v1/admin/blueprints":                           {"admin.blueprint_import", nil},
	"POST /api/v1/admin/blueprints/egg-preview":               {"admin.blueprint_egg_preview", nil},
	"PATCH /api/v1/admin/blueprints/:bp":                      {"admin.blueprint_update", param("bp")},
	"DELETE /api/v1/admin/blueprints/:bp":                     {"admin.blueprint_delete", param("bp")},
	"POST /api/v1/admin/allocations":                          {"admin.allocations_create", nil},
	"PATCH /api/v1/admin/allocations/:aid":                    {"admin.allocation_update", param("aid")},
	"DELETE /api/v1/admin/allocations/:aid":                   {"admin.allocation_delete", param("aid")},
	"PATCH /api/v1/nodes/:id":                                 {"admin.node_update", param("id")},
	"DELETE /api/v1/nodes/:id":                                {"admin.node_delete", param("id")},
	"POST /api/v1/nodes/:id/revoke-certificates":              {"admin.node_certificates_revoke", param("id")},
	"POST /api/v1/nodes/enrollments":                          {"admin.node_enroll", bodyField("name")},
	"POST /api/v1/nodes/:id/enrollment":                       {"admin.node_reenroll", param("id")},
	"POST /api/v1/nodes/locations":                            {"admin.location_create", bodyField("name")},
	"PATCH /api/v1/nodes/locations/:id":                       {"admin.location_update", param("id")},
	"DELETE /api/v1/nodes/locations/:id":                      {"admin.location_delete", param("id")},
	"PATCH /api/v1/bots/:id":                                  {"bot.update", nil},
	"DELETE /api/v1/bots/:id":                                 {"bot.delete", nil},
	"POST /api/v1/bots/:id/start":                             {"bot.start", nil},
	"POST /api/v1/bots/:id/stop":                              {"bot.stop", nil},
	"POST /api/v1/bots/:id/restart":                           {"bot.restart", nil},
	"POST /api/v1/bots/:id/kill":                              {"bot.kill", nil},
	"PUT /api/v1/bots/:id/ports":                              {"bot.ports", nil},
	"PUT /api/v1/bots/:id/env":                                {"env.set", envNames},
	"POST /api/v1/bots/:id/env/:name/reveal":                  {"env.reveal", param("name")},
	"DELETE /api/v1/bots/:id/env/:name":                       {"env.delete", param("name")},
	"PUT /api/v1/bots/:id/files/content":                      {"files.write", query("path")},
	"DELETE /api/v1/bots/:id/files":                           {"files.delete", query("path")},
	"POST /api/v1/bots/:id/files/mkdir":                       {"files.mkdir", nil},
	"POST /api/v1/bots/:id/files/move":                        {"files.move", nil},
	"POST /api/v1/bots/:id/files/extract":                     {"files.extract", query("path")},
	"POST /api/v1/bots/:id/files/compress":                    {"files.compress", nil},
	"POST /api/v1/bots/:id/files/decompress":                  {"files.decompress", nil},
	"PUT /api/v1/bots/:id/packages":                           {"packages.edit", nil},
	"PUT /api/v1/bots/:id/github":                             {"deploy.link", nil},
	"DELETE /api/v1/bots/:id/github":                          {"deploy.unlink", nil},
	"POST /api/v1/bots/:id/github/deploy":                     {"deploy.start", nil},
	"POST /api/v1/bots/:id/backups":                           {"backup.create", nil},
	"POST /api/v1/bots/:id/backups/:bid/restore":              {"backup.restore", param("bid")},
	"DELETE /api/v1/bots/:id/backups/:bid":                    {"backup.delete", param("bid")},
	"PATCH /api/v1/bots/:id/backups/:bid":                     {"backup.label", param("bid")},
	"PUT /api/v1/bots/:id/users":                              {"access.grant", nil},
	"POST /api/v1/bots/:id/invites":                           {"access.invite", nil},
	"DELETE /api/v1/bots/:id/invites/:iid":                    {"access.invite_revoke", param("iid")},
	"POST /api/v1/invites/accept":                             {"access.invite_accept", nil},
	"DELETE /api/v1/bots/:id/users/:uid":                      {"access.revoke", param("uid")},
	"POST /api/v1/bots/:id/telemetry-key":                     {"telemetry.key", nil},
	"DELETE /api/v1/bots/:id/telemetry-key":                   {"telemetry.revoke", nil},
	"POST /api/v1/bots/:id/schedules":                         {"schedule.create", nil},
	"PATCH /api/v1/bots/:id/schedules/:sid":                   {"schedule.update", param("sid")},
	"DELETE /api/v1/bots/:id/schedules/:sid":                  {"schedule.delete", param("sid")},
	"POST /api/v1/bots/:id/transfer":                          {"bot.transfer", nil},
	"PUT /api/v1/bots/:id/alerts":                             {"bot.alerts", nil},
	"PUT /api/v1/bots/:id/tags":                               {"bot.tags", nil},
	"POST /api/v1/bots/batch":                                 {"bot.batch", nil},
	"POST /api/v1/me/password":                                {"account.password", nil},
	"POST /api/v1/me/email":                                   {"account.email_change_request", nil},
	"DELETE /api/v1/me/sessions/:sid":                         {"account.session_revoke", nil},
	"POST /api/v1/me/sessions/revoke-others":                  {"account.sessions_revoke", nil},
	"POST /api/v1/me/api-keys":                                {"account.key_create", nil},
	"DELETE /api/v1/me/api-keys/:id":                          {"account.key_delete", nil},
	"POST /api/v1/me/tokens":                                  {"account.token_create", nil},
	"POST /api/v1/me/api-clients":                             {"account.api_client_create", bodyField("name")},
	"DELETE /api/v1/me/api-clients/:id":                       {"account.api_client_revoke", nil},
	"DELETE /api/v1/admin/api-clients/:id":                    {"admin.api_client_revoke", param("id")},
	"DELETE /api/v1/me/identities/:pid":                       {"account.identity_unlink", param("pid")},
	"POST /api/v1/me/passkeys/register/finish":                {"account.passkey_add", nil},
	"PATCH /api/v1/me/passkeys/:id":                           {"account.passkey_rename", bodyField("name")},
	"DELETE /api/v1/me/passkeys/:id":                          {"account.passkey_delete", nil},
	"POST /api/v1/admin/oidc-providers":                       {"admin.oidc_provider_create", bodyField("name")},
	"PATCH /api/v1/admin/oidc-providers/:id":                  {"admin.oidc_provider_update", param("id")},
	"DELETE /api/v1/admin/oidc-providers/:id":                 {"admin.oidc_provider_delete", param("id")},
	"DELETE /api/v1/me/tokens/:id":                            {"account.token_delete", nil},
	"DELETE /api/v1/me/connections/:provider":                 {"account.disconnect", param("provider")},
	"POST /api/v1/me/mfa/enable":                              {"account.mfa_enable", nil},
	"POST /api/v1/me/mfa/disable":                             {"account.mfa_disable", nil},
	"POST /api/v1/users":                                      {"admin.user_create", bodyField("email")},
	"POST /api/v1/admin/roles":                                {"admin.role_create", bodyField("name")},
	"PATCH /api/v1/admin/roles/:rid":                          {"admin.role_update", param("rid")},
	"DELETE /api/v1/admin/roles/:rid":                         {"admin.role_delete", param("rid")},
	"PATCH /api/v1/users/:id":                                 {"admin.user_update", param("id")},
	"PUT /api/v1/admin/settings":                              {"admin.settings", nil},
	"POST /api/v1/admin/settings/mail/test":                   {"admin.mail_test", nil},
	"PUT /api/v1/admin/log-archive":                           {"admin.log_archive_settings", nil},
	"POST /api/v1/admin/log-archive/run":                      {"admin.log_archive_run", nil},
	"PUT /api/v1/me/email-alerts":                             {"account.email_alerts", nil},
	"PUT /api/v1/me/email-news":                               {"account.email_news", nil},
	"PUT /api/v1/me/notification-prefs":                       {"account.notification_prefs", nil},
	"POST /api/v1/tickets":                                    {"support.ticket_create", nil},
	"POST /api/v1/tickets/:tid/messages":                      {"support.ticket_reply", param("tid")},
	"PATCH /api/v1/tickets/:tid":                              {"support.ticket_update", param("tid")},
	"POST /api/v1/kb/manage/articles":                         {"kb.article_create", nil},
	"PATCH /api/v1/kb/manage/articles/:aid":                   {"kb.article_update", param("aid")},
	"DELETE /api/v1/kb/manage/articles/:aid":                  {"kb.article_delete", param("aid")},
	"POST /api/v1/kb/manage/categories":                       {"kb.category_create", nil},
	"PATCH /api/v1/kb/manage/categories/:cid":                 {"kb.category_update", param("cid")},
	"DELETE /api/v1/kb/manage/categories/:cid":                {"kb.category_delete", param("cid")},
	"PUT /api/v1/kb/manage/settings":                          {"kb.settings", nil},
	"PUT /api/v1/status/manage/config":                        {"status.config", nil},
	"POST /api/v1/status/manage/components":                   {"status.component_create", nil},
	"PATCH /api/v1/status/manage/components/:cid":             {"status.component_update", param("cid")},
	"DELETE /api/v1/status/manage/components/:cid":            {"status.component_delete", param("cid")},
	"POST /api/v1/status/manage/incidents":                    {"status.incident_create", nil},
	"PATCH /api/v1/status/manage/incidents/:iid":              {"status.incident_update", param("iid")},
	"POST /api/v1/status/manage/incidents/:iid/updates":       {"status.incident_post", param("iid")},
	"DELETE /api/v1/status/manage/incidents/:iid":             {"status.incident_delete", param("iid")},
	"POST /api/v1/admin/mail/announcements":                   {"admin.announcement", announcementTarget},
	"PUT /api/v1/admin/environment":                           {"admin.environment", environmentNames},
	"POST /api/v1/admin/environment/restart":                  {"admin.restart", nil},
	"POST /api/v1/auth/logout":                                {"account.logout", nil},
	"POST /api/v1/workspaces":                                 {"workspace.create", bodyField("name")},
	"PATCH /api/v1/workspaces/:wid":                           {"workspace.rename", param("wid")},
	"DELETE /api/v1/workspaces/:wid":                          {"workspace.delete", param("wid")},
	"PUT /api/v1/workspaces/:wid/members":                     {"workspace.member_add", bodyField("email")},
	"PATCH /api/v1/workspaces/:wid/members/:uid":              {"workspace.member_role", param("uid")},
	"DELETE /api/v1/workspaces/:wid/members/:uid":             {"workspace.member_remove", param("uid")},
	"PUT /api/v1/bots/:id/workspace":                          {"bot.move", bodyField("workspace_id")},
	"POST /api/v1/bots/:id/github/publish":                    {"deploy.publish", bodyField("name")},
	"POST /api/v1/bots/:id/github/push":                       {"deploy.push", nil},
	"POST /api/v1/sites":                                      {"site.create", bodyField("name")},
	"PATCH /api/v1/sites/:sid":                                {"site.update", param("sid")},
	"DELETE /api/v1/sites/:sid":                               {"site.delete", param("sid")},
	"POST /api/v1/sites/:sid/upload":                          {"site.upload", param("sid")},
	"POST /api/v1/sites/:sid/deploy":                          {"site.deploy", param("sid")},
	"POST /api/v1/sites/:sid/releases/:rid/activate":          {"site.rollback", param("rid")},
	"POST /api/v1/sites/:sid/domains":                         {"site.domain_add", bodyField("domain")},
	"POST /api/v1/sites/:sid/domains/:domain/verify":          {"site.domain_verify", param("domain")},
	"DELETE /api/v1/sites/:sid/domains/:domain":               {"site.domain_remove", param("domain")},
	"PATCH /api/v1/admin/sites/:sid":                          {"admin.site_update", param("sid")},
	"POST /api/v1/admin/site-base-domains":                    {"admin.sites_domain_add", bodyField("domain")},
	"POST /api/v1/admin/site-base-domains/:domain/verify":     {"admin.sites_domain_verify", param("domain")},
	"POST /api/v1/admin/site-base-domains/:domain/move-sites": {"admin.sites_domain_move", param("domain")},
	"PATCH /api/v1/admin/site-base-domains/:domain":           {"admin.sites_domain_update", param("domain")},
	"DELETE /api/v1/admin/site-base-domains/:domain":          {"admin.sites_domain_remove", param("domain")},
	"POST /api/v1/admin/ai/providers":                         {"admin.ai_provider_create", nil},
	"PATCH /api/v1/admin/ai/providers/:provider":              {"admin.ai_provider_update", param("provider")},
	"DELETE /api/v1/admin/ai/providers/:provider":             {"admin.ai_provider_delete", param("provider")},
	"POST /api/v1/admin/ai/providers/:provider/test":          {"admin.ai_provider_test", param("provider")},
	"PUT /api/v1/admin/ai/search":                             {"admin.ai_search", nil},
	"POST /api/v1/admin/ai/search/test":                       {"admin.ai_search_test", nil},
	"POST /api/v1/ai/conversations":                           {"ai.conversation_create", nil},
	"POST /api/v1/bots/:id/ai/conversations":                  {"ai.conversation_create", nil},
	"POST /api/v1/sites/:sid/ai/conversations":                {"ai.conversation_create", nil},
	"DELETE /api/v1/ai/conversations/:conversation":           {"ai.conversation_delete", param("conversation")},
	"POST /api/v1/ai/conversations/:conversation/messages":    {"ai.run_start", param("conversation")},
	"POST /api/v1/ai/runs/:run/cancel":                        {"ai.run_cancel", param("run")},
	"POST /api/v1/ai/tool-calls/:call/decision":               {"ai.approval", param("call")},
	"POST /api/v1/ai/tool-calls/:call/secure-input":           {"ai.secure_input", secureInputNames},
	"POST /api/v1/ai/change-sets/:change/revert":              {"ai.change_revert", param("change")},
	// Automation API: the target names the token, so the record shows which
	// credential acted.
	"POST /api/v1/automation/bots/:id/start":   {"bot.start", viaToken},
	"POST /api/v1/automation/bots/:id/stop":    {"bot.stop", viaToken},
	"POST /api/v1/automation/bots/:id/restart": {"bot.restart", viaToken},
	"POST /api/v1/automation/bots/:id/deploy":  {"deploy.start", viaToken},
	"POST /api/v1/automation/bots/:id/backups": {"backup.create", viaToken},
}

func secureInputNames(c fiber.Ctx) string {
	var in struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if json.Unmarshal(c.Body(), &in) != nil {
		return ""
	}
	names := make([]string, 0, len(in.Values))
	for n := range in.Values {
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

// bodyField names a short, non-secret JSON string field of the request body
// (a name, email or domain), truncated.
func bodyField(name string) func(fiber.Ctx) string {
	return func(c fiber.Ctx) string {
		var in map[string]json.RawMessage
		if json.Unmarshal(c.Body(), &in) != nil {
			return ""
		}
		var v string
		if json.Unmarshal(in[name], &v) != nil {
			return ""
		}
		if len(v) > 200 {
			v = v[:197] + "..."
		}
		return v
	}
}

func viaToken(c fiber.Ctx) string { return "token: " + currentAutoToken(c).Name }

// envNames lists the variable NAMES of a PUT /env body (never the values).
func envNames(c fiber.Ctx) string {
	var in struct {
		Vars map[string]json.RawMessage `json:"vars"`
	}
	if json.Unmarshal(c.Body(), &in) != nil {
		return ""
	}
	names := make([]string, 0, len(in.Vars))
	for n := range in.Vars {
		names = append(names, n)
	}
	s := strings.Join(names, ", ")
	if len(s) > 300 {
		s = s[:297] + "..."
	}
	return s
}

// auditMW records every changing request after it ran (success and refusal;
// server errors are recorded as failed). Reads are not recorded, except the
// explicit secret reveal, which is a POST.
func (s *panel) auditMW(c fiber.Ctx) error {
	err := c.Next()
	if s.audit == nil || isSafeMethod(c.Method()) {
		return err
	}
	r, ok := auditRoutes[c.Method()+" "+c.Route().Path]
	if !ok {
		return err
	}
	status := c.Response().StatusCode()
	if err != nil {
		status = fiber.StatusInternalServerError
		if fe, ok := err.(*fiber.Error); ok {
			status = fe.Code
		}
		var e errorBody
		_ = e
		if code := statusOfError(err); code != 0 {
			status = code
		}
	}
	outcome := "ok"
	switch {
	case status == fiber.StatusForbidden || status == fiber.StatusNotFound || status == fiber.StatusUnauthorized:
		outcome = "denied"
	case status >= 400:
		outcome = "failed"
	}
	u := currentUser(c)
	// Refusals of API client requests are always kept: they show a
	// credential being used outside what it was given.
	if outcome == "denied" && u.Client == nil && !strings.HasPrefix(r.action, "env.") && !strings.HasPrefix(r.action, "admin.") && !strings.HasPrefix(r.action, "support.") &&
		!strings.HasPrefix(r.action, "kb.") && !strings.HasPrefix(r.action, "status.") &&
		r.action != "access.grant" && !strings.HasPrefix(r.action, "account.api_client") {
		return err // only security-relevant refusals are worth keeping
	}
	ev := domain.AuditEvent{Action: r.action, Outcome: outcome}
	ev.ActorID, ev.ActorLabel = strPtr(u.ID), strPtr(u.Email)
	if u.Client != nil {
		// The record names the credential that acted.
		ev.ActorLabel = strPtr(u.Email + " via API client " + u.Client.Name)
	}
	ip := c.IP()
	ev.IP = &ip
	if r.target != nil {
		ev.Target = strPtr(strings.Clone(r.target(c)))
	}
	if t, ok := c.Locals(keyAuditTarget).(string); ok && t != "" {
		ev.Target = strPtr(t) // a handler named what it changed
	}
	botID := strings.Clone(c.Params("id"))
	switch {
	case strings.HasPrefix(c.Route().Path, "/api/v1/bots/:id"), strings.HasPrefix(c.Route().Path, "/api/v1/automation/bots/:id"):
		ev.BotID = &botID
		if b, e := s.bots.Store.GetBot(c.Context(), botID); e == nil {
			ev.BotName = strPtr(b.Name)
		}
	case strings.HasPrefix(c.Route().Path, "/api/v1/sites/:sid"):
		siteID := strings.Clone(c.Params("sid"))
		ev.SiteID = &siteID
		if s.sites != nil {
			if st, e := s.sites.Store.GetSite(c.Context(), siteID); e == nil {
				ev.SiteName = strPtr(st.Name)
			}
		}
	case r.action == "access.invite_accept" && outcome == "ok":
		var inv struct {
			BotID   string `json:"bot_id"`
			BotName string `json:"bot_name"`
		}
		if json.Unmarshal(c.Response().Body(), &inv) == nil && inv.BotID != "" {
			ev.BotID, ev.BotName = &inv.BotID, &inv.BotName
		}
	case (r.action == "bot.create" || r.action == "game.create") && outcome == "ok":
		var created struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(c.Response().Body(), &created) == nil && created.ID != "" {
			ev.BotID, ev.BotName = &created.ID, &created.Name
		}
	case strings.HasPrefix(c.Route().Path, "/api/v1/users/:id"):
		ev.SubjectUserID = &botID // the :id here is a user id
	default:
		ev.SubjectUserID = strPtr(u.ID)
	}
	if r.action == "access.grant" {
		var in struct {
			Email string `json:"email"`
		}
		if json.Unmarshal(c.Body(), &in) == nil {
			ev.Target = strPtr(in.Email)
		}
	}
	s.audit.Record(c.Context(), ev)
	return err
}

// statusOfError mirrors errorHandler's mapping for the audit outcome.
func statusOfError(err error) int {
	var ve *domain.ValidationError
	var be *domain.BusyError
	switch {
	case asErr(err, &ve):
		return fiber.StatusBadRequest
	case asErr(err, &be):
		return fiber.StatusConflict
	case asErr(err, new(*domain.CapacityError)):
		return fiber.StatusConflict
	case isErr(err, domain.ErrNotFound):
		return fiber.StatusNotFound
	case isErr(err, domain.ErrForbidden):
		return fiber.StatusForbidden
	case isErr(err, domain.ErrUnauthorized):
		return fiber.StatusUnauthorized
	case isErr(err, domain.ErrNotStopped), isErr(err, domain.ErrConflict):
		return fiber.StatusConflict
	}
	return 0
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type auditDTO struct {
	ID      int64   `json:"id"`
	AtMS    int64   `json:"at_ms"`
	Actor   *string `json:"actor"`
	BotID   *string `json:"bot_id"`
	BotName *string `json:"bot_name"`
	Action  string  `json:"action"`
	Target  *string `json:"target"`
	Outcome string  `json:"outcome"`
}

func auditPage(evs []domain.AuditEvent, limit int) fiber.Map {
	out := make([]auditDTO, len(evs))
	for i, e := range evs {
		out[i] = auditDTO{e.ID, e.AtMS, e.ActorLabel, e.BotID, e.BotName, e.Action, e.Target, e.Outcome}
	}
	m := fiber.Map{"events": out}
	if len(evs) == limit {
		m["next_before"] = evs[len(evs)-1].ID
	}
	return m
}

func auditQuery(c fiber.Ctx) (before int64, limit int, err error) {
	limit = 50
	if v := c.Query("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > 200 {
			return 0, 0, domain.Invalid("limit must be between 1 and 200")
		}
	}
	if v := c.Query("before"); v != "" {
		if before, err = strconv.ParseInt(v, 10, 64); err != nil || before < 0 {
			return 0, 0, domain.Invalid("before must be an event id")
		}
	}
	return before, limit, nil
}

func (s *panel) botAudit(c fiber.Ctx) error {
	before, limit, err := auditQuery(c)
	if err != nil {
		return err
	}
	evs, err := s.audit.ForBot(c.Context(), currentUser(c), strings.Clone(c.Params("id")), before, limit)
	if err != nil {
		return err
	}
	return c.JSON(auditPage(evs, limit))
}

func (s *panel) visibleAudit(c fiber.Ctx) error {
	before, limit, err := auditQuery(c)
	if err != nil {
		return err
	}
	evs, err := s.audit.Visible(c.Context(), currentUser(c), before, limit)
	if err != nil {
		return err
	}
	return c.JSON(auditPage(evs, limit))
}
