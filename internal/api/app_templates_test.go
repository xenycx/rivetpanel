package api

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/apptemplates"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type appTemplatesOut struct {
	Templates []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
		Services  int    `json:"services"`
		Vars      []struct {
			Name     string `json:"name"`
			Secret   bool   `json:"secret"`
			Generate *struct {
				Kind string `json:"kind"`
			} `json:"generate"`
		} `json:"vars"`
	} `json:"templates"`
	Categories []string `json:"categories"`
}

type installOut struct {
	Plan  service.ComposePlan `json:"plan"`
	URL   string              `json:"url"`
	Stack *struct {
		ID         string                 `json:"id"`
		TemplateID string                 `json:"template_id"`
		Compose    string                 `json:"compose"`
		URL        string                 `json:"url"`
		Template   *service.StackTemplate `json:"template"`
		Services   []containerOut         `json:"services"`
	} `json:"stack"`
}

// Every built-in template plans cleanly on a default panel: no errors, no
// variable left unset, and the web service published where the URL says.
func TestAppTemplatesDryRunEveryTemplate(t *testing.T) {
	e := newEnv(t)
	withContainers(t, e, "docker.io", "ghcr.io", "quay.io")
	u := e.user("apps@example.com", domain.RoleUser)

	var list appTemplatesOut
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/app-templates", nil), &list)
	if len(list.Templates) != len(apptemplates.IDs()) || len(list.Categories) == 0 {
		t.Fatalf("listed %d of %d templates", len(list.Templates), len(apptemplates.IDs()))
	}
	for _, tpl := range list.Templates {
		meta, _ := apptemplates.Get(tpl.ID)
		if meta.Web == nil && meta.Console != nil {
			continue // terminal-only apps need a terminal backend (none in this test)
		}
		if !tpl.Available {
			t.Errorf("%s unavailable: %s", tpl.ID, tpl.Reason)
			continue
		}
		var out installOut
		json.Unmarshal(u.mustStatus(200, "POST", "/api/v1/app-templates/"+tpl.ID+"/install", map[string]any{"dry_run": true}), &out)
		if len(out.Plan.Errors) != 0 {
			t.Errorf("%s: plan errors %+v", tpl.ID, out.Plan.Errors)
		}
		for _, w := range out.Plan.Warnings {
			if strings.Contains(w.Message, "not set") || strings.Contains(w.Message, "has no value") {
				t.Errorf("%s: %s", tpl.ID, w.Message)
			}
		}
		if out.Stack != nil {
			t.Fatalf("%s: a dry run created a stack", tpl.ID)
		}
		if meta.Web == nil {
			continue
		}
		pu, err := url.Parse(out.URL)
		if err != nil || pu.Port() == "" {
			t.Fatalf("%s: url %q", tpl.ID, out.URL)
		}
		found := false
		for _, s := range out.Plan.Services {
			for _, p := range s.Ports {
				if s.Service == meta.Web.Service && p.ContainerPort == meta.Web.Port && pu.Port() == itoaPort(p.HostPort) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: web port %d is not published on %s", tpl.ID, meta.Web.Port, out.URL)
		}
	}
}

func itoaPort(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestAppTemplateInstall(t *testing.T) {
	e := newEnv(t)
	withContainers(t, e, "docker.io", "ghcr.io")
	u := e.user("feeds@example.com", domain.RoleUser)

	// Unknown settings and templates are refused.
	u.mustStatus(400, "POST", "/api/v1/app-templates/miniflux/install", map[string]any{"values": map[string]string{"NOPE": "x"}})
	u.mustStatus(404, "POST", "/api/v1/app-templates/no-such-app/install", map[string]any{})
	u.mustStatus(400, "POST", "/api/v1/app-templates/miniflux/install", map[string]any{"address": "site"})

	var out installOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/app-templates/miniflux/install",
		map[string]any{"name": "feeds", "values": map[string]string{"ADMIN_USERNAME": "reader"}}), &out)
	st := out.Stack
	if st == nil || st.TemplateID != "miniflux" || len(st.Services) != 2 || st.Template == nil || st.URL == "" {
		t.Fatalf("installed %+v", st)
	}
	if !strings.Contains(st.Compose, "${ADMIN_PASSWORD}") {
		t.Fatalf("stored compose lost its placeholders:\n%s", st.Compose)
	}
	if len(st.Template.Credentials) != 1 || st.Template.Credentials[0].Name != "ADMIN_PASSWORD" {
		t.Fatalf("credentials %+v", st.Template.Credentials)
	}
	if len(st.Template.Notes) == 0 || !strings.Contains(st.Template.Notes[0], "reader") {
		t.Fatalf("notes %+v", st.Template.Notes)
	}
	cred := st.Template.Credentials[0]
	var rev struct {
		Value string `json:"value"`
	}
	json.Unmarshal(u.mustStatus(200, "POST", "/api/v1/bots/"+cred.BotID+"/env/"+cred.Name+"/reveal", nil), &rev)
	if len(rev.Value) != 20 {
		t.Fatalf("generated password %q", rev.Value)
	}
	raw := string(u.mustStatus(200, "GET", "/api/v1/stacks/"+st.ID, nil))
	if strings.Contains(raw, rev.Value) {
		t.Fatal("the stack view leaks the generated password")
	}
	var app containerOut
	for _, s := range st.Services {
		if strings.HasSuffix(s.Container.Service, "app") {
			app = s
		}
	}
	if app.Container == nil || len(app.Ports) != 1 || app.Ports[0].ContainerPort != 8080 ||
		!strings.HasSuffix(st.URL, ":"+itoaPort(app.Ports[0].HostPort)) {
		t.Fatalf("app %+v url %s", app, st.URL)
	}
	// The stack lists with its template, and deleting it removes everything.
	if !strings.Contains(string(u.mustStatus(200, "GET", "/api/v1/stacks", nil)), `"template_id":"miniflux"`) {
		t.Fatal("stack list lost the template")
	}
	u.mustStatus(204, "DELETE", "/api/v1/stacks/"+st.ID, nil)
}

func TestTrustedCapabilitiesMatch(t *testing.T) {
	a, b := apptemplates.TrustedCaps(), domain.TrustedCapabilities
	if len(a) != len(b) {
		t.Fatalf("%v vs %v", a, b)
	}
	for k := range a {
		if !b[k] {
			t.Fatalf("%s differs", k)
		}
	}
}
