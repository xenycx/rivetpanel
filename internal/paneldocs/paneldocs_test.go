package paneldocs

import (
	"strings"
	"testing"
)

func TestGuideIsEmbeddedAndSearchable(t *testing.T) {
	if n := len(Pages()); n < 40 {
		t.Fatalf("only %d pages embedded", n)
	}
	cases := map[string]string{
		"custom domain dns txt record verify":          "websites/",
		"deploy a bot from github auto deploy webhook": "apps/deploy-from-github",
		"minecraft jvm memory flags":                   "game-servers/memory-and-jvm",
		"add a remote node agent":                      "operate/remote-nodes",
	}
	for q, want := range cases {
		hits := Search(q, 3)
		ok := false
		for _, h := range hits {
			ok = ok || strings.HasPrefix(h.Page.ID, want)
		}
		if !ok {
			var got []string
			for _, h := range hits {
				got = append(got, h.Page.ID)
			}
			t.Errorf("%q: want %s in %v", q, want, got)
		}
	}
	p, ok := Find("ai")
	if !ok || p.ID != "operate/ai-assistant" || p.URL != "/docs/operate/ai-assistant" {
		t.Fatalf("alias: %+v %v", p.ID, ok)
	}
	if txt := p.Text("run limits"); !strings.Contains(txt, "Wall time") || strings.Contains(txt, "## Web research") {
		t.Fatalf("section text: %s", txt)
	}
	if _, ok := Find("/docs/websites/domains#verify"); !ok {
		t.Fatal("URL form not found")
	}
}
