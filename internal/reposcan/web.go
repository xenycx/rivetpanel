package reposcan

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Web site kinds.
const (
	WebStatic      = "static"       // files published as they are
	WebStaticBuild = "static_build" // a build whose output directory is published
	WebApp         = "app"          // a server the sites listener proxies to
)

// WebPlan is a suggestion for hosting a repository as a site. Like Plan,
// every field is reviewed in the form and validated again on create.
type WebPlan struct {
	Kind           string   `json:"kind"`
	Framework      string   `json:"framework"` // preset id
	Label          string   `json:"label"`
	Runtime        string   `json:"runtime"`
	BuildCommand   string   `json:"build_command"` // "" = runtime default (apps) or no build (static)
	OutputDir      string   `json:"output_dir"`    // static builds
	Argv           []string `json:"argv"`          // apps
	Port           int      `json:"port"`          // apps
	HealthPath     string   `json:"health_path"`   // apps
	ReleaseCommand string   `json:"release_command"`
	SPA            bool     `json:"spa"`
	Env            []EnvVar `json:"env"`
	Addons         []string `json:"addons"`
	MemoryBytes    int64    `json:"memory_bytes"` // 0 = runtime default
	Summary        string   `json:"summary"`
	Notes          []string `json:"notes"`
	Evidence       []string `json:"evidence"`
}

const (
	npmInstall = "if [ -f package-lock.json ]; then npm ci --include=dev --no-audit --no-fund; else npm install --include=dev --no-audit --no-fund; fi"
	npmBuild   = npmInstall + "\nnpm run build"
	npmApp     = npmBuild + "\nnpm prune --omit=dev --no-audit --no-fund"
	pyPrefix   = "/workspace/.venv/bin/python"
)

// webPresets are the frameworks the panel knows how to host. The first
// entry of each kind is that kind's generic fallback.
var webPresets = []WebPlan{
	{Kind: WebStatic, Framework: "static", Label: "Static files", Runtime: "nodejs", OutputDir: ".",
		Summary: "The repository's files are published as they are; no build runs."},
	{Kind: WebStaticBuild, Framework: "vite", Label: "Vite (React, Vue, Svelte, Solid…)", Runtime: "nodejs", BuildCommand: npmBuild,
		OutputDir: "dist", SPA: true, Summary: "`npm run build` runs and the dist directory is published; unknown paths serve index.html."},
	{Kind: WebStaticBuild, Framework: "sveltekit-static", Label: "SvelteKit (adapter-static)", Runtime: "nodejs", BuildCommand: npmBuild,
		OutputDir: "build", Summary: "`npm run build` prerenders the site and the build directory is published."},
	{Kind: WebStaticBuild, Framework: "astro", Label: "Astro (static)", Runtime: "nodejs", BuildCommand: npmBuild, OutputDir: "dist",
		Summary: "`npm run build` renders the site and the dist directory is published."},
	{Kind: WebStaticBuild, Framework: "next-export", Label: "Next.js (static export)", Runtime: "nodejs", BuildCommand: npmBuild, OutputDir: "out",
		Summary: "`npm run build` exports the site (output: 'export') and the out directory is published."},
	{Kind: WebStaticBuild, Framework: "nuxt-generate", Label: "Nuxt (generate)", Runtime: "nodejs",
		BuildCommand: npmInstall + "\nnpx nuxi generate", OutputDir: ".output/public", Summary: "`nuxi generate` prerenders the site and .output/public is published."},
	{Kind: WebStaticBuild, Framework: "custom-static", Label: "Other static build", Runtime: "nodejs", BuildCommand: npmBuild, OutputDir: "dist",
		Summary: "Your build command runs and the output directory is published."},

	{Kind: WebApp, Framework: "node", Label: "Node.js server (Express, Hono, Fastify…)", Runtime: "nodejs",
		BuildCommand: npmInstall + "\nif grep -q '\"build\"' package.json; then npm run build; fi\nnpm prune --omit=dev --no-audit --no-fund",
		Argv:         []string{"npm", "start"}, Port: 3000, HealthPath: "/",
		Summary: "Dependencies are installed (and `npm run build` runs when there is one); `npm start` serves on $PORT."},
	{Kind: WebApp, Framework: "sveltekit-node", Label: "SvelteKit (adapter-node)", Runtime: "nodejs", BuildCommand: npmApp,
		Argv: []string{"node", "build"}, Port: 3000, HealthPath: "/",
		Env: []EnvVar{
			{Name: "PROTOCOL_HEADER", Value: "x-forwarded-proto"}, {Name: "HOST_HEADER", Value: "x-forwarded-host"},
			{Name: "ADDRESS_HEADER", Value: "x-real-ip"}, {Name: "BODY_SIZE_LIMIT", Value: "25M"},
		},
		Summary: "`npm run build` builds the server and `node build` serves it on $PORT behind the sites listener."},
	{Kind: WebApp, Framework: "next", Label: "Next.js (server)", Runtime: "nodejs", BuildCommand: npmApp,
		Argv: []string{"npm", "start"}, Port: 3000, HealthPath: "/", MemoryBytes: 512 << 20,
		Summary: "`npm run build` builds the app and `npm start` (next start) serves it on $PORT."},
	{Kind: WebApp, Framework: "nuxt", Label: "Nuxt (server)", Runtime: "nodejs", BuildCommand: npmApp,
		Argv: []string{"node", ".output/server/index.mjs"}, Port: 3000, HealthPath: "/",
		Summary: "`npm run build` builds the Nitro server, which serves on $PORT."},
	{Kind: WebApp, Framework: "astro-node", Label: "Astro (Node adapter)", Runtime: "nodejs", BuildCommand: npmApp,
		Argv: []string{"node", "dist/server/entry.mjs"}, Port: 3000, HealthPath: "/",
		Summary: "`npm run build` builds the standalone server, which serves on $HOST:$PORT."},
	{Kind: WebApp, Framework: "fastapi", Label: "FastAPI", Runtime: "python",
		Argv: []string{pyPrefix, "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000", "--proxy-headers", "--forwarded-allow-ips", "*"},
		Port: 8000, HealthPath: "/", Summary: "Requirements are installed into a virtual environment and uvicorn serves the app on port 8000."},
	{Kind: WebApp, Framework: "flask", Label: "Flask", Runtime: "python",
		Argv: []string{pyPrefix, "-m", "gunicorn", "-b", "0.0.0.0:8000", "app:app"}, Port: 8000, HealthPath: "/",
		Summary: "Requirements are installed and gunicorn serves the app on port 8000 (add gunicorn to requirements.txt)."},
	{Kind: WebApp, Framework: "django", Label: "Django", Runtime: "python",
		Argv: []string{pyPrefix, "-m", "gunicorn", "-b", "0.0.0.0:8000", "config.wsgi"}, Port: 8000, HealthPath: "/",
		ReleaseCommand: pyPrefix + " manage.py migrate --noinput",
		Summary:        "Requirements are installed, migrations run before each start, and gunicorn serves the project on port 8000."},
	{Kind: WebApp, Framework: "go", Label: "Go server", Runtime: "go", Argv: []string{"./app"}, Port: 8080, HealthPath: "/",
		Summary: "The module is compiled to a static binary that serves on $PORT (8080)."},
	{Kind: WebApp, Framework: "rust", Label: "Rust server (Axum, Actix, Rocket…)", Runtime: "rust", Argv: []string{"./app"}, Port: 8080, HealthPath: "/",
		Summary: "The crate is built in release mode and the binary serves on $PORT (8080). Bind to 0.0.0.0."},
	{Kind: WebApp, Framework: "laravel", Label: "Laravel", Runtime: "php", Argv: []string{"./.rivet-bin/frankenphp", "php-server", "--listen", ":8080", "--root", "public"},
		Port: 8080, HealthPath: "/up", ReleaseCommand: "php artisan migrate --force",
		Env: []EnvVar{
			{Name: "APP_ENV", Value: "production"}, {Name: "APP_DEBUG", Value: "false"},
			{Name: "APP_KEY", Description: "base64:… from php artisan key:generate --show", Required: true, Secret: true},
			{Name: "LOG_CHANNEL", Value: "stderr"}, {Name: "DB_CONNECTION", Value: "sqlite"}, {Name: "DB_DATABASE", Value: "/data/database.sqlite"},
			{Name: "SESSION_DRIVER", Value: "file"}, {Name: "CACHE_STORE", Value: "file"},
		},
		Summary: "Composer installs the dependencies, migrations run before each start and FrankenPHP serves public/ on port 8080."},
	{Kind: WebApp, Framework: "php", Label: "PHP", Runtime: "php", Argv: []string{"./.rivet-bin/frankenphp", "php-server", "--listen", ":8080", "--root", "."},
		Port: 8080, HealthPath: "/", Summary: "FrankenPHP serves the PHP files on port 8080 (Composer runs when composer.json exists)."},
}

// WebPresets returns every preset, generic ones first within each kind.
func WebPresets() []WebPlan {
	out := make([]WebPlan, len(webPresets))
	for i, p := range webPresets {
		out[i] = clonePlan(p)
	}
	return out
}

// WebPreset returns one preset by framework id.
func WebPreset(id string) (WebPlan, bool) {
	for _, p := range webPresets {
		if p.Framework == id {
			return clonePlan(p), true
		}
	}
	return WebPlan{}, false
}

func clonePlan(p WebPlan) WebPlan {
	p.Argv = append([]string(nil), p.Argv...)
	p.Env = append([]EnvVar(nil), p.Env...)
	p.Addons = append([]string(nil), p.Addons...)
	p.Notes = append([]string(nil), p.Notes...)
	return p
}

type packageJSON struct {
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p packageJSON) has(dep string) bool {
	return p.Dependencies[dep] != "" || p.DevDependencies[dep] != ""
}

var nodeStartRe = regexp.MustCompile(`^node\s+([\w./-]+\.(?:c|m)?js)$`)

// DetectWeb suggests how to host a repository as a site.
func DetectWeb(s *Snapshot) WebPlan {
	plan := func(id string, evidence ...string) WebPlan {
		p, _ := WebPreset(id)
		p.Evidence = evidence
		return p
	}
	switch {
	case s.Has("artisan") && strings.Contains(s.Files["composer.json"], "laravel/framework"):
		return plan("laravel", "artisan", "composer.json")
	case s.Has("composer.json") || s.Has("index.php"):
		p := plan("php", "index.php")
		if s.Has("public/index.php") {
			p.Argv[len(p.Argv)-1] = "public"
		}
		return p
	case s.Has("package.json"):
		return detectWebNode(s, plan)
	case s.Has("manage.py"):
		p := plan("django", "manage.py")
		if m := regexp.MustCompile(`DJANGO_SETTINGS_MODULE['"],\s*['"]([\w]+)\.settings`).FindStringSubmatch(s.Files["manage.py"]); m != nil {
			p.Argv[len(p.Argv)-1] = m[1] + ".wsgi"
		}
		return p
	case pyHas(s, "fastapi"):
		p := plan("fastapi", "requirements.txt")
		if !s.Has("main.py") && s.Has("app.py") {
			p.Argv[3] = "app:app"
		}
		return p
	case pyHas(s, "flask"):
		p := plan("flask", "requirements.txt")
		if !pyHas(s, "gunicorn") {
			p.Notes = append(p.Notes, "Add gunicorn to requirements.txt; the start command uses it.")
		}
		return p
	case s.Has("go.mod"):
		return plan("go", "go.mod")
	case s.Has("Cargo.toml"):
		return plan("rust", "Cargo.toml")
	}
	p := plan("static")
	if !s.Has("index.html") {
		p.Notes = append(p.Notes, "No index.html at the root: check the root directory.")
	}
	return p
}

func pyHas(s *Snapshot, pkg string) bool {
	text := strings.ToLower(s.Files["requirements.txt"] + "\n" + s.Files["pyproject.toml"])
	return regexp.MustCompile(`(?m)(^|["'\s])` + regexp.QuoteMeta(pkg) + `\b`).MatchString(text)
}

func detectWebNode(s *Snapshot, plan func(string, ...string) WebPlan) WebPlan {
	var pkg packageJSON
	_ = json.Unmarshal([]byte(s.Files["package.json"]), &pkg)
	svelteCfg := s.Files["svelte.config.js"] + s.Files["svelte.config.ts"] + s.Files["svelte.config.mjs"]
	nextCfg := s.Files["next.config.js"] + s.Files["next.config.mjs"] + s.Files["next.config.ts"]
	switch {
	case pkg.has("@sveltejs/kit"):
		switch {
		case pkg.has("@sveltejs/adapter-static") || strings.Contains(svelteCfg, "adapter-static"):
			return plan("sveltekit-static", "package.json", "svelte.config.js")
		case pkg.has("@sveltejs/adapter-node") || strings.Contains(svelteCfg, "adapter-node"):
			return plan("sveltekit-node", "package.json", "svelte.config.js")
		}
		p := plan("sveltekit-node", "package.json")
		p.Notes = append(p.Notes, "Switch the project to @sveltejs/adapter-node (or adapter-static) to host it here.")
		return p
	case pkg.has("next"):
		if regexp.MustCompile(`output\s*:\s*['"]export['"]`).MatchString(nextCfg) {
			return plan("next-export", "package.json", "next.config.js")
		}
		return plan("next", "package.json")
	case pkg.has("nuxt"):
		return plan("nuxt", "package.json")
	case pkg.has("astro"):
		if pkg.has("@astrojs/node") {
			return plan("astro-node", "package.json", "astro.config.mjs")
		}
		return plan("astro", "package.json")
	case pkg.has("vite") && !serverDeps(pkg):
		return plan("vite", "package.json")
	}
	if serverDeps(pkg) || pkg.Scripts["start"] != "" {
		p := plan("node", "package.json")
		start := strings.TrimSpace(pkg.Scripts["start"])
		if m := nodeStartRe.FindStringSubmatch(start); m != nil {
			p.Argv = []string{"node", m[1]}
		} else if start == "" && pkg.Main != "" && !strings.HasSuffix(pkg.Main, ".ts") {
			p.Argv = []string{"node", pkg.Main}
		}
		return p
	}
	if pkg.Scripts["build"] != "" {
		p := plan("custom-static", "package.json")
		p.Notes = append(p.Notes, "Check the output directory your build writes to.")
		return p
	}
	return plan("static")
}

// serverDeps reports whether a package depends on a Node.js HTTP framework.
func serverDeps(p packageJSON) bool {
	for _, d := range []string{"express", "hono", "@hono/node-server", "fastify", "koa", "@nestjs/core", "@adonisjs/core", "h3", "polka"} {
		if p.Dependencies[d] != "" {
			return true
		}
	}
	return false
}
