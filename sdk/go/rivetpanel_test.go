package rivetpanel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlushSendsHeartbeatStatsAndCommands(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bpt_x" || r.URL.Path != "/api/v1/bot-telemetry" {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	t.Setenv("RIVET_URL", srv.URL+"/")
	t.Setenv("RIVET_TELEMETRY_KEY", "bpt_x")
	p := New(func() map[string]float64 { return map[string]float64{"guilds": 3} }, func() bool { return true })
	p.Command("ping")
	p.Command("ping")
	p.Event("guild_join", map[string]string{"id": "1"})
	p.Flush(context.Background())
	if got["ready"] != true || got["stats"].(map[string]any)["guilds"] != 3.0 || len(got["commands"].([]any)) != 1 || len(got["events"].([]any)) != 1 {
		t.Fatalf("payload: %v", got)
	}
}
