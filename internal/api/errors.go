package api

import (
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func codeFor(status int) string {
	switch status {
	case fiber.StatusBadRequest:
		return "bad_request"
	case fiber.StatusUnauthorized:
		return "unauthorized"
	case fiber.StatusForbidden:
		return "forbidden"
	case fiber.StatusNotFound:
		return "not_found"
	case fiber.StatusConflict:
		return "conflict"
	case fiber.StatusPreconditionFailed:
		return "precondition_failed"
	case fiber.StatusTooManyRequests:
		return "rate_limited"
	case fiber.StatusMethodNotAllowed:
		return "method_not_allowed"
	case fiber.StatusLengthRequired:
		return "length_required"
	case fiber.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case fiber.StatusUpgradeRequired:
		return "upgrade_required"
	case fiber.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case fiber.StatusServiceUnavailable:
		return "unavailable"
	default:
		if status >= 500 {
			return "internal"
		}
		return "error"
	}
}

// errorHandler renders every error as JSON. Internal error text is logged but
// never returned to clients.
func errorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		status := fiber.StatusInternalServerError
		msg := "internal server error"
		var fe *fiber.Error
		var ve *domain.ValidationError
		var be *domain.BusyError
		var ce *domain.CapacityError
		var pe *domain.PermissionError
		switch {
		case errors.As(err, &pe):
			status, msg = fiber.StatusForbidden, pe.Error()
		case errors.As(err, new(*domain.ScopeError)):
			status, msg = fiber.StatusForbidden, err.Error()
		case errors.As(err, &be):
			status, msg = fiber.StatusConflict, be.Error()
		case errors.As(err, &ce):
			status, msg = fiber.StatusConflict, ce.Error()
		case errors.As(err, &ve):
			status, msg = fiber.StatusBadRequest, ve.Msg
		case errors.Is(err, domain.ErrNotFound):
			status, msg = fiber.StatusNotFound, "not found"
		case errors.Is(err, domain.ErrUnauthorized):
			status, msg = fiber.StatusUnauthorized, "authentication required"
		case errors.Is(err, domain.ErrForbidden):
			status, msg = fiber.StatusForbidden, "forbidden"
		case errors.Is(err, domain.ErrRunnerUnavailable):
			status, msg = fiber.StatusServiceUnavailable, "no runner is available for this bot's node"
		case errors.Is(err, domain.ErrNotStopped):
			status, msg = fiber.StatusConflict, "bot must be stopped before its configuration can change"
		case errors.Is(err, domain.ErrConflict):
			status, msg = fiber.StatusConflict, "conflict; reload and retry"
		case errors.As(err, &fe):
			status = fe.Code
			// 503 messages are written by handlers for the user (for example
			// "the server's node is offline"); other 5xx texts stay private.
			if status < 500 || status == fiber.StatusServiceUnavailable {
				msg = fe.Message
			}
		}
		if status >= 500 {
			log.Error("request failed", "method", c.Method(), "path", c.Path(), "status", status, "err", err)
		}
		return c.Status(status).JSON(errorBody{errorDetail{Code: codeFor(status), Message: msg}})
	}
}

func asErr[T error](err error, target *T) bool { return errors.As(err, target) }
func isErr(err, target error) bool             { return errors.Is(err, target) }

// ErrorHandler formats errors exactly like the panel API does (rivet-agent
// uses it so forwarded responses look the same to clients).
func ErrorHandler(log *slog.Logger) fiber.ErrorHandler { return errorHandler(log) }
