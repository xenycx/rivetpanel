package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/service"
)

// analyzeWithAI runs POST /github/analyze against the public fake repository
// with the scripted provider steps.
func analyzeWithAI(t *testing.T, steps ...func(http.ResponseWriter)) (service.Analysis, *fakeProvider) {
	t.Helper()
	fp := &fakeProvider{steps: steps}
	e, ai, admin, _, _ := aiEnv(t, fp)
	gh := newPublicGH(t)
	dep := &service.DeployService{Bots: e.bots, OAuth: &service.OAuthService{Store: e.db, Keys: e.bots.Keys}, GH: &github.Client{API: gh.srv.URL},
		Keys: e.bots.Keys, AI: ai, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), PollInterval: time.Hour}
	dep.Files = e.bots.Workspaces.(*filesystem.Manager)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		SecureCookies: true, Deploy: dep, AI: ai, Files: dep.Files})
	var an service.Analysis
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/github/analyze", map[string]any{"repo": "pub/bot", "ai": true}), &an)
	return an, fp
}

const goodPlan = `{"name":"Pub   Bot","runtime":"nodejs","argv":["node","index.js"],"build_command":"","addons":["redis"],
"env":[{"name":"LOG_LEVEL","description":"Log verbosity.","required":false,"secret":false,"value":"info"}],
"memory_bytes":268435456,"nano_cpus":500000000,"pids_limit":0,"ports":[],"summary":"A Node.js bot.","setup":["Copy the token."],
"notes":[],"confidence":"high","evidence":["package.json"]}`

func TestAnalyzeAIFillsEveryField(t *testing.T) {
	an, fp := analyzeWithAI(t, textStep("<think>{not the answer}</think>```json\n"+goodPlan+"\n```"))
	if !an.AI.Used || an.AI.Error != "" || an.Plan.Source != "ai" {
		t.Fatalf("AI not used: %+v", an.AI)
	}
	p := an.Plan
	if p.Name != "Pub Bot" || p.MemoryBytes != 256<<20 || p.NanoCPUs != 500000000 || len(p.Argv) != 2 {
		t.Fatalf("fields not filled: %+v", p)
	}
	var token, level bool
	for _, v := range p.Env {
		switch v.Name {
		case "DISCORD_TOKEN": // detected but left out by the AI: kept, optional
			token = !v.Required && v.Secret
		case "LOG_LEVEL":
			level = v.Value == "info"
		}
	}
	if !token || !level {
		t.Fatalf("variables not merged: %+v", p.Env)
	}
	body := fp.body(0)
	if body["max_tokens"] != float64(16384) {
		t.Fatalf("suggestion kept the provider's small output cap: %v", body["max_tokens"])
	}
	if rf, _ := body["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Fatalf("JSON object not requested: %v", body["response_format"])
	}
}

func TestAnalyzeAIRetriesCutOffAndUnsupportedJSONMode(t *testing.T) {
	cut := func(w http.ResponseWriter) {
		sse(w, `{"choices":[{"delta":{"content":"{\"runtime\":\"nod"},"finish_reason":"length"}]}`)
	}
	reject := func(w http.ResponseWriter) {
		http.Error(w, `{"error":{"message":"response_format is not supported"}}`, 400)
	}
	an, fp := analyzeWithAI(t, reject, cut, textStep(goodPlan))
	if !an.AI.Used || an.Plan.Name != "Pub Bot" {
		t.Fatalf("plan not recovered: %+v %+v", an.AI, an.Plan)
	}
	if _, ok := fp.body(1)["response_format"]; ok {
		t.Fatal("JSON mode kept after the provider rejected it")
	}
	msgs, _ := fp.body(2)["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("the cut-off reply was not followed up: %d messages", len(msgs))
	}
}

func TestAnalyzeAIGivesUpWithReason(t *testing.T) {
	an, _ := analyzeWithAI(t, textStep("I cannot help with that."), textStep("Still no."))
	if an.AI.Used || an.AI.Error != "the AI did not return a plan" || an.Plan.Source != "detected" {
		t.Fatalf("%+v %+v", an.AI, an.Plan.Source)
	}
}
