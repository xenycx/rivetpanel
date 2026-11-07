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

// withContainers rebuilds the env's app with container apps enabled.
func withContainers(t *testing.T, e *env, registries ...string) *service.ContainerService {
	t.Helper()
	if registries == nil {
		registries = []string{"docker.io", "ghcr.io"}
	}
	c := &service.ContainerService{Bots: e.bots, Store: e.db, Policy: service.ContainerPolicy{Registries: registries}}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, SecureCookies: true,
		Nodes: e.db, Containers: c})
	return c
}

type containerOut struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Runtime    string    `json:"runtime"`
	ImageRef   string    `json:"image_ref"`
	Argv       []string  `json:"argv"`
	Generation int64     `json:"generation"`
	Ports      []portDTO `json:"ports"`
	Container  *struct {
		User      string          `json:"user"`
		Writable  []string        `json:"writable"`
		Volumes   []domain.Volume `json:"volumes"`
		StackID   string          `json:"stack_id"`
		Service   string          `json:"service"`
		DependsOn []string        `json:"depends_on"`
	} `json:"container"`
}

func TestContainerAppAPI(t *testing.T) {
	e := newEnv(t)
	withContainers(t, e)
	u := e.user("ops@example.com", domain.RoleUser)

	bad := map[string]any{"name": "web", "image": "registry.evil.example/x:1"}
	if resp, body := u.do("POST", "/api/v1/containers", bad); resp.StatusCode != 400 || !strings.Contains(string(body), "not allowed") {
		t.Fatalf("image from an unlisted registry accepted: %d %s", resp.StatusCode, body)
	}
	for _, in := range []map[string]any{
		{"name": "web", "image": "nginx:1.27", "user": "0:0"},
		{"name": "web", "image": "nginx:1.27", "writable": []string{"/proc/x"}},
		{"name": "web", "image": "nginx:1.27", "volumes": []map[string]string{{"name": "Bad Name", "path": "/data"}}},
		{"name": "web", "image": "nginx:1.27", "volumes": []map[string]string{{"name": "a", "path": "/data"}, {"name": "b", "path": "/data/x"}}},
	} {
		if resp, body := u.do("POST", "/api/v1/containers", in); resp.StatusCode != 400 {
			t.Fatalf("%v accepted: %d %s", in, resp.StatusCode, body)
		}
	}
	create := map[string]any{"name": "web", "image": "nginx:1.27", "writable": []string{"/var/cache/nginx", "/var/run"},
		"volumes": []map[string]string{{"name": "html", "path": "/usr/share/nginx/html"}},
		"ports":   []map[string]any{{"container_port": 80, "host_port": 20080}}, "env": map[string]string{"GREETING": "hi"}}
	var c containerOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/containers", create), &c)
	if c.Kind != "container" || c.Runtime != "image" || c.ImageRef != "docker.io/library/nginx:1.27" || len(c.Argv) != 0 {
		t.Fatalf("created %+v", c)
	}
	if c.Container == nil || len(c.Container.Volumes) != 1 || len(c.Container.Writable) != 2 || len(c.Ports) != 1 || c.Ports[0].HostIP != "127.0.0.1" {
		t.Fatalf("container settings %+v ports %+v", c.Container, c.Ports)
	}
	var got containerOut
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots/"+c.ID, nil), &got)
	if got.Container == nil || got.Container.Volumes[0].Name != "html" {
		t.Fatalf("detail lost settings: %+v", got.Container)
	}
	var list struct {
		Bots []containerOut `json:"bots"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots", nil), &list)
	if len(list.Bots) != 1 || list.Bots[0].Container == nil || len(list.Bots[0].Container.Writable) != 2 {
		t.Fatalf("list lost settings: %+v", list)
	}
	// Changing the image starts a new generation.
	var upd containerOut
	json.Unmarshal(u.mustStatus(200, "PUT", "/api/v1/bots/"+c.ID+"/container", map[string]any{"image": "nginx:1.28", "user": "101:101"}), &upd)
	if upd.ImageRef != "docker.io/library/nginx:1.28" || upd.Generation <= c.Generation || upd.Container.User != "101:101" {
		t.Fatalf("update %+v", upd)
	}
	if resp, _ := u.do("PUT", "/api/v1/bots/"+c.ID+"/container", map[string]any{"image": "quay.io/x/y:1"}); resp.StatusCode != 400 {
		t.Fatalf("update to an unlisted registry accepted: %d", resp.StatusCode)
	}
	// A container app's command is free-form; a runtime change is refused.
	u.mustStatus(200, "PATCH", "/api/v1/bots/"+c.ID, map[string]any{"argv": []string{"nginx", "-g", "daemon off;"}})
	if resp, _ := u.do("PATCH", "/api/v1/bots/"+c.ID, map[string]any{"runtime": "nodejs"}); resp.StatusCode != 400 {
		t.Fatalf("runtime change accepted: %d", resp.StatusCode)
	}
	// Other accounts cannot see it.
	other := e.user("other@example.com", domain.RoleUser)
	if resp, _ := other.do("PUT", "/api/v1/bots/"+c.ID+"/container", map[string]any{"image": "nginx:1"}); resp.StatusCode != 404 {
		t.Fatalf("other account changed it: %d", resp.StatusCode)
	}
}

const testCompose = `
name: blog
services:
  web:
    image: ghcr.io/example/blog:${TAG:-1.0}
    command: ["serve", "--port", "8080"]
    environment:
      DATABASE_URL: postgres://blog:${DB_PASSWORD}@db/blog
    ports: ["8080:8080"]
    depends_on:
      db:
        condition: service_healthy
    volumes:
      - ./uploads:/app/uploads
      - /etc/localtime:/etc/localtime:ro
    healthcheck:
      test: ["CMD", "true"]
  db:
    image: postgres:16
    environment:
      - POSTGRES_PASSWORD=${DB_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
    mem_limit: 256m
  admin:
    image: adminer
    profiles: [debug]
volumes:
  pgdata: {}
`

type stackOut struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Compose  string         `json:"compose"`
	Services []containerOut `json:"services"`
}

func TestComposeImportAPI(t *testing.T) {
	e := newEnv(t)
	withContainers(t, e)
	u := e.user("stack@example.com", domain.RoleUser)

	var dry struct {
		Plan service.ComposePlan `json:"plan"`
	}
	json.Unmarshal(u.mustStatus(200, "POST", "/api/v1/stacks/import", map[string]any{"compose": testCompose, "env": "DB_PASSWORD=s3cret", "dry_run": true}), &dry)
	p := dry.Plan
	if p.Name != "blog" || len(p.Services) != 2 || len(p.Errors) != 0 {
		t.Fatalf("plan %+v", p)
	}
	byName := map[string]service.ComposeServicePlan{}
	for _, s := range p.Services {
		byName[s.Service] = s
	}
	web, db := byName["web"], byName["db"]
	if web.Image != "ghcr.io/example/blog:1.0" || strings.Join(web.Command, " ") != "serve --port 8080" || len(web.DependsOn) != 1 {
		t.Fatalf("web %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0].ContainerPort != 8080 || web.Ports[0].HostPort < 20000 || web.Ports[0].HostIP != "127.0.0.1" {
		t.Fatalf("web ports %+v", web.Ports)
	}
	if len(web.Volumes) != 1 || web.Volumes[0].Name != "uploads" {
		t.Fatalf("web volumes %+v", web.Volumes)
	}
	if db.MemoryBytes != 256<<20 || len(db.Volumes) != 1 || db.Volumes[0].Name != "pgdata" || !containsStr(db.Writable, "/var/run/postgresql") {
		t.Fatalf("db %+v", db)
	}
	notes := ""
	for _, w := range p.Warnings {
		notes += w.Service + ": " + w.Message + "\n"
	}
	for _, want := range []string{"profile", "/etc/localtime", "uploads", "health", "service_healthy"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("warnings miss %q:\n%s", want, notes)
		}
	}

	for _, bad := range []string{
		"services:\n  x:\n    image: nginx\n    privileged: true\n",
		"services:\n  x:\n    image: nginx\n    network_mode: host\n",
		"services:\n  x:\n    image: nginx\n    cap_add: [NET_ADMIN]\n",
		"services:\n  x:\n    image: nginx\n    volumes: [/var/run/docker.sock:/var/run/docker.sock]\n",
		"services:\n  x:\n    build: .\n",
		"services:\n  x:\n    image: evil.example/x\n",
		"services:\n  a:\n    image: nginx\n    depends_on: [b]\n  b:\n    image: nginx\n    depends_on: [a]\n",
	} {
		var r struct {
			Plan service.ComposePlan `json:"plan"`
		}
		json.Unmarshal(u.mustStatus(200, "POST", "/api/v1/stacks/import", map[string]any{"compose": bad}), &r)
		if len(r.Plan.Errors) == 0 || r.Plan.Stack != nil {
			t.Fatalf("accepted:\n%s\n%+v", bad, r.Plan)
		}
	}
	var none struct {
		Bots []containerOut `json:"bots"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots", nil), &none)
	if len(none.Bots) != 0 {
		t.Fatalf("a refused import created apps: %d", len(none.Bots))
	}

	var created struct {
		Stack stackOut `json:"stack"`
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/stacks/import", map[string]any{"compose": testCompose, "env": "DB_PASSWORD=s3cret"}), &created)
	st := created.Stack
	if st.ID == "" || len(st.Services) != 2 || !strings.Contains(st.Compose, "postgres:16") {
		t.Fatalf("stack %+v", st)
	}
	for _, s := range st.Services {
		if s.Container == nil || s.Container.StackID != st.ID || s.Container.Service == "" {
			t.Fatalf("service %+v", s)
		}
	}
	var stacks struct {
		Stacks []stackOut `json:"stacks"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/stacks", nil), &stacks)
	if len(stacks.Stacks) != 1 || stacks.Stacks[0].Compose != "" {
		t.Fatalf("stacks %+v", stacks)
	}
	other := e.user("nosy@example.com", domain.RoleUser)
	other.mustStatus(404, "GET", "/api/v1/stacks/"+st.ID, nil)
	// A service cannot leave its stack's workspace on its own.
	if resp, body := u.do("POST", "/api/v1/bots/"+st.Services[0].ID+"/transfer", map[string]any{"email": "nosy@example.com"}); resp.StatusCode != 400 || !strings.Contains(string(body), "stack") {
		t.Fatalf("stack service transferred alone: %d %s", resp.StatusCode, body)
	}
	u.mustStatus(200, "PATCH", "/api/v1/stacks/"+st.ID, map[string]any{"name": "Blog"})
	u.mustStatus(204, "DELETE", "/api/v1/stacks/"+st.ID, nil)
	u.mustStatus(404, "GET", "/api/v1/stacks/"+st.ID, nil)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
