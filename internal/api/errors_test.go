package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// Handler-written 503 messages (for example "the server's node is offline")
// reach the user; other 5xx texts and plain errors stay private.
func TestErrorHandlerShowsUnavailableMessagesOnly(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler(slog.New(slog.NewTextHandler(io.Discard, nil)))})
	app.Get("/offline", func(fiber.Ctx) error {
		return fiber.NewError(fiber.StatusServiceUnavailable, "the server's node is offline")
	})
	app.Get("/gateway", func(fiber.Ctx) error { return fiber.NewError(fiber.StatusBadGateway, "dial tcp 10.0.0.5: refused") })
	app.Get("/plain", func(fiber.Ctx) error { return errors.New("secret detail") })
	for path, want := range map[string]struct {
		status int
		msg    string
	}{
		"/offline": {503, "the server's node is offline"},
		"/gateway": {502, "internal server error"},
		"/plain":   {500, "internal server error"},
	} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		var body errorBody
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want.status || body.Error.Message != want.msg {
			t.Errorf("%s: %d %q, want %d %q", path, resp.StatusCode, body.Error.Message, want.status, want.msg)
		}
	}
}
