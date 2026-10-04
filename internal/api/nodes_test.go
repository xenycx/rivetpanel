package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type disconnectRecorder struct{ ids []string }

func (r *disconnectRecorder) Disconnect(id string) { r.ids = append(r.ids, id) }

func seedSamples(e *env, n int) {
	for i := 1; i <= n; i++ {
		e.db.InsertTelemetry(nil2ctx(), domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: int64(i) * 1000, CPUPercent: float64(i),
			LogicalCPUs: 4, MemoryUsedBytes: 1, MemoryTotalBytes: 2, DiskUsedBytes: 1, DiskTotalBytes: 2, RunningBots: i})
	}
}

func TestNodeAndTelemetryEndpointsAreAdminOnly(t *testing.T) {
	e := newEnv(t)
	user := e.user("u@example.com", domain.RoleUser)
	admin := e.user("a@example.com", domain.RoleAdmin)
	for _, p := range []string{"/api/v1/nodes", "/api/v1/nodes/" + domain.LocalNodeID + "/telemetry"} {
		user.mustStatus(403, "GET", p, nil)
		anon := &client{e: e}
		if resp, _ := anon.do("GET", p, nil); resp.StatusCode != 401 {
			t.Fatalf("%s anonymous: %d", p, resp.StatusCode)
		}
	}
	seedSamples(e, 5)
	var nodes struct {
		Nodes []struct {
			ID     string `json:"id"`
			Latest *struct {
				CPU  float64 `json:"cpu_percent"`
				Bots int     `json:"running_bots"`
			} `json:"latest"`
		} `json:"nodes"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/nodes", nil), &nodes)
	if len(nodes.Nodes) != 1 || nodes.Nodes[0].ID != domain.LocalNodeID || nodes.Nodes[0].Latest == nil || nodes.Nodes[0].Latest.Bots != 5 {
		t.Fatalf("%+v", nodes)
	}
}

func TestTelemetryQueryParametersAreValidatedAndBounded(t *testing.T) {
	e := newEnv(t)
	admin := e.user("a@example.com", domain.RoleAdmin)
	seedSamples(e, 10)
	base := "/api/v1/nodes/" + domain.LocalNodeID + "/telemetry"
	var out struct {
		Samples []struct {
			At int64 `json:"sampled_at_ms"`
		} `json:"samples"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", base+"?limit=3&since_ms=2000", nil), &out)
	if len(out.Samples) != 3 || out.Samples[0].At != 8000 || out.Samples[2].At != 10000 {
		t.Fatalf("%+v", out)
	}
	for _, q := range []string{"?limit=0", "?limit=100000", "?limit=x", "?since_ms=-1", "?since_ms=abc"} {
		admin.mustStatus(400, "GET", base+q, nil)
	}
	admin.mustStatus(404, "GET", "/api/v1/nodes/00000000-0000-4000-8000-000000000000/telemetry", nil)
}

func TestAdminManagesLocationsAndAgentNodes(t *testing.T) {
	e := newEnv(t)
	disconnects := &disconnectRecorder{}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots,
		Nodes: e.db, Enrollment: &service.AgentEnrollmentService{Store: e.db}, AgentControl: disconnects,
		PublicURL: "https://panel.example.com", SecureCookies: true})
	admin := e.user("nodes-admin@example.com", domain.RoleAdmin)
	user := e.user("nodes-user@example.com", domain.RoleUser)
	user.mustStatus(403, "POST", "/api/v1/nodes/locations", map[string]string{"name": "Edge"})

	var loc locationDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/nodes/locations", map[string]string{
		"name": "Tbilisi", "description": "Primary edge location",
	}), &loc)
	if loc.ID == "" || loc.Name != "Tbilisi" {
		t.Fatalf("location: %+v", loc)
	}

	var enrolled struct {
		NodeID      string `json:"node_id"`
		Token       string `json:"token"`
		Command     string `json:"command"`
		ExpiresAtMS int64  `json:"expires_at_ms"`
	}
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/nodes/enrollments", map[string]any{
		"name": "edge-one", "location_id": loc.ID, "ttl_minutes": 20,
	}), &enrolled)
	if enrolled.NodeID == "" || !strings.HasPrefix(enrolled.Token, "rvt_enroll_") ||
		!strings.Contains(enrolled.Command, "rivet-agent enroll --panel https://panel.example.com --token "+enrolled.Token) {
		t.Fatalf("enrollment: %+v", enrolled)
	}

	admin.mustStatus(200, "PATCH", "/api/v1/nodes/"+enrolled.NodeID, map[string]any{
		"draining": true, "public_address": "play.example.com", "enabled": false,
	})
	var listed struct {
		Nodes []nodeDTO `json:"nodes"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/nodes", nil), &listed)
	var edge *nodeDTO
	for i := range listed.Nodes {
		if listed.Nodes[i].ID == enrolled.NodeID {
			edge = &listed.Nodes[i]
		}
	}
	if edge == nil || edge.Enabled || !edge.Draining || edge.PublicAddress != "play.example.com" || edge.Agent == nil {
		t.Fatalf("node list: %+v", edge)
	}
	if len(disconnects.ids) != 1 || disconnects.ids[0] != enrolled.NodeID {
		t.Fatalf("disable disconnects: %v", disconnects.ids)
	}

	admin.mustStatus(200, "POST", "/api/v1/nodes/"+enrolled.NodeID+"/revoke-certificates", map[string]string{"reason": "rekey"})
	admin.mustStatus(201, "POST", "/api/v1/nodes/"+enrolled.NodeID+"/enrollment", map[string]int{"ttl_minutes": 15})
	admin.mustStatus(204, "DELETE", "/api/v1/nodes/"+enrolled.NodeID, nil)
	admin.mustStatus(204, "DELETE", "/api/v1/nodes/locations/"+loc.ID, nil)
	if len(disconnects.ids) != 3 {
		t.Fatalf("expected disable, revoke and delete to disconnect the node: %v", disconnects.ids)
	}
	admin.mustStatus(409, "DELETE", "/api/v1/nodes/"+domain.LocalNodeID, nil)
}
