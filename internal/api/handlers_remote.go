package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/agenthub"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// Headers passed through to and from an agent for file requests.
var (
	forwardRequestHeaders  = []string{"Content-Type", "If-Match", "If-None-Match"}
	forwardResponseHeaders = []string{"Content-Type", "ETag", "Content-Disposition", "Cache-Control", "Content-Security-Policy", "X-Content-Type-Options"}
)

// forwardFiles serves a server's file request on its node: after the same
// authorization as local files (file permission, and the deployment/restore
// lock for changes), a server on a remote node is answered by its agent.
func (s *panel) forwardFiles(c fiber.Ctx) error {
	if s.router == nil || s.router.Hub == nil {
		return c.Next()
	}
	b, err := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermEditFiles)
	if err != nil {
		return err
	}
	if !s.router.Remote(b.NodeID) {
		return c.Next()
	}
	if !isSafeMethod(c.Method()) {
		if err := s.bots.FilesBlocked(b.ID); err != nil {
			return err
		}
	}
	target := "/node/v1" + strings.TrimPrefix(c.Path(), "/api/v1")
	if q := string(c.Request().URI().QueryString()); q != "" {
		target += "?" + q
	}
	req, err := http.NewRequestWithContext(s.baseCtx, c.Method(), target, bodyReader(c))
	if err != nil {
		return err
	}
	for _, h := range forwardRequestHeaders {
		if v := c.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if n := c.Request().Header.ContentLength(); n >= 0 {
		req.ContentLength = int64(n)
	}
	resp, err := s.router.Hub.Forward(b.NodeID, req)
	if err != nil {
		if errors.Is(err, agenthub.ErrOffline) {
			return fiber.NewError(fiber.StatusServiceUnavailable, "the server's node is offline; its files are available again when the agent reconnects")
		}
		return fiber.NewError(fiber.StatusServiceUnavailable, "the server's node did not answer; try again in a moment")
	}
	for _, h := range forwardResponseHeaders {
		if v := resp.Header.Get(h); v != "" {
			c.Set(h, v)
		}
	}
	c.Status(resp.StatusCode)
	return c.SendStream(resp.Body) // closed by fasthttp when the copy ends
}
