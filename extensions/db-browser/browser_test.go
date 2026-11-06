//go:build !wasip1

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/extensions/sdk"
)

func TestQueryForcesReadUnlessWrite(t *testing.T) {
	var got []map[string]any
	sdk.SetHost(func(name string, req json.RawMessage) (any, *sdk.HostError) {
		var m map[string]any
		json.Unmarshal(req, &m)
		got = append(got, m)
		if name != "addons.query" {
			return nil, &sdk.HostError{Code: "denied", Message: name}
		}
		return map[string]any{"columns": []string{"x"}}, nil
	})
	sdk.Handle(handle)
	defer sdk.SetHost(nil)
	resp := sdk.Invoke(&sdk.Request{Kind: "route", Route: "query", BotID: "b", Body: []byte(`{"kind":"postgres","query":{"mode":"anything","query":"select 1"}}`)})
	if resp.Status != 200 || got[0]["query"].(map[string]any)["mode"] != "read" {
		t.Fatalf("%d %v", resp.Status, got)
	}
	resp = sdk.Invoke(&sdk.Request{Kind: "route", Route: "addons", BotID: "b"})
	if resp.Status != 403 || !strings.Contains(string(resp.Body), "addons.list") {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
}
