package api

import (
	_ "embed"

	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/lazyre"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Automation API: a small, documented surface (docs/openapi.yaml) for scripts
// and CI, authenticated by scoped bearer tokens instead of the session cookie.
// No cookie is read here, so browser CSRF does not apply; every call is also
// authorized as the token's owner, so a token never exceeds the owner's
// current permissions.

const keyAutoToken ctxKey = 100

func currentAutoToken(c fiber.Ctx) domain.AutomationToken {
	return fiber.Locals[domain.AutomationToken](c, keyAutoToken)
}

var requestIDRe = lazyre.New(`^[A-Za-z0-9._-]{8,64}$`)

func (s *panel) automationRoutes(v1 fiber.Router) {
	g := v1.Group("/automation", s.requestID)
	g.Get("/openapi.yaml", s.openAPI)
	a := g.Group("", s.tokenAuth, newLimiter(limiter.Config{
		Max: 120, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return "tok:" + currentAutoToken(c).ID },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "rate limit: 120 requests per minute per token")
		},
	}), s.auditMW, s.idempotency)
	a.Get("/bots", s.autoListBots)
	a.Get("/bots/:id", s.autoScope(service.TokenRead), s.autoGetBot)
	if s.ops != nil {
		a.Get("/bots/:id/operations", s.autoScope(service.TokenRead), s.listBotOperations)
		a.Get("/bots/:id/operations/:op", s.autoScope(service.TokenRead), s.getOperation)
	}
	for _, act := range []string{"start", "stop", "restart"} {
		a.Post("/bots/:id/"+act, s.autoScope(service.TokenPower), s.requirePerm(domain.PermBotsPower), s.autoPower(act))
	}
	if s.deploy != nil {
		a.Post("/bots/:id/deploy", s.autoScope(service.TokenDeploy), s.requirePerm(domain.PermBotsDeploy), s.autoDeploy)
	}
	if s.backups != nil {
		a.Post("/bots/:id/backups", s.autoScope(service.TokenBackup), s.requirePerm(domain.PermBotsBackups), s.autoBackup)
	}
}

// requestID echoes a sane client X-Request-ID or creates one, for correlating
// CI logs with the panel's.
func (s *panel) requestID(c fiber.Ctx) error {
	id := c.Get("X-Request-ID")
	if !requestIDRe.MatchString(id) {
		var b [8]byte
		_, _ = rand.Read(b[:])
		id = hex.EncodeToString(b[:])
	}
	c.Set("X-Request-ID", strings.Clone(id))
	return c.Next()
}

func (s *panel) tokenAuth(c fiber.Ctx) error {
	h := c.Get(fiber.HeaderAuthorization)
	bearer, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		c.Set(fiber.HeaderWWWAuthenticate, `Bearer realm="rivetpanel"`)
		return fiber.NewError(fiber.StatusUnauthorized, "send an automation token as: Authorization: Bearer bpa_...")
	}
	t, u, err := s.tokens.Authenticate(c.Context(), strings.TrimSpace(bearer))
	if err != nil {
		c.Set(fiber.HeaderWWWAuthenticate, `Bearer realm="rivetpanel", error="invalid_token"`)
		return fiber.NewError(fiber.StatusUnauthorized, "the token is unknown, expired or revoked")
	}
	c.Locals(keyUser, u)
	c.Locals(keyAutoToken, t)
	return c.Next()
}

// autoScope checks the token's own scope. A bot outside the token's list is
// reported as not found, like a bot the owner cannot see.
func (s *panel) autoScope(action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		t := currentAutoToken(c)
		if t.BotIDs != nil && !contains(t.BotIDs, c.Params("id")) {
			return domain.ErrNotFound
		}
		if !service.Allows(t, action, c.Params("id")) {
			return fiber.NewError(fiber.StatusForbidden, "this token does not allow "+action)
		}
		return c.Next()
	}
}

// idempotency replays the stored response of a repeated POST that carries the
// same Idempotency-Key, so a retried CI step does not start work twice.
func (s *panel) idempotency(c fiber.Ctx) error {
	key := c.Get("Idempotency-Key")
	if c.Method() != fiber.MethodPost || key == "" {
		return c.Next()
	}
	if len(key) > 64 {
		return domain.Invalid("Idempotency-Key is at most 64 characters")
	}
	full := currentAutoToken(c).ID + "|" + c.Path() + "|" + key
	if e := s.tokens.Idempotent(full); e != nil {
		if !e.Done {
			return fiber.NewError(fiber.StatusConflict, "a request with this Idempotency-Key is still running")
		}
		c.Set("Idempotent-Replayed", "true")
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Status(e.Status).Send(e.Body)
	}
	err := c.Next()
	if err != nil {
		s.tokens.Remember(full, fiber.StatusInternalServerError, nil) // failures release the key
		return err
	}
	s.tokens.Remember(full, c.Response().StatusCode(), c.Response().Body())
	return nil
}

type autoBotDTO struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Runtime            string  `json:"runtime"`
	Phase              string  `json:"phase"`
	DesiredState       string  `json:"desired_state"`
	ObservedState      string  `json:"observed_state"`
	Generation         int64   `json:"generation"`
	ObservedGeneration int64   `json:"observed_generation"`
	LastError          *string `json:"last_error"`
}

func (s *panel) autoBot(c fiber.Ctx, b domain.Bot) autoBotDTO {
	d := s.viewBot(c, b)
	return autoBotDTO{b.ID, b.Name, b.Runtime, d.Phase, b.DesiredState, b.ObservedState, b.Generation, b.ObservedGeneration, b.LastError}
}

func (s *panel) autoListBots(c fiber.Ctx) error {
	t := currentAutoToken(c)
	if !service.Allows(t, service.TokenRead, "") {
		return fiber.NewError(fiber.StatusForbidden, "this token does not allow read")
	}
	bots, err := s.bots.List(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := []autoBotDTO{}
	for _, b := range bots {
		if t.BotIDs == nil || contains(t.BotIDs, b.ID) {
			out = append(out, s.autoBot(c, b))
		}
	}
	return c.JSON(fiber.Map{"bots": out})
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *panel) autoGetBot(c fiber.Ctx) error {
	b, err := s.bots.Get(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(s.autoBot(c, b))
}

func (s *panel) autoPower(action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, u := strings.Clone(c.Params("id")), currentUser(c)
		var b domain.Bot
		var err error
		switch action {
		case "start":
			b, err = s.bots.Start(c.Context(), u, id)
		case "stop":
			b, err = s.bots.Stop(c.Context(), u, id)
		default:
			b, err = s.bots.Restart(c.Context(), u, id)
		}
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusAccepted).JSON(s.autoBot(c, b))
	}
}

func (s *panel) autoDeploy(c fiber.Ctx) error {
	var in struct {
		SHA string `json:"sha"`
	}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	if err := s.deploy.DeployAs(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.SHA, "api"); err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "queued",
		"message": "Follow progress with GET /automation/bots/{id}/operations?kind=deploy,rollback"})
}

func (s *panel) autoBackup(c fiber.Ctx) error {
	var in struct {
		Label string `json:"label"`
	}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	b, err := s.backups.Create(c.Context(), currentUser(c), strings.Clone(c.Params("id")), service.CreateOptions{IncludeEnv: true, Label: in.Label})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(toBackup(b))
}

//go:embed openapi.yaml
var openAPISpec []byte

func (s *panel) openAPI(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "application/yaml; charset=utf-8")
	return c.Send(openAPISpec)
}

// ---- token management (session-authenticated) ----

type tokenDTO struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Prefix       string   `json:"prefix"`
	Actions      []string `json:"actions"`
	BotIDs       []string `json:"bot_ids"`
	CreatedAtMS  int64    `json:"created_at_ms"`
	LastUsedAtMS *int64   `json:"last_used_at_ms"`
	ExpiresAtMS  int64    `json:"expires_at_ms"`
}

func toToken(t domain.AutomationToken) tokenDTO {
	return tokenDTO{t.ID, t.Name, t.Prefix, t.Actions, t.BotIDs, t.CreatedAtMS, t.LastUsedAtMS, t.ExpiresAtMS}
}

func (s *panel) listTokens(c fiber.Ctx) error {
	ts, err := s.tokens.List(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]tokenDTO, len(ts))
	for i, t := range ts {
		out[i] = toToken(t)
	}
	return c.JSON(fiber.Map{"tokens": out, "actions": service.TokenActions})
}

func (s *panel) createToken(c fiber.Ctx) error {
	var in struct {
		Name     string   `json:"name"`
		Actions  []string `json:"actions"`
		BotIDs   []string `json:"bot_ids"`
		LifeDays int      `json:"expires_in_days"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	plain, t, err := s.tokens.Create(c.Context(), currentUser(c), service.TokenInput{Name: in.Name, Actions: in.Actions, BotIDs: in.BotIDs, LifeDays: in.LifeDays})
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"token": plain, "info": toToken(t)})
}

func (s *panel) deleteToken(c fiber.Ctx) error {
	if err := s.tokens.Delete(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
