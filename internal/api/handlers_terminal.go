package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	fws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Interactive terminals in container apps. The browser sends keystrokes as
// binary frames and control messages ({"type":"resize","cols":..,"rows":..})
// as text frames; the server sends terminal output as binary frames.

const (
	terminalKV          = "terminal.session"
	terminalIdleTimeout = 30 * time.Minute
	terminalReadLimit   = 64 << 10
)

type terminalSession struct {
	sess    service.TerminalSession
	release func()
}

// terminalGuard opens the terminal before the upgrade, so refusals are
// ordinary HTTP errors (and are audited).
func (s *panel) terminalGuard(c fiber.Ctx) error {
	if !fws.IsWebSocketUpgrade(c) {
		return fiber.ErrUpgradeRequired
	}
	if !originOK(c) {
		return fiber.NewError(fiber.StatusForbidden, "cross-origin request rejected")
	}
	user := currentUser(c)
	id := strings.Clone(c.Params("id"))
	var cmd []string
	if v := c.Query("cmd"); v != "" {
		if err := json.Unmarshal([]byte(v), &cmd); err != nil {
			return domain.Invalid("cmd must be a JSON list of strings")
		}
	}
	cols, _ := strconv.Atoi(c.Query("cols"))
	rows, _ := strconv.Atoi(c.Query("rows"))
	cols, rows = min(max(cols, 0), 1000), min(max(rows, 0), 1000)
	release, ok := s.consoleLimit.Acquire(user.ID, id)
	if !ok {
		return fiber.NewError(fiber.StatusTooManyRequests, "too many open terminals")
	}
	sess, bot, err := s.containers.OpenTerminal(c.Context(), user, id, cmd, uint(cols), uint(rows))
	s.auditTerminal(c, user, id, bot, cmd, err)
	if err != nil {
		release()
		return err
	}
	c.Locals(terminalKV, terminalSession{sess: sess, release: release})
	if err := c.Next(); err != nil {
		sess.Close()
		release()
		return err
	}
	return nil
}

func (s *panel) auditTerminal(c fiber.Ctx, u domain.User, id string, b domain.Bot, cmd []string, err error) {
	if s.audit == nil {
		return
	}
	outcome := "ok"
	if err != nil {
		outcome = "failed"
		if code := statusOfError(err); code == fiber.StatusForbidden || code == fiber.StatusNotFound {
			outcome = "denied"
		}
	}
	target := "sh"
	if len(cmd) > 0 {
		target = strings.Join(cmd, " ")
	}
	ip := c.IP()
	ev := domain.AuditEvent{Action: "bot.terminal", Outcome: outcome, ActorID: strPtr(u.ID), ActorLabel: strPtr(u.Email),
		BotID: strPtr(id), Target: strPtr(target), IP: &ip}
	if b.Name != "" {
		ev.BotName = strPtr(b.Name)
	}
	s.audit.Record(c.Context(), ev)
}

func (s *panel) terminalWS(c *fws.Conn) {
	t, ok := c.Locals(terminalKV).(terminalSession)
	if !ok {
		c.Close()
		return
	}
	defer t.release()
	defer t.sess.Close()
	defer c.Close()
	c.SetReadLimit(terminalReadLimit)
	ctx, cancel := context.WithCancel(s.baseCtx)
	defer cancel()
	var mu sync.Mutex // one writer at a time
	write := func(kind int, b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		_ = c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		return c.WriteMessage(kind, b)
	}
	// Terminal output → browser.
	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := t.sess.Read(buf)
			if n > 0 {
				if write(websocket.BinaryMessage, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "the terminal ended"))
				return
			}
		}
	}()
	go func() { // keepalive
		tk := time.NewTicker(wsPingInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)) != nil {
					return
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		t.sess.Close()
		c.Close()
	}()
	// Browser → terminal.
	for {
		_ = c.SetReadDeadline(time.Now().Add(terminalIdleTimeout))
		kind, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		switch kind {
		case websocket.BinaryMessage:
			if _, err := t.sess.Write(msg); err != nil {
				return
			}
		case websocket.TextMessage:
			var ctl struct {
				Type string `json:"type"`
				Cols uint   `json:"cols"`
				Rows uint   `json:"rows"`
			}
			if json.Unmarshal(msg, &ctl) == nil && ctl.Type == "resize" {
				_ = t.sess.Resize(min(ctl.Cols, 1000), min(ctl.Rows, 1000))
			}
		}
	}
}
