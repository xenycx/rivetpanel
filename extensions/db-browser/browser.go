// Package main is the database browser extension. The panel runs every
// query (in a disposable client container, as the signed-in person); this
// code only validates requests and shapes answers for its page.
package main

import (
	"encoding/json"

	"github.com/xenycx/rivetpanel/extensions/sdk"
)

type queryRequest struct {
	Kind  string          `json:"kind"`
	Query json.RawMessage `json:"query"`
}

func handle(r *sdk.Request) *sdk.Response {
	if r.Kind == "uninstall" {
		return nil
	}
	if r.Kind != "route" {
		return sdk.Error(404, "unknown request")
	}
	switch r.Route {
	case "addons":
		var out any
		if err := sdk.Call("addons.list", map[string]string{"bot_id": r.BotID}, &out); err != nil {
			return hostError(err)
		}
		return sdk.JSON(200, out)
	case "query":
		var in queryRequest
		if err := json.Unmarshal(r.Body, &in); err != nil || in.Kind == "" {
			return sdk.Error(400, "send {kind, query}")
		}
		var q map[string]any
		if err := json.Unmarshal(in.Query, &q); err != nil {
			return sdk.Error(400, "the query must be an object")
		}
		if q["mode"] != "write" {
			q["mode"] = "read"
		}
		var out map[string]any
		if err := sdk.Call("addons.query", map[string]any{"bot_id": r.BotID, "kind": in.Kind, "query": q}, &out); err != nil {
			return hostError(err)
		}
		return sdk.JSON(200, out)
	}
	return sdk.Error(404, "unknown route")
}

// hostError passes the panel's refusal on with a fitting status.
func hostError(err error) *sdk.Response {
	if he, ok := err.(*sdk.HostError); ok {
		switch he.Code {
		case "denied":
			return sdk.Error(403, he.Message)
		case "not_found":
			return sdk.Error(404, he.Message)
		case "limit":
			return sdk.Error(429, he.Message)
		case "unavailable":
			return sdk.Error(503, he.Message)
		}
		return sdk.Error(400, he.Message)
	}
	return sdk.Error(500, err.Error())
}
