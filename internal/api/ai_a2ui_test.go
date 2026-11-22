package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The assistant shows an A2UI surface; a broken one comes back as an error it
// can fix; the person's button press becomes their next message, and only
// for a button this chat really showed.
func TestAIShowUIAndButtonActions(t *testing.T) {
	surface := map[string]any{"messages": []any{
		map[string]any{"version": "1.0", "createSurface": map[string]any{"surfaceId": "plan", "dataModel": map[string]any{"form": map[string]any{"name": "pixel"}}, "components": []any{
			map[string]any{"id": "root", "component": "Card", "child": "col"},
			map[string]any{"id": "col", "component": "Column", "children": []any{"name", "go"}},
			map[string]any{"id": "name", "component": "TextField", "label": "Name", "value": map[string]any{"@path": "/form/name"}},
			map[string]any{"id": "go_l", "component": "Text", "text": "Deploy"},
			map[string]any{"id": "go", "component": "Button", "child": "go_l", "variant": "primary", "action": map[string]any{"event": map[string]any{"name": "deploy", "context": map[string]any{"name": map[string]any{"@path": "/form/name"}}}}},
		}}},
	}}
	broken := map[string]any{"messages": []any{map[string]any{"createSurface": map[string]any{"surfaceId": "plan", "components": []any{map[string]any{"id": "root", "component": "Script"}}}}}}
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("a", "show_ui", broken),
		toolStep("b", "show_ui", surface),
		textStep("Check the plan and press Deploy."),
		textStep("Deploying pixel."),
	}}
	_, _, admin, _, _ := aiEnv(t, fp)
	chat := newChat(t, admin)
	run := sendChat(t, admin, chat, "approval", "deploy my bot", nil)
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run: %v", r)
	}
	if res := lastToolResult(fp, 1); !strings.Contains(res, `unknown component "Script"`) {
		t.Fatalf("broken surface: %q", res)
	}
	if res := lastToolResult(fp, 2); !strings.Contains(res, `Showed surface "plan" (5 components)`) {
		t.Fatalf("surface: %q", res)
	}
	// The system prompt teaches the tool and the tool is offered.
	sys := fp.body(0)["messages"].([]any)[0].(map[string]any)["content"].(string)
	tools, _ := json.Marshal(fp.body(0)["tools"])
	if !strings.Contains(sys, "show_ui") || !strings.Contains(string(tools), `"show_ui"`) {
		t.Fatal("show_ui is not offered")
	}

	// Forged presses are refused.
	path := "/api/v1/ai/conversations/" + chat + "/messages"
	for _, a := range []map[string]any{
		{"name": "delete_bot", "surfaceId": "plan", "sourceComponentId": "go", "context": map[string]any{}},
		{"name": "deploy", "surfaceId": "plan", "sourceComponentId": "name", "context": map[string]any{}},
		{"name": "deploy", "surfaceId": "nope", "sourceComponentId": "go", "context": map[string]any{}},
	} {
		admin.mustStatus(400, "POST", path, map[string]any{"mode": "approval", "action": a})
	}

	var r2 struct{ ID string }
	json.Unmarshal(admin.mustStatus(202, "POST", path, map[string]any{"mode": "approval", "action": map[string]any{"name": "deploy", "surfaceId": "plan", "sourceComponentId": "go", "timestamp": "1999-01-01T00:00:00Z", "context": map[string]any{"name": "pixel-2"}}}), &r2)
	if r := waitRun(t, admin, r2.ID, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("action run: %v", r)
	}
	msgs := fp.body(3)["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)["content"].(string)
	if !strings.Contains(last, `[A2UI action] {"version":"1.0","action":{"name":"deploy","surfaceId":"plan","sourceComponentId":"go"`) || !strings.Contains(last, `"context":{"name":"pixel-2"}`) || !strings.Contains(last, `"label":"Deploy"`) || strings.Contains(last, "1999") {
		t.Fatalf("action message: %q", last)
	}
}
