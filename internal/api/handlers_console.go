package api

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	fws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/console"
	"github.com/xenycx/rivetpanel/internal/domain"
)

const (
	wsReadLimit     = 8 << 10
	wsIdleTimeout   = 90 * time.Second
	wsWriteTimeout  = 10 * time.Second
	wsPingInterval  = 30 * time.Second
	defaultLogTail  = 200
	maxLogTail      = 1000
	consoleParamsKV = "console.params"
	consoleReleaseK = "console.release"
)

type consoleParams struct {
	bot      domain.Bot
	user     domain.User
	token    string
	since    time.Time
	tail     int
	canStdin bool
}

// consoleGuard runs before the WebSocket upgrade so failures are ordinary HTTP
// responses: 426 (not an upgrade), 403 (foreign origin), 404 (no such bot or not
// yours), 429 (too many connections), 400 (bad parameters).
func (s *panel) consoleGuard(c fiber.Ctx) error {
	if !fws.IsWebSocketUpgrade(c) {
		return fiber.ErrUpgradeRequired
	}
	// Browsers always send Origin on WebSocket handshakes; refuse cross-origin
	// pages even though the cookie is SameSite=Strict (defense in depth).
	if !originOK(c) {
		return fiber.NewError(fiber.StatusForbidden, "cross-origin request rejected")
	}
	user := currentUser(c)
	botID := strings.Clone(c.Params("id"))
	bot, err := s.bots.Authorize(c.Context(), user, botID, domain.PermViewConsole)
	if err != nil {
		return err
	}
	// Input needs the power permission: it can make the bot do things. It is
	// re-checked for every line (see AllowStdin below), not only here.
	p := consoleParams{bot: bot, user: user, token: currentToken(c), tail: defaultLogTail,
		canStdin: c.Query("stdin", "1") != "0"}
	if v := c.Query("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxLogTail {
			return domain.Invalid("tail must be between 0 and " + strconv.Itoa(maxLogTail))
		}
		p.tail = n
	}
	if v := c.Query("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil || t.After(time.Now().Add(time.Minute)) {
			return domain.Invalid("since must be an RFC 3339 timestamp")
		}
		p.since = t
	}
	release, ok := s.consoleLimit.Acquire(user.ID, bot.ID)
	if !ok {
		return fiber.NewError(fiber.StatusTooManyRequests, "too many console connections")
	}
	c.Locals(consoleParamsKV, p)
	c.Locals(consoleReleaseK, release)
	if err := c.Next(); err != nil {
		release() // the upgrade failed; the handler will never run
		return err
	}
	return nil
}

// wsTransport adapts a WebSocket to console.Conn.
type wsTransport struct {
	c  *websocket.Conn
	mu sync.Mutex // one writer at a time
}

func (w *wsTransport) Send(ctx context.Context, m console.Outgoing) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return w.c.WriteJSON(m)
}

func (w *wsTransport) Receive(ctx context.Context) (console.Incoming, error) {
	var in console.Incoming
	_ = w.c.SetReadDeadline(time.Now().Add(wsIdleTimeout))
	err := w.c.ReadJSON(&in)
	return in, err
}

func (s *panel) consoleWS(c *fws.Conn) {
	release, _ := c.Locals(consoleReleaseK).(func())
	if release != nil {
		defer release()
	}
	p, ok := c.Locals(consoleParamsKV).(consoleParams)
	if !ok {
		c.Close()
		return
	}
	defer c.Close()
	c.SetReadLimit(wsReadLimit)
	_ = c.SetReadDeadline(time.Now().Add(wsIdleTimeout))
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(wsIdleTimeout)) })

	ctx, cancel := context.WithCancel(s.baseCtx)
	defer cancel()
	go func() { // keepalive; WriteControl is safe to call concurrently
		t := time.NewTicker(wsPingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)) != nil {
					return
				}
			}
		}
	}()

	refresh := func(ctx context.Context) (domain.Bot, error) {
		// Re-validate the session on every look so logout, expiry, disabling
		// the account and bot deletion all end the stream promptly.
		u, err := s.auth.Authenticate(ctx, p.token)
		if err != nil {
			return domain.Bot{}, err
		}
		return s.bots.Authorize(ctx, u, p.bot.ID, domain.PermViewConsole)
	}
	allowStdin := func(ctx context.Context) error {
		u, err := s.auth.Authenticate(ctx, p.token)
		if err != nil {
			return err
		}
		_, err = s.bots.Authorize(ctx, u, p.bot.ID, domain.PermPower)
		return err
	}
	err := s.console.Run(ctx, &wsTransport{c: c.Conn}, console.Params{
		BotID: p.bot.ID, Refresh: refresh, CanStdin: p.canStdin, AllowStdin: allowStdin, Since: p.since, Tail: p.tail,
	})
	code, text := websocket.CloseNormalClosure, ""
	switch {
	case errors.Is(err, console.ErrSessionEnded):
		code, text = websocket.ClosePolicyViolation, "session ended"
	case websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived):
		code, text = websocket.CloseAbnormalClosure, ""
	}
	if code != websocket.CloseAbnormalClosure {
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
	}
}
