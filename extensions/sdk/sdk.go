// Package sdk is the guest side of the RivetPanel extension ABI (version 1).
// An extension is a Go program built with GOOS=wasip1 GOARCH=wasm
// -buildmode=c-shared; it registers its handler from an init function:
//
//	func init() { sdk.Handle(func(r *sdk.Request) *sdk.Response { ... }) }
//
// Off wasip1 (go test on the host) the package talks to a fake host that
// tests install with SetHost, so plugin logic is unit-testable.
package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Request is one invocation: a route call, a scheduled job, an event, or a
// lifecycle hook.
type Request struct {
	Kind    string            `json:"kind"` // route | job | event | uninstall
	Route   string            `json:"route,omitempty"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
	BotID   string            `json:"bot_id,omitempty"`
	User    *User             `json:"user,omitempty"`
	Job     string            `json:"job,omitempty"`
	Event   string            `json:"event,omitempty"`
	Data    json.RawMessage   `json:"data,omitempty"`
}

// User is the signed-in person a route runs for.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
}

// Response is what the extension returns. For routes, Status and Body become
// the HTTP response (JSON when ContentType is empty).
type Response struct {
	Status      int             `json:"status"`
	ContentType string          `json:"content_type,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
	Text        string          `json:"text,omitempty"`
}

// JSON builds a 200 response with v encoded as JSON.
func JSON(status int, v any) *Response {
	b, err := json.Marshal(v)
	if err != nil {
		return Error(500, "could not encode the response")
	}
	return &Response{Status: status, Body: b}
}

// Error builds an error response the panel shows as {"error": msg}.
func Error(status int, msg string) *Response {
	return JSON(status, map[string]string{"error": msg})
}

// Handler serves every invocation.
type Handler func(*Request) *Response

var handler Handler

// Handle registers the extension's handler. Call it from init.
func Handle(h Handler) { handler = h }

// HostError is an error the host returned for a capability call.
type HostError struct {
	Code    string `json:"code"` // denied | invalid | not_found | unavailable | limit | failed
	Message string `json:"message"`
}

func (e *HostError) Error() string { return e.Code + ": " + e.Message }

// IsDenied reports whether err is the host refusing a capability.
func IsDenied(err error) bool {
	var he *HostError
	return errors.As(err, &he) && he.Code == "denied"
}

type envelope struct {
	OK    json.RawMessage `json:"ok,omitempty"`
	Error *HostError      `json:"error,omitempty"`
}

// Call invokes a host capability with a JSON request and decodes the reply
// into out (which may be nil).
func Call(name string, in, out any) error {
	req, err := json.Marshal(in)
	if err != nil {
		return err
	}
	raw := hostCall(name, req)
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("host reply for %s: %w", name, err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out != nil && len(env.OK) > 0 {
		return json.Unmarshal(env.OK, out)
	}
	return nil
}

// dispatch decodes a request, runs the handler and encodes its response,
// turning a panic into a 500 so one bad request does not kill the instance.
func dispatch(in []byte) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			out, _ = json.Marshal(Error(500, fmt.Sprint("extension panicked: ", r)))
		}
	}()
	var req Request
	if err := json.Unmarshal(in, &req); err != nil {
		out, _ = json.Marshal(Error(400, "malformed request"))
		return out
	}
	if handler == nil {
		out, _ = json.Marshal(Error(501, "extension registered no handler"))
		return out
	}
	resp := handler(&req)
	if resp == nil {
		resp = &Response{Status: 204}
	}
	out, _ = json.Marshal(resp)
	return out
}
