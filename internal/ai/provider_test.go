package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// strictMessage mirrors a provider that deserializes messages into a typed
// struct with a required content string (DeepSeek-style serde): a missing
// "content" field or a null is a 400, as is an unmatched tool message.
type strictMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCallID string     `json:"tool_call_id"`
	ToolCalls  []ToolCall `json:"tool_calls"`
}

// validateWire applies the rules of the provider formats RivetPanel speaks
// (all OpenAI-compatible Chat Completions):
//   - "openai": content may be null/absent only on an assistant turn with
//     tool_calls; every tool_call id is answered by exactly one tool message
//     before the next non-tool message.
//   - "strict": like openai, plus content must be present and a string on
//     every message (DeepSeek and other serde-based servers).
func validateWire(format string, raw []byte) error {
	var body struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	open := map[string]bool{}
	for i, rm := range body.Messages {
		var probe map[string]json.RawMessage
		_ = json.Unmarshal(rm, &probe)
		var m strictMessage
		if err := json.Unmarshal(rm, &m); err != nil {
			return fmt.Errorf("messages[%d]: %v", i, err)
		}
		c, has := probe["content"]
		if format == "strict" && (!has || string(c) == "null") {
			return fmt.Errorf("messages[%d]: missing field content", i)
		}
		if format == "openai" && (!has || string(c) == "null") && !(m.Role == "assistant" && len(m.ToolCalls) > 0) {
			return fmt.Errorf("messages[%d]: content is required", i)
		}
		if m.Role != "tool" && len(open) > 0 {
			return fmt.Errorf("messages[%d]: tool_call ids without a response", i)
		}
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				if tc.ID == "" {
					return fmt.Errorf("messages[%d]: tool call without id", i)
				}
				open[tc.ID] = true
			}
		case "tool":
			if !open[m.ToolCallID] {
				return fmt.Errorf("messages[%d]: tool message without a matching call", i)
			}
			if m.Content == nil || *m.Content == "" {
				return fmt.Errorf("messages[%d]: empty tool content", i)
			}
			delete(open, m.ToolCallID)
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("tool_call ids without a response at the end")
	}
	return nil
}

func call(id, name, args string) ToolCall {
	return ToolCall{ID: id, Type: "function", Function: ToolCallFunction{Name: name, Arguments: args}}
}

// brokenHistories are the shapes that made providers refuse a request.
func brokenHistories() map[string][]Message {
	return map[string][]Message{
		"tool-only assistant turn": {
			{Role: "system", Content: "sys"}, {Role: "user", Content: "why?"},
			{Role: "assistant", ToolCalls: []ToolCall{call("a", "read_file", `{"path":"package.json"}`)}},
			{Role: "tool", ToolCallID: "a", Content: `["index.js"]`},
		},
		"empty tool output": {
			{Role: "system", Content: "sys"}, {Role: "user", Content: "read it"},
			{Role: "assistant", ToolCalls: []ToolCall{call("a", "read_file", `{"path":"empty.txt"}`)}},
			{Role: "tool", ToolCallID: "a", Content: ""},
		},
		"dangling tool call (interrupted)": {
			{Role: "system", Content: "sys"}, {Role: "user", Content: "go"},
			{Role: "assistant", Content: "Reading.", ToolCalls: []ToolCall{call("a", "read_file", `{}`), call("b", "list_files", `{}`)}},
			{Role: "tool", ToolCallID: "b", Content: "[]"},
			{Role: "user", Content: "still there?"},
		},
		"orphan tool result and empty turns": {
			{Role: "system", Content: "sys"}, {Role: "tool", ToolCallID: "zz", Content: "x"},
			{Role: "user", Content: "  "}, {Role: "assistant", Content: ""}, {Role: "user", Content: "hi"},
		},
		"call without id and bad arguments": {
			{Role: "system", Content: "sys"}, {Role: "user", Content: "go"},
			{Role: "assistant", ToolCalls: []ToolCall{{Function: ToolCallFunction{Name: "list_targets", Arguments: "{"}}}},
			{Role: "tool", Content: "[]"},
		},
	}
}

func TestBrokenHistoriesWereRejected(t *testing.T) {
	// The raw shapes really are invalid for the strict format, so the
	// normalization below is what makes them acceptable.
	for name, h := range brokenHistories() {
		if name == "orphan tool result and empty turns" {
			continue // only invalid by the tool-matching rule
		}
		raw, _ := json.Marshal(map[string]any{"messages": h})
		if strings.Contains(name, "tool-only") {
			var legacy []map[string]any
			for _, m := range h {
				v := map[string]any{"role": m.Role}
				if m.Content != "" { // the old omitempty encoding
					v["content"] = m.Content
				}
				if len(m.ToolCalls) > 0 {
					v["tool_calls"] = m.ToolCalls
				}
				if m.ToolCallID != "" {
					v["tool_call_id"] = m.ToolCallID
				}
				legacy = append(legacy, v)
			}
			raw, _ = json.Marshal(map[string]any{"messages": legacy})
			if err := validateWire("strict", raw); err == nil || !strings.Contains(err.Error(), "messages[2]: missing field content") {
				t.Fatalf("%s: old encoding should reproduce the reported error, got %v", name, err)
			}
		}
	}
}

func TestNormalizedHistoriesSatisfyEveryProviderFormat(t *testing.T) {
	for name, h := range brokenHistories() {
		raw, err := json.Marshal(map[string]any{"messages": NormalizeMessages(h)})
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"openai", "strict"} {
			if err := validateWire(format, raw); err != nil {
				t.Errorf("%s (%s): %v\n%s", name, format, err, raw)
			}
		}
	}
}

func TestNormalizeMessagesDetails(t *testing.T) {
	h := brokenHistories()
	got := NormalizeMessages(h["dangling tool call (interrupted)"])
	// system, user, assistant, tool a (synthesized), tool b, user
	if len(got) != 6 || got[3].ToolCallID != "a" || got[3].Content != MissingToolResult || got[4].ToolCallID != "b" || got[4].Content != "[]" {
		t.Fatalf("dangling call not repaired in order: %+v", got)
	}
	got = NormalizeMessages(h["empty tool output"])
	if got[3].Content != EmptyToolOutput {
		t.Fatalf("empty tool output = %q", got[3].Content)
	}
	got = NormalizeMessages(h["orphan tool result and empty turns"])
	if len(got) != 2 || got[1].Content != "hi" {
		t.Fatalf("orphans and empty turns kept: %+v", got)
	}
	got = NormalizeMessages(h["call without id and bad arguments"])
	if got[2].ToolCalls[0].ID == "" || got[2].ToolCalls[0].Function.Arguments != "{}" || got[3].ToolCallID != got[2].ToolCalls[0].ID || got[3].Content != "[]" {
		t.Fatalf("id/arguments not repaired: %+v", got)
	}
	// The caller's slice is not modified.
	if h["call without id and bad arguments"][2].ToolCalls[0].ID != "" {
		t.Fatal("NormalizeMessages modified its input")
	}
	// Every message carries a content key, also an assistant turn that only
	// calls tools.
	raw, _ := json.Marshal(Message{Role: "assistant", ToolCalls: []ToolCall{call("a", "x", "{}")}})
	if !strings.Contains(string(raw), `"content":""`) {
		t.Fatalf("assistant tool-call turn without content: %s", raw)
	}
}

func TestCompleteSendsAcceptableHistory(t *testing.T) {
	for _, format := range []string{"openai", "strict"} {
		t.Run(format, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if err := validateWire(format, raw); err != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(422)
					fmt.Fprintf(w, `{"error":{"message":%q}}`, "Failed to deserialize the JSON body into the target type: "+err.Error())
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"function\",\"function\":{\"name\":\"list_targets\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			cfg := Config{BaseURL: srv.URL, ChatPath: "/chat/completions", Model: "m"}
			for name, h := range brokenHistories() {
				r, err := (&Client{}).Complete(context.Background(), cfg, h, nil, nil)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				// A provider that streams a call without an id still gets one.
				if len(r.Calls) != 1 || r.Calls[0].ID == "" {
					t.Fatalf("%s: calls = %+v", name, r.Calls)
				}
			}
		})
	}
}
