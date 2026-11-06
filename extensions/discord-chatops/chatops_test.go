//go:build !wasip1

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/extensions/sdk"
)

type host struct {
	kv     map[string][]byte
	ident  map[string]identity
	power  []map[string]string
	logsAs string
}

func (h *host) call(name string, req json.RawMessage) (any, *sdk.HostError) {
	var m map[string]any
	_ = json.Unmarshal(req, &m)
	switch name {
	case "kv.get":
		v, ok := h.kv[m["scope"].(string)+"/"+m["key"].(string)]
		return map[string]any{"found": ok, "value": v}, nil
	case "kv.set":
		var r struct {
			Scope, Key string
			Value      []byte
		}
		_ = json.Unmarshal(req, &r)
		h.kv[r.Scope+"/"+r.Key] = r.Value
		return nil, nil
	case "kv.delete":
		delete(h.kv, m["scope"].(string)+"/"+m["key"].(string))
		return nil, nil
	case "identity.discord":
		id, ok := h.ident[m["discord_user_id"].(string)]
		if !ok {
			return identity{}, nil
		}
		return id, nil
	case "bots.list":
		if m["as"] != "h1" {
			return nil, &sdk.HostError{Code: "denied", Message: "bad handle"}
		}
		return []bot{{ID: "b-1", Name: "Music", ObservedState: "running"}, {ID: "b-2", Name: "Mod", ObservedState: "stopped"}}, nil
	case "bot.power":
		var r map[string]string
		_ = json.Unmarshal(req, &r)
		h.power = append(h.power, r)
		return bot{ID: r["bot_id"], ObservedState: "starting"}, nil
	case "bot.logs":
		h.logsAs = m["as"].(string)
		return map[string]any{"text": "line1\nline2", "available": true}, nil
	}
	return nil, &sdk.HostError{Code: "denied", Message: name}
}

func newHost(t *testing.T) *host {
	h := &host{kv: map[string][]byte{}, ident: map[string]identity{
		"111111111111111111": {Linked: true, Consented: true, As: "h1", Name: "Ann", Scopes: []string{"status", "logs", "power"}},
		"222222222222222222": {Linked: true, Consented: false},
	}}
	sdk.SetHost(h.call)
	sdk.Handle(handle)
	t.Cleanup(func() { sdk.SetHost(nil) })
	return h
}

var seq int

func send(t *testing.T, user string, typ int, data map[string]any) map[string]any {
	t.Helper()
	seq++
	body, _ := json.Marshal(map[string]any{"id": "9" + strings.Repeat("0", seq%5) + string(rune('a'+seq%26)), "type": typ,
		"member": map[string]any{"user": map[string]string{"id": user}}, "data": data})
	resp := sdk.Invoke(&sdk.Request{Kind: "route", Route: "interactions", Body: body})
	if resp.Status != 200 {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	var out map[string]any
	json.Unmarshal(resp.Body, &out)
	return out
}

func content(r map[string]any) string {
	d, _ := r["data"].(map[string]any)
	s, _ := d["content"].(string)
	return s
}

func TestPingAndIdentityGates(t *testing.T) {
	newHost(t)
	if r := send(t, "", tPing, nil); r["type"] != float64(rPong) {
		t.Fatalf("%v", r)
	}
	if r := send(t, "333333333333333333", tCommand, map[string]any{"name": "status"}); !strings.Contains(content(r), "not linked") {
		t.Fatal(content(r))
	}
	if r := send(t, "222222222222222222", tCommand, map[string]any{"name": "status"}); !strings.Contains(content(r), "Allow Discord ChatOps") {
		t.Fatal(content(r))
	}
	r := send(t, "111111111111111111", tCommand, map[string]any{"name": "status"})
	if !strings.Contains(content(r), "Music") || !strings.Contains(content(r), "Mod") {
		t.Fatal(content(r))
	}
	if d := r["data"].(map[string]any); d["flags"] != float64(ephemeral) {
		t.Fatal("replies must be ephemeral")
	}
}

func TestRestartNeedsConfirmationFromSameUser(t *testing.T) {
	h := newHost(t)
	r := send(t, "111111111111111111", tCommand, map[string]any{"name": "restart", "options": []any{map[string]any{"name": "bot", "value": "b-1"}}})
	if len(h.power) != 0 {
		t.Fatal("acted before confirmation")
	}
	comps := r["data"].(map[string]any)["components"].([]any)[0].(map[string]any)["components"].([]any)
	ok := comps[0].(map[string]any)["custom_id"].(string)

	// Someone else pressing the button is refused.
	h.ident["444444444444444444"] = identity{Linked: true, Consented: true, As: "h1", Scopes: []string{"power"}}
	if r := send(t, "444444444444444444", tComponent, map[string]any{"custom_id": ok}); !strings.Contains(content(r), "Only the person") || len(h.power) != 0 {
		t.Fatal(content(r))
	}
	r = send(t, "111111111111111111", tComponent, map[string]any{"custom_id": ok})
	if len(h.power) != 1 || h.power[0]["action"] != "restart" || h.power[0]["as"] != "h1" || !strings.Contains(content(r), "restarting") {
		t.Fatalf("%v %s", h.power, content(r))
	}
	// A confirmation works once.
	if r := send(t, "111111111111111111", tComponent, map[string]any{"custom_id": ok}); !strings.Contains(content(r), "expired") || len(h.power) != 1 {
		t.Fatal(content(r))
	}
}

func TestScopesAndNames(t *testing.T) {
	h := newHost(t)
	if r := send(t, "111111111111111111", tCommand, map[string]any{"name": "deploy", "options": []any{map[string]any{"name": "bot", "value": "Music"}}}); !strings.Contains(content(r), "did not allow") {
		t.Fatal(content(r))
	}
	r := send(t, "111111111111111111", tCommand, map[string]any{"name": "logs", "options": []any{map[string]any{"name": "bot", "value": "music"}}})
	if !strings.Contains(content(r), "line2") || h.logsAs != "h1" {
		t.Fatal(content(r))
	}
	r = send(t, "111111111111111111", tAutocomplete, map[string]any{"name": "logs", "options": []any{map[string]any{"name": "bot", "value": "mo", "focused": true}}})
	choices := r["data"].(map[string]any)["choices"].([]any)
	if len(choices) != 1 || choices[0].(map[string]any)["value"] != "b-2" {
		t.Fatalf("%v", choices)
	}
}
