package modules

import "testing"

func TestParse(t *testing.T) {
	r, err := Parse("agents=preview, game_servers=stable")
	if err != nil {
		t.Fatal(err)
	}
	if got := r["agents"].State; got != Preview {
		t.Fatalf("agents state = %q", got)
	}
	if !r.Enabled("game_servers") || r.Enabled("extensions") {
		t.Fatalf("unexpected enabled modules: %#v", r)
	}
	if len(r.List()) != len(catalog) {
		t.Fatalf("list length = %d, want %d", len(r.List()), len(catalog))
	}
}

func TestParseRejectsInvalidOverrides(t *testing.T) {
	for _, value := range []string{"missing", "unknown=preview", "agents=beta", "agents=off,agents=preview", "core=off"} {
		if _, err := Parse(value); err == nil {
			t.Errorf("Parse(%q) succeeded", value)
		}
	}
}
