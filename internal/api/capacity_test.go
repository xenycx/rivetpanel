package api

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestCapacityBudgets(t *testing.T) {
	e := newEnv(t)
	e.bots.Limits.MaxBotsPerUser = 3
	e.bots.Limits.UserMemoryBytes = 512 << 20
	e.bots.Limits.NodeMemoryBytes = 600 << 20
	u := e.user("u@x.io", domain.RoleUser)
	mk := func(name string, mem int64) (int, string) {
		resp, body := u.do("POST", "/api/v1/bots", map[string]any{"name": name, "runtime": "nodejs", "memory_bytes": mem})
		var out struct{ ID string }
		json.Unmarshal(body, &out)
		return resp.StatusCode, out.ID
	}
	_, a := mk("a", 256<<20)
	_, b := mk("b", 128<<20)
	if code, _ := mk("c", 256<<20); code != 400 {
		t.Fatalf("user memory budget: %d", code)
	}
	_, c := mk("c", 128<<20)
	if code, _ := mk("d", 64<<20); code != 400 {
		t.Fatalf("bot count budget: %d", code)
	}
	// Raising memory past the user's budget is refused.
	u.mustStatus(400, "PATCH", "/api/v1/bots/"+b, map[string]any{"memory_bytes": 256 << 20})

	// A second user fills the node: starts are admitted atomically.
	v := e.user("v@x.io", domain.RoleUser)
	var bigID struct{ ID string }
	json.Unmarshal(v.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "big", "runtime": "nodejs", "memory_bytes": 256 << 20}), &bigID)
	v.mustStatus(202, "POST", "/api/v1/bots/"+bigID.ID+"/start", nil) // 256 of 600 reserved

	// a (256) + b (128) + c (128) = 512 more; only some fit in the 344 left,
	// whatever the interleaving of concurrent requests.
	var wg sync.WaitGroup
	codes := make(chan int, 3)
	for _, id := range []string{a, b, c} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			resp, _ := u.do("POST", "/api/v1/bots/"+id+"/start", nil)
			codes <- resp.StatusCode
		}(id)
	}
	wg.Wait()
	close(codes)
	accepted, refused := 0, 0
	for c := range codes {
		switch c {
		case 202:
			accepted++
		case 409:
			refused++
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if refused == 0 || accepted == 0 {
		t.Fatalf("accepted %d refused %d", accepted, refused)
	}
	_, reserved, _ := e.db.NodeReserved(t.Context(), domain.LocalNodeID)
	if reserved > 600<<20 {
		t.Fatalf("node overcommitted: %d MiB", reserved>>20)
	}
	var cp struct {
		Bots    int `json:"bots"`
		MaxBots int `json:"max_bots"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/me/capacity", nil), &cp)
	if cp.Bots != 3 || cp.MaxBots != 3 {
		t.Fatalf("capacity: %+v", cp)
	}
}
