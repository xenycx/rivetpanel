package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	fws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/sdk"
)

const keyBotID = "rivetpanel.bot_id"

// botAuth authenticates a bot by its "Authorization: Bearer bpt_..." key.
func (s *panel) botAuth(c fiber.Ctx) error {
	h := c.Get(fiber.HeaderAuthorization)
	key, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		return domain.ErrUnauthorized
	}
	id, err := s.analytics.Authenticate(c.Context(), strings.Clone(key))
	if err != nil {
		return err
	}
	c.Locals(keyBotID, id)
	return c.Next()
}

func decodeTelemetry(b []byte) (service.TelemetryPayload, error) {
	var p service.TelemetryPayload
	if len(b) > service.MaxTelemetryBody {
		return p, fiber.NewError(fiber.StatusRequestEntityTooLarge, "telemetry push too large")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, domain.Invalid("invalid telemetry JSON: expected {ready, identity, stats, commands, events, widgets, unpublish}")
	}
	if _, err := dec.Token(); err != io.EOF {
		return p, domain.Invalid("invalid telemetry JSON")
	}
	return p, nil
}

func (s *panel) deleteBotWidget(c fiber.Ctx) error {
	b, err := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermFullAdmin)
	if err != nil {
		return err
	}
	key := strings.Clone(c.Params("key"))
	if err := s.analytics.DeleteWidgets(c.Context(), b.ID, []string{key}); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// botTelemetryPost ingests one push over HTTP.
func (s *panel) botTelemetryPost(c fiber.Ctx) error {
	id := fiber.Locals[string](c, keyBotID)
	if !s.analytics.Allow(id) {
		return fiber.NewError(fiber.StatusTooManyRequests, "telemetry rate limit: at most 60 pushes per minute")
	}
	p, err := decodeTelemetry(c.Body())
	if err != nil {
		return err
	}
	n, err := s.analytics.Ingest(c.Context(), id, p)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"stored": n})
}

// botTelemetryWS ingests pushes over a long-lived WebSocket; each text frame is
// one payload and is answered with {"stored":n} or {"error":"..."}.
func (s *panel) botTelemetryWS(c *fws.Conn) {
	defer c.Close()
	id, _ := c.Locals(keyBotID).(string)
	c.SetReadLimit(service.MaxTelemetryBody)
	idle := 3 * time.Minute
	for {
		_ = c.SetReadDeadline(time.Now().Add(idle))
		_, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		reply := fiber.Map{}
		if !s.analytics.Allow(id) {
			_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit"), time.Now().Add(time.Second))
			return
		}
		p, derr := decodeTelemetry(msg)
		if derr == nil {
			ctx, cancel := context.WithTimeout(s.baseCtx, 5*time.Second)
			var n int
			n, derr = s.analytics.Ingest(ctx, id, p)
			cancel()
			reply["stored"] = n
		}
		if derr != nil {
			var ve *domain.ValidationError
			msg := "could not store telemetry"
			if errors.As(derr, &ve) {
				msg = ve.Msg
			}
			reply = fiber.Map{"error": msg}
		}
		_ = c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if c.WriteJSON(reply) != nil {
			return
		}
	}
}

var analyticsWindows = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

// botAnalytics returns the dashboard data for a bot the caller can view.
func (s *panel) botAnalytics(c fiber.Ctx) error {
	b, err := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermViewConsole)
	if err != nil {
		return err
	}
	w, ok := analyticsWindows[c.Query("window", "1h")]
	if !ok {
		return domain.Invalid("window must be one of 1h, 6h, 24h, 7d")
	}
	r, err := s.analytics.Report(c.Context(), b.ID, w)
	if err != nil {
		return err
	}
	return c.JSON(r)
}

// rotateTelemetryKey issues a new key (plaintext returned once).
func (s *panel) rotateTelemetryKey(c fiber.Ctx) error {
	key, err := s.bots.RotateTelemetryKey(c.Context(), currentUser(c), strings.Clone(c.Params("id")), s.currentPublicURL())
	if err != nil {
		return err
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"key": key, "url": s.currentPublicURL()})
}

func (s *panel) revokeTelemetryKey(c fiber.Ctx) error {
	if err := s.bots.RevokeTelemetryKey(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// sdkFile serves an embedded telemetry snippet as plain text (never HTML).
func (s *panel) sdkFile(c fiber.Ctx) error {
	name := sdk.Files[c.Params("lang")]
	if name == "" {
		return fiber.ErrNotFound
	}
	b, err := sdk.FS.ReadFile(name)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	return c.Send(b)
}
