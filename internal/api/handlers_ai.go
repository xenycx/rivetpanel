package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	fws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func (s *panel) aiRoutes(r fiber.Router) {
	r.Get("/ai/providers", s.aiAvailableProviders)
	r.Get("/admin/ai/providers", s.requirePerm(domain.PermAIManage), s.aiProviders)
	r.Post("/admin/ai/providers", s.requirePerm(domain.PermAIManage), s.aiCreateProvider)
	r.Patch("/admin/ai/providers/:provider", s.requirePerm(domain.PermAIManage), s.aiPatchProvider)
	r.Delete("/admin/ai/providers/:provider", s.requirePerm(domain.PermAIManage), s.aiDeleteProvider)
	r.Post("/admin/ai/providers/:provider/test", s.requirePerm(domain.PermAIManage), s.aiTestProvider)
	r.Get("/admin/ai/providers/:provider/models", s.requirePerm(domain.PermAIManage), s.aiProviderModels)
	r.Get("/admin/ai/search", s.requirePerm(domain.PermAIManage), s.aiSearchSettings)
	r.Put("/admin/ai/search", s.requirePerm(domain.PermAIManage), s.aiPutSearchSettings)
	r.Post("/admin/ai/search/test", s.requirePerm(domain.PermAIManage), s.aiTestSearch)

	ai := s.requirePerm(domain.PermAIUse)
	// One chat for the whole panel. The bot or site a message is about travels
	// with the message, not with the conversation.
	r.Get("/ai/conversations", ai, s.aiMyConversations)
	r.Post("/ai/conversations", ai, s.aiCreateConversation)

	// Per-target conversations predate the global chat and keep working.
	r.Get("/bots/:id/ai/conversations", ai, s.aiListBotConversations)
	r.Post("/bots/:id/ai/conversations", ai, s.aiCreateBotConversation)
	r.Get("/sites/:sid/ai/conversations", ai, s.aiListSiteConversations)
	r.Post("/sites/:sid/ai/conversations", ai, s.aiCreateSiteConversation)
	r.Get("/ai/conversations/:conversation", ai, s.aiConversation)
	r.Patch("/ai/conversations/:conversation", ai, s.aiPatchConversation)
	r.Delete("/ai/conversations/:conversation", ai, s.aiDeleteConversation)
	r.Get("/ai/conversations/:conversation/runs", ai, s.aiConversationRuns)
	r.Post("/ai/conversations/:conversation/messages", ai, s.aiMessage)
	r.Get("/ai/runs/:run", ai, s.aiRun)
	r.Get("/ai/runs/:run/stream", ai, s.aiStreamGuard, fws.New(s.aiStream, fws.Config{ReadBufferSize: 1024, WriteBufferSize: 4096}))
	r.Post("/ai/runs/:run/cancel", ai, s.aiCancel)
	r.Post("/ai/tool-calls/:call/decision", ai, s.aiDecision)
	r.Post("/ai/tool-calls/:call/secure-input", ai, s.aiSecureInput)
	r.Get("/ai/change-sets/:change", ai, s.aiChangeSet)
	r.Post("/ai/change-sets/:change/revert", ai, s.aiRevertChangeSet)
}

func (s *panel) aiAvailableProviders(c fiber.Ctx) error {
	v, e := s.ai.AvailableProviders(c.Context())
	if e != nil {
		return e
	}
	return c.JSON(fiber.Map{"providers": v})
}

func (s *panel) aiProviders(c fiber.Ctx) error {
	v, e := s.ai.Providers(c.Context(), currentUser(c))
	if e != nil {
		return e
	}
	return c.JSON(fiber.Map{"providers": v})
}
func (s *panel) aiCreateProvider(c fiber.Ctx) error {
	var in service.AIProviderInput
	if e := decode(c, &in); e != nil {
		return e
	}
	v, e := s.ai.PutProvider(c.Context(), currentUser(c), "", in)
	if e != nil {
		return e
	}
	return c.Status(fiber.StatusCreated).JSON(v)
}
func (s *panel) aiPatchProvider(c fiber.Ctx) error {
	var in service.AIProviderInput
	if e := decode(c, &in); e != nil {
		return e
	}
	v, e := s.ai.PutProvider(c.Context(), currentUser(c), strings.Clone(c.Params("provider")), in)
	if e != nil {
		return e
	}
	return c.JSON(v)
}
func (s *panel) aiDeleteProvider(c fiber.Ctx) error {
	if e := s.ai.DeleteProvider(c.Context(), currentUser(c), strings.Clone(c.Params("provider"))); e != nil {
		return e
	}
	return c.SendStatus(fiber.StatusNoContent)
}
func (s *panel) aiTestProvider(c fiber.Ctx) error {
	v, e := s.ai.TestProvider(c.Context(), currentUser(c), strings.Clone(c.Params("provider")))
	if e != nil {
		return e
	}
	return c.JSON(v)
}
func (s *panel) aiProviderModels(c fiber.Ctx) error {
	v, e := s.ai.ProviderModels(c.Context(), currentUser(c), strings.Clone(c.Params("provider")))
	if e != nil {
		return e
	}
	return c.JSON(fiber.Map{"models": v})
}
func (s *panel) aiSearchSettings(c fiber.Ctx) error {
	v, e := s.ai.SearchSettings(c.Context(), currentUser(c))
	if e != nil {
		return e
	}
	return c.JSON(v)
}
func (s *panel) aiPutSearchSettings(c fiber.Ctx) error {
	var in service.AISearchInput
	if e := decode(c, &in); e != nil {
		return e
	}
	v, e := s.ai.PutSearchSettings(c.Context(), currentUser(c), in)
	if e != nil {
		return e
	}
	return c.JSON(v)
}
func (s *panel) aiTestSearch(c fiber.Ctx) error {
	v, e := s.ai.TestSearch(c.Context(), currentUser(c))
	if e != nil {
		// Our own refusals keep their status; anything else is the search
		// service or the network, reported as such instead of a bare 500.
		if code := statusOfError(e); code != 0 && code != fiber.StatusInternalServerError {
			return e
		}
		return fiber.NewError(fiber.StatusBadGateway, "search failed: "+e.Error())
	}
	return c.JSON(v)
}

type aiConversationInput struct {
	Title string `json:"title"`
}
type aiPatchConversationInput struct {
	Title      *string `json:"title"`
	ProviderID *string `json:"provider_id"`
	Model      *string `json:"model"`
}
type aiMessageInput struct {
	Content string             `json:"content"`
	Mode    string             `json:"mode"`
	Context *service.AIContext `json:"context"`
}

func conversationJSON(v domain.AIConversation) fiber.Map {
	return fiber.Map{"id": v.ID, "creator_id": v.CreatorID, "bot_id": v.BotID, "site_id": v.SiteID, "title": v.Title, "provider_id": v.ProviderID, "model": v.Model, "created_at_ms": v.CreatedAtMS, "updated_at_ms": v.UpdatedAtMS}
}
func messageJSON(v domain.AIMessage) fiber.Map {
	var citations any = []any{}
	_ = json.Unmarshal([]byte(v.CitationsJSON), &citations)
	var view any = fiber.Map{}
	_ = json.Unmarshal([]byte(v.ContextJSON), &view)
	return fiber.Map{"id": v.ID, "conversation_id": v.ConversationID, "role": v.Role, "content": v.Content, "citations": citations, "context": view, "created_at_ms": v.CreatedAtMS}
}
func runJSON(v domain.AIRun) fiber.Map {
	var limits, plan any
	_ = json.Unmarshal([]byte(v.LimitsJSON), &limits)
	_ = json.Unmarshal([]byte(v.PlanJSON), &plan)
	return fiber.Map{"id": v.ID, "conversation_id": v.ConversationID, "user_id": v.UserID, "bot_id": v.BotID, "site_id": v.SiteID, "provider_id": v.ProviderID, "model": v.Model, "mode": v.Mode, "status": v.Status, "limits": limits, "plan": plan, "auto_approved_at_ms": v.AutoApprovedAtMS, "input_tokens": v.InputTokens, "output_tokens": v.OutputTokens, "error_code": v.ErrorCode, "error_message": v.ErrorMessage, "created_at_ms": v.CreatedAtMS, "started_at_ms": v.StartedAtMS, "finished_at_ms": v.FinishedAtMS}
}
func (s *panel) aiMyConversations(c fiber.Ctx) error {
	v, e := s.ai.MyConversations(c.Context(), currentUser(c))
	if e != nil {
		return e
	}
	out := make([]fiber.Map, len(v))
	for i, x := range v {
		out[i] = conversationJSON(x)
	}
	return c.JSON(fiber.Map{"conversations": out})
}
func (s *panel) aiCreateConversation(c fiber.Ctx) error {
	var in aiConversationInput
	if e := decode(c, &in); e != nil {
		return e
	}
	v, e := s.ai.CreateConversation(c.Context(), currentUser(c), nil, nil, in.Title)
	if e != nil {
		return e
	}
	return c.Status(fiber.StatusCreated).JSON(conversationJSON(v))
}
func (s *panel) aiListBotConversations(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	v, e := s.ai.ListConversations(c.Context(), currentUser(c), &id, nil)
	if e != nil {
		return e
	}
	out := make([]fiber.Map, len(v))
	for i, x := range v {
		out[i] = conversationJSON(x)
	}
	return c.JSON(fiber.Map{"conversations": out})
}
func (s *panel) aiCreateBotConversation(c fiber.Ctx) error {
	var in aiConversationInput
	if e := decode(c, &in); e != nil {
		return e
	}
	id := strings.Clone(c.Params("id"))
	v, e := s.ai.CreateConversation(c.Context(), currentUser(c), &id, nil, in.Title)
	if e != nil {
		return e
	}
	return c.Status(fiber.StatusCreated).JSON(conversationJSON(v))
}
func (s *panel) aiListSiteConversations(c fiber.Ctx) error {
	id := strings.Clone(c.Params("sid"))
	v, e := s.ai.ListConversations(c.Context(), currentUser(c), nil, &id)
	if e != nil {
		return e
	}
	out := make([]fiber.Map, len(v))
	for i, x := range v {
		out[i] = conversationJSON(x)
	}
	return c.JSON(fiber.Map{"conversations": out})
}
func (s *panel) aiCreateSiteConversation(c fiber.Ctx) error {
	var in aiConversationInput
	if e := decode(c, &in); e != nil {
		return e
	}
	id := strings.Clone(c.Params("sid"))
	v, e := s.ai.CreateConversation(c.Context(), currentUser(c), nil, &id, in.Title)
	if e != nil {
		return e
	}
	return c.Status(fiber.StatusCreated).JSON(conversationJSON(v))
}
func (s *panel) aiConversation(c fiber.Ctx) error {
	v, m, e := s.ai.Conversation(c.Context(), currentUser(c), strings.Clone(c.Params("conversation")))
	if e != nil {
		return e
	}
	msgs := make([]fiber.Map, len(m))
	for i, x := range m {
		msgs[i] = messageJSON(x)
	}
	return c.JSON(fiber.Map{"conversation": conversationJSON(v), "messages": msgs})
}

// aiConversationRuns restores the run inspector: the latest runs with their
// tool calls (approvals and secure-input requests included) and change sets.
func (s *panel) aiConversationRuns(c fiber.Ctx) error {
	limit := 5
	if v := c.Query("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 20 {
			return domain.Invalid("limit must be between 1 and 20")
		}
		limit = n
	}
	v, e := s.ai.ConversationRuns(c.Context(), currentUser(c), strings.Clone(c.Params("conversation")), limit)
	if e != nil {
		return e
	}
	out := make([]fiber.Map, len(v))
	for i, d := range v {
		m := runJSON(d.Run)
		m["tool_calls"], m["change_sets"] = d.ToolCalls, d.ChangeSets
		out[i] = m
	}
	return c.JSON(fiber.Map{"runs": out})
}
func (s *panel) aiPatchConversation(c fiber.Ctx) error {
	var in aiPatchConversationInput
	if e := decode(c, &in); e != nil {
		return e
	}
	v, e := s.ai.UpdateConversation(c.Context(), currentUser(c), strings.Clone(c.Params("conversation")), in.Title, in.ProviderID, in.Model)
	if e != nil {
		return e
	}
	return c.JSON(conversationJSON(v))
}
func (s *panel) aiDeleteConversation(c fiber.Ctx) error {
	if e := s.ai.DeleteConversation(c.Context(), currentUser(c), strings.Clone(c.Params("conversation"))); e != nil {
		return e
	}
	return c.SendStatus(fiber.StatusNoContent)
}
func (s *panel) aiMessage(c fiber.Ctx) error {
	var in aiMessageInput
	if e := decode(c, &in); e != nil {
		return e
	}
	r, e := s.ai.StartMessage(c.Context(), currentUser(c), strings.Clone(c.Params("conversation")), in.Content, in.Mode, in.Context)
	if e != nil {
		return e
	}
	return c.Status(fiber.StatusAccepted).JSON(runJSON(r))
}
func (s *panel) aiRun(c fiber.Ctx) error {
	r, e := s.ai.Run(c.Context(), currentUser(c), strings.Clone(c.Params("run")))
	if e != nil {
		return e
	}
	return c.JSON(runJSON(r))
}
func (s *panel) aiCancel(c fiber.Ctx) error {
	if e := s.ai.Cancel(c.Context(), currentUser(c), strings.Clone(c.Params("run"))); e != nil {
		return e
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type aiDecisionInput struct {
	Approve bool `json:"approve"`
}

func (s *panel) aiDecision(c fiber.Ctx) error {
	var in aiDecisionInput
	if e := decode(c, &in); e != nil {
		return e
	}
	if e := s.ai.Decision(c.Context(), currentUser(c), strings.Clone(c.Params("call")), in.Approve); e != nil {
		return e
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type aiSecureInput struct {
	Values map[string]string `json:"values"`
}

func (s *panel) aiSecureInput(c fiber.Ctx) error {
	var in aiSecureInput
	if e := decode(c, &in); e != nil {
		return e
	}
	if e := s.ai.SecureInput(c.Context(), currentUser(c), strings.Clone(c.Params("call")), in.Values); e != nil {
		return e
	}
	names := make([]string, 0, len(in.Values))
	for n := range in.Values {
		names = append(names, n)
	}
	return c.JSON(fiber.Map{"configured": names})
}
func (s *panel) aiChangeSet(c fiber.Ctx) error {
	v, e := s.ai.ChangeSet(c.Context(), currentUser(c), strings.Clone(c.Params("change")))
	if e != nil {
		return e
	}
	files := make([]fiber.Map, 0, len(v.Files))
	for _, f := range v.Files {
		files = append(files, fiber.Map{"path": f.Path, "operation": f.Operation, "before_revision": f.BeforeRevision, "after_revision": f.AfterRevision, "mode": f.Mode, "diff": f.Diff})
	}
	return c.JSON(fiber.Map{"id": v.ID, "run_id": v.RunID, "target_kind": v.TargetKind, "target_id": v.TargetID, "status": v.Status, "summary": v.Summary, "created_at_ms": v.CreatedAtMS, "applied_at_ms": v.AppliedAtMS, "reverted_at_ms": v.RevertedAtMS, "files": files})
}
func (s *panel) aiRevertChangeSet(c fiber.Ctx) error {
	if e := s.ai.RevertChangeSet(c.Context(), currentUser(c), strings.Clone(c.Params("change"))); e != nil {
		return e
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type aiWSParams struct {
	RunID string
	User  domain.User
	After int64
}

const aiWSKey = "ai.ws.params"

func (s *panel) aiStreamGuard(c fiber.Ctx) error {
	if !fws.IsWebSocketUpgrade(c) {
		return fiber.ErrUpgradeRequired
	}
	if !originOK(c) {
		return fiber.NewError(fiber.StatusForbidden, "cross-origin request rejected")
	}
	after, e := strconv.ParseInt(c.Query("after", "0"), 10, 64)
	if e != nil || after < 0 {
		return domain.Invalid("after must be a non-negative sequence")
	}
	id := strings.Clone(c.Params("run"))
	if _, e = s.ai.Run(c.Context(), currentUser(c), id); e != nil {
		return e
	}
	c.Locals(aiWSKey, aiWSParams{id, currentUser(c), after})
	return c.Next()
}
func (s *panel) aiStream(c *fws.Conn) {
	defer c.Close()
	p, ok := c.Locals(aiWSKey).(aiWSParams)
	if !ok {
		return
	}
	old, ch, cancel := s.ai.Subscribe(p.RunID, p.After)
	defer cancel()
	for _, e := range old {
		if c.WriteJSON(e) != nil {
			return
		}
	}
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	authTick := time.NewTicker(20 * time.Second)
	defer authTick.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok || c.WriteJSON(e) != nil {
				return
			}
		case <-authTick.C:
			if _, e := s.ai.Run(context.Background(), p.User, p.RunID); e != nil {
				return
			}
		case <-ping.C:
			_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
		}
	}
}
