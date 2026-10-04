package blueprint

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveProviders checks the real provider APIs. It needs internet access:
// RIVET_LIVE_PROVIDERS=1 go test ./internal/blueprint -run Live -v
func TestLiveProviders(t *testing.T) {
	if os.Getenv("RIVET_LIVE_PROVIDERS") != "1" {
		t.Skip("set RIVET_LIVE_PROVIDERS=1 to contact the real provider APIs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p := &Providers{}
	for _, c := range []Download{
		{Provider: Provider{Name: "minecraft-vanilla"}, Version: "latest"},
		{Provider: Provider{Name: "papermc", Project: "paper"}, Version: "latest"},
		{Provider: Provider{Name: "papermc", Project: "velocity"}, Version: "latest"},
		{Provider: Provider{Name: "papermc", Project: "folia"}, Version: "latest"},
		{Provider: Provider{Name: "purpur"}, Version: "latest"},
		{Provider: Provider{Name: "fabric"}, Version: "latest"},
		{Provider: Provider{Name: "forge"}, Version: "1.20.1"},
		{Provider: Provider{Name: "neoforge"}, Version: "latest"},
	} {
		vs, err := p.Versions(ctx, c.Name, c.Project)
		if err != nil || len(vs) == 0 {
			t.Errorf("%s/%s versions: %v", c.Name, c.Project, err)
			continue
		}
		a, err := p.Resolve(ctx, c, nil)
		if err != nil || a.URL == "" {
			t.Errorf("%s/%s resolve: %v", c.Name, c.Project, err)
			continue
		}
		t.Logf("%s/%s: %d versions, newest %s -> %s (java %d)", c.Name, c.Project, len(vs), vs[0].ID, a.Version,
			p.JavaFor(ctx, c.Name, c.Project, a.Version))
	}
}
