package reposcan

import (
	"sort"
	"testing"
)

func snap(files map[string]string) *Snapshot {
	s := &Snapshot{Files: map[string]string{}}
	for p, c := range files {
		s.Files[p] = c
		s.Paths = append(s.Paths, p)
	}
	sort.Strings(s.Paths)
	return s
}

func TestDetectWeb(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string]string
		framework string
		kind      string
	}{
		{"plain html", map[string]string{"index.html": "<p>", "css/style.css": ""}, "static", WebStatic},
		{"vite spa", map[string]string{"package.json": `{"devDependencies":{"vite":"5"},"scripts":{"build":"vite build"}}`}, "vite", WebStaticBuild},
		{"vite + hono api", map[string]string{"package.json": `{"dependencies":{"hono":"4"},"devDependencies":{"vite":"5"},"scripts":{"start":"node server/index.js"}}`}, "node", WebApp},
		{"sveltekit node", map[string]string{"package.json": `{"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}`}, "sveltekit-node", WebApp},
		{"sveltekit static", map[string]string{"package.json": `{"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-static":"3"}}`}, "sveltekit-static", WebStaticBuild},
		{"next", map[string]string{"package.json": `{"dependencies":{"next":"15"}}`}, "next", WebApp},
		{"next export", map[string]string{"package.json": `{"dependencies":{"next":"15"}}`, "next.config.mjs": `export default { output: 'export' }`}, "next-export", WebStaticBuild},
		{"astro", map[string]string{"package.json": `{"dependencies":{"astro":"5"}}`}, "astro", WebStaticBuild},
		{"astro node", map[string]string{"package.json": `{"dependencies":{"astro":"5","@astrojs/node":"9"}}`}, "astro-node", WebApp},
		{"nuxt", map[string]string{"package.json": `{"dependencies":{"nuxt":"3"}}`}, "nuxt", WebApp},
		{"express", map[string]string{"package.json": `{"dependencies":{"express":"4"},"main":"server.js"}`}, "node", WebApp},
		{"fastapi", map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "app.py": ""}, "fastapi", WebApp},
		{"flask", map[string]string{"requirements.txt": "Flask==3\ngunicorn\n"}, "flask", WebApp},
		{"django", map[string]string{"manage.py": `os.environ.setdefault("DJANGO_SETTINGS_MODULE", "mysite.settings")`, "requirements.txt": "django"}, "django", WebApp},
		{"go", map[string]string{"go.mod": "module x"}, "go", WebApp},
		{"rust", map[string]string{"Cargo.toml": "[package]"}, "rust", WebApp},
		{"laravel", map[string]string{"artisan": "", "composer.json": `{"require":{"laravel/framework":"^11"}}`}, "laravel", WebApp},
		{"php", map[string]string{"index.php": "<?php"}, "php", WebApp},
	}
	for _, c := range cases {
		p := DetectWeb(snap(c.files))
		if p.Framework != c.framework || p.Kind != c.kind {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, p.Framework, p.Kind, c.framework, c.kind)
		}
	}
	if p := DetectWeb(snap(map[string]string{"package.json": `{"dependencies":{"express":"4"},"scripts":{"start":"node server/index.js"}}`})); p.Argv[0] != "node" || p.Argv[1] != "server/index.js" {
		t.Errorf("node start script: %v", p.Argv)
	}
	if p := DetectWeb(snap(map[string]string{"manage.py": `os.environ.setdefault("DJANGO_SETTINGS_MODULE", "mysite.settings")`})); p.Argv[len(p.Argv)-1] != "mysite.wsgi" {
		t.Errorf("django wsgi module: %v", p.Argv)
	}
	if p := DetectWeb(snap(map[string]string{"fastapi.txt": "", "requirements.txt": "fastapi", "app.py": ""})); p.Argv[3] != "app:app" {
		t.Errorf("fastapi module: %v", p.Argv)
	}
	// Presets are copies: editing one must not change the catalog.
	p, _ := WebPreset("sveltekit-node")
	p.Env[0].Value = "changed"
	if q, _ := WebPreset("sveltekit-node"); q.Env[0].Value == "changed" {
		t.Error("presets share state")
	}
}
