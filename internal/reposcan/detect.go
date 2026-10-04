package reposcan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// Plan is a hosting suggestion. Every field is a proposal the person reviews
// in the creation form; the server validates it again on create.
type Plan struct {
	Source       string   `json:"source"`     // recipe | detected | ai
	Confidence   string   `json:"confidence"` // high | medium | low
	Summary      string   `json:"summary"`
	Runtime      string   `json:"runtime"`
	Argv         []string `json:"argv"`          // start command (exec form)
	BuildCommand string   `json:"build_command"` // "" = the runtime's default build
	Env          []EnvVar `json:"env"`
	Addons       []string `json:"addons"`
	MemoryBytes  int64    `json:"memory_bytes"` // 0 = runtime default
	NanoCPUs     int64    `json:"nano_cpus"`    // 0 = runtime default
	PidsLimit    int64    `json:"pids_limit"`   // 0 = runtime default
	Ports        []int    `json:"ports"`        // container ports the app listens on (informational)
	Setup        []string `json:"setup"`        // steps for the person
	Notes        []string `json:"notes"`        // caveats
	Evidence     []string `json:"evidence"`     // files the suggestion is based on
}

// EnvVar is a suggested variable. Value prefills the form (never a secret;
// it may reference add-on variables such as ${POSTGRES_PASSWORD}).
type EnvVar struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Value       string `json:"value"`
}

const mib = 1 << 20

var secretRe = lazyre.New(`(?i)token|secret|key|password|passwd|pass$|auth|credential|webhook|dsn|_uri$|_url$`)

// IsSecretName reports whether a variable name looks like it holds a secret.
func IsSecretName(n string) bool { return secretRe.MatchString(n) }

func (p *Plan) note(format string, a ...any) { p.Notes = append(p.Notes, fmt.Sprintf(format, a...)) }
func (p *Plan) evidence(f string) {
	for _, e := range p.Evidence {
		if e == f {
			return
		}
	}
	p.Evidence = append(p.Evidence, f)
}

func (p *Plan) addAddon(k string) {
	for _, a := range p.Addons {
		if a == k {
			return
		}
	}
	p.Addons = append(p.Addons, k)
}

func (p *Plan) addEnv(v EnvVar) {
	for i, e := range p.Env {
		if e.Name == v.Name {
			if p.Env[i].Description == "" {
				p.Env[i].Description = v.Description
			}
			if v.Value != "" && p.Env[i].Value == "" {
				p.Env[i].Value = v.Value
			}
			p.Env[i].Required = p.Env[i].Required || v.Required
			p.Env[i].Secret = p.Env[i].Secret || v.Secret
			return
		}
	}
	p.Env = append(p.Env, v)
}

// Detect derives a plan from a snapshot without any AI.
func Detect(s *Snapshot, repoName string) Plan {
	p := Plan{Source: "detected", Confidence: "medium"}
	switch {
	case s.Has("package.json"):
		detectNode(s, &p)
	case s.Has("go.mod"):
		detectGo(s, &p, repoName)
	case s.Has("Cargo.toml"):
		p.Runtime = "rust"
		p.evidence("Cargo.toml")
		p.Summary = "A Rust project built with cargo in release mode."
		detectDeps(&p, s.Files["Cargo.toml"], map[string]string{"tokio-postgres": "postgres", "postgres": "postgres", "sqlx": "", "redis": "redis",
			"mongodb": "mongodb", "mysql_async": "mariadb", "deadpool-redis": "redis", "deadpool-postgres": "postgres"})
		if strings.Contains(s.Files["Cargo.toml"], "sqlx") {
			for _, k := range []string{"postgres", "mysql"} {
				if strings.Contains(s.Files["Cargo.toml"], "\""+k+"\"") {
					p.addAddon(map[string]string{"postgres": "postgres", "mysql": "mariadb"}[k])
				}
			}
		}
	case s.Has("requirements.txt") || s.Has("pyproject.toml") || s.Has("setup.py") || s.Has("Pipfile"):
		detectPython(s, &p)
	case s.Has("pom.xml") || s.Has("build.gradle") || s.Has("build.gradle.kts"):
		p.Runtime = "java"
		if s.Has("pom.xml") {
			p.evidence("pom.xml")
			p.Summary = "A Java project built with Maven."
			if !strings.Contains(s.Files["pom.xml"], "<finalName>app</finalName>") {
				p.BuildCommand = "if [ ! -x .maven/bin/mvn ]; then mkdir -p .maven && wget -q -O /tmp/m.tgz https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.9/apache-maven-3.9.9-bin.tar.gz && tar xzf /tmp/m.tgz --strip-components=1 -C .maven; fi\n" +
					".maven/bin/mvn -q -B -DskipTests -Dmaven.repo.local=/workspace/.m2 package\n" +
					"cp \"$(ls -S target/*.jar | grep -v original | head -n1)\" app.jar"
				p.note("The build copies the largest JAR from target/ to app.jar; use a shaded (fat) JAR so dependencies are included.")
			}
		} else {
			p.Summary = "A Java project built with Gradle."
			p.evidence("build.gradle")
			p.BuildCommand = "chmod +x gradlew && ./gradlew --no-daemon -q shadowJar || ./gradlew --no-daemon -q build\ncp \"$(ls -S build/libs/*.jar | head -n1)\" app.jar"
		}
		p.MemoryBytes = 512 * mib
	case s.Has("Gemfile"):
		p.Runtime = "ruby"
		p.evidence("Gemfile")
		p.Summary = "A Ruby project using Bundler."
		for _, f := range []string{"bot.rb", "main.rb", "app.rb", "run.rb"} {
			if s.Has(f) {
				p.Argv = []string{"bundle", "exec", "ruby", f}
				break
			}
		}
		detectDeps(&p, s.Files["Gemfile"], map[string]string{"'pg'": "postgres", "\"pg\"": "postgres", "redis": "redis", "mongo": "mongodb", "mysql2": "mariadb"})
	default:
		p.Confidence = "low"
		p.Summary = "No package manifest was found, so the language could not be determined."
		p.note("Choose the language by hand and set the start command under Startup after creating the bot.")
	}
	detectEnvFiles(s, &p)
	detectCompose(s, &p)
	detectDockerfile(s, &p)
	for name, file := range s.EnvRefs {
		p.addEnv(EnvVar{Name: name, Description: "Read by " + file + ".", Secret: IsSecretName(name), Required: IsSecretName(name)})
	}
	linkAddonEnv(&p)
	sort.SliceStable(p.Env, func(i, j int) bool {
		if p.Env[i].Required != p.Env[j].Required {
			return p.Env[i].Required
		}
		return p.Env[i].Name < p.Env[j].Name
	})
	if len(p.Env) > 60 {
		p.Env = p.Env[:60]
		p.note("Only the first 60 variables are listed.")
	}
	if s.Truncated {
		p.note("The repository is large; only part of it was inspected.")
	}
	if p.Runtime != "" && len(p.Argv) == 0 && p.Runtime != "go" && p.Runtime != "rust" && p.Runtime != "java" {
		p.Confidence = "low"
		p.note("No start file was recognised; the runtime's default start command is used.")
	}
	return p
}

func detectNode(s *Snapshot, p *Plan) {
	p.Runtime = "nodejs"
	p.evidence("package.json")
	var pkg struct {
		Main            string            `json:"main"`
		Type            string            `json:"type"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Engines         map[string]string `json:"engines"`
	}
	_ = json.Unmarshal([]byte(s.Files["package.json"]), &pkg)
	deps := map[string]bool{}
	for d := range pkg.Dependencies {
		deps[d] = true
	}
	ts := pkg.DevDependencies["typescript"] != "" || pkg.Dependencies["typescript"] != "" || s.Has("tsconfig.json")
	start := strings.TrimSpace(pkg.Scripts["start"])
	if m := regexp.MustCompile(`^node\s+([\w./-]+\.(?:c|m)?js)$`).FindStringSubmatch(start); m != nil {
		p.Argv = []string{"node", m[1]}
	} else if start != "" {
		p.Argv = []string{"npm", "start"}
	} else if pkg.Main != "" && !strings.HasSuffix(pkg.Main, ".ts") {
		p.Argv = []string{"node", pkg.Main}
	} else {
		for _, f := range []string{"index.js", "bot.js", "main.js", "app.js", "src/index.js", "src/bot.js", "index.mjs"} {
			if s.Has(f) {
				p.Argv = []string{"node", f}
				break
			}
		}
	}
	lock := "npm install --no-audit --no-fund"
	if s.Has("package-lock.json") {
		lock = "npm ci --no-audit --no-fund"
	}
	switch {
	case ts && pkg.Scripts["build"] != "":
		p.BuildCommand = lock + "\nnpm run build\nnpm prune --omit=dev --no-audit --no-fund"
		p.Summary = "A TypeScript Node.js project: dependencies are installed, `npm run build` compiles it, then development packages are pruned."
	case s.Has("yarn.lock") && !s.Has("package-lock.json"):
		p.BuildCommand = "corepack enable --install-directory /tmp/bin 2>/dev/null || true\nPATH=/tmp/bin:$PATH yarn install --production --frozen-lockfile || npm install --omit=dev --no-audit --no-fund"
		p.Summary = "A Node.js project using Yarn."
	case s.Has("pnpm-lock.yaml"):
		p.BuildCommand = "npx --yes pnpm@9 install --prod --frozen-lockfile"
		p.Summary = "A Node.js project using pnpm."
	default:
		p.Summary = "A Node.js project; dependencies are installed with npm."
	}
	if strings.Contains(start, "ts-node") || strings.Contains(start, "tsx ") {
		p.note("The start script runs TypeScript directly (%s); prefer compiling with `npm run build` and starting the output.", start)
	}
	detectDeps(p, s.Files["package.json"], map[string]string{"\"pg\"": "postgres", "\"postgres\"": "postgres", "\"ioredis\"": "redis",
		"\"redis\"": "redis", "\"mongoose\"": "mongodb", "\"mongodb\"": "mongodb", "\"mysql2\"": "mariadb", "\"mysql\"": "mariadb"})
	if pr := s.Files["prisma/schema.prisma"]; pr != "" {
		p.evidence("prisma/schema.prisma")
		switch {
		case strings.Contains(pr, `"postgresql"`):
			p.addAddon("postgres")
		case strings.Contains(pr, `"mysql"`):
			p.addAddon("mariadb")
		case strings.Contains(pr, `"mongodb"`):
			p.addAddon("mongodb")
		}
		p.note("Prisma projects usually need `npx prisma generate` (and migrations) in the build command.")
	}
	if deps["discord.js"] || deps["eris"] || deps["oceanic.js"] {
		p.Setup = append(p.Setup, "Create the application and bot user in the Discord Developer Portal and copy its token.")
	}
}

func detectPython(s *Snapshot, p *Plan) {
	p.Runtime = "python"
	text := ""
	for _, f := range []string{"requirements.txt", "pyproject.toml", "setup.py", "setup.cfg", "Pipfile"} {
		if c, ok := s.Files[f]; ok {
			p.evidence(f)
			text += strings.ToLower(c) + "\n"
		}
	}
	p.Summary = "A Python project; dependencies are installed into a virtual environment (.venv)."
	if !s.Has("requirements.txt") {
		if s.Has("Pipfile") && !s.Has("pyproject.toml") && !s.Has("setup.py") {
			p.BuildCommand = "python -m venv .venv\n.venv/bin/pip install --no-cache-dir pipenv\n.venv/bin/pipenv requirements > /tmp/req.txt\n.venv/bin/pip install --no-cache-dir -r /tmp/req.txt"
		} else if s.Has("pyproject.toml") && strings.Contains(s.Files["pyproject.toml"], "[tool.poetry") && !strings.Contains(s.Files["pyproject.toml"], "packages") {
			p.BuildCommand = "python -m venv .venv\n.venv/bin/pip install --no-cache-dir poetry-plugin-export poetry\n.venv/bin/poetry export --without-hashes -f requirements.txt -o /tmp/req.txt\n.venv/bin/pip install --no-cache-dir -r /tmp/req.txt"
		}
	}
	py := "/workspace/.venv/bin/python"
	for _, f := range []string{"main.py", "bot.py", "app.py", "run.py", "launcher.py", "src/main.py", "src/bot.py"} {
		if s.Has(f) {
			p.Argv = []string{py, f}
			break
		}
	}
	if len(p.Argv) == 0 {
		// A package with __main__.py, or a [project.scripts] entry point.
		for _, f := range s.Paths {
			if strings.Count(f, "/") == 1 && strings.HasSuffix(f, "/__main__.py") {
				p.Argv = []string{py, "-m", path.Dir(f)}
				break
			}
		}
	}
	if len(p.Argv) == 0 {
		if m := regexp.MustCompile(`(?m)^\[project\.scripts\]\s*\n\s*[\w.-]+\s*=\s*"([\w.]+):(\w+)"`).FindStringSubmatch(s.Files["pyproject.toml"]); m != nil {
			p.Argv = []string{py, "-c", "import " + m[1] + " as m; m." + m[2] + "()"}
		}
	}
	detectDeps(p, text, map[string]string{"psycopg": "postgres", "asyncpg": "postgres", "psycopg2": "postgres", "redis": "redis",
		"aioredis": "redis", "pymongo": "mongodb", "motor": "mongodb", "aiomysql": "mariadb", "pymysql": "mariadb", "mysqlclient": "mariadb"})
	if strings.Contains(text, "discord.py") || strings.Contains(text, "py-cord") || strings.Contains(text, "nextcord") || strings.Contains(text, "disnake") {
		p.Setup = append(p.Setup, "Create the application and bot user in the Discord Developer Portal and copy its token.")
	}
	if strings.Contains(text, "numpy") || strings.Contains(text, "pillow") || strings.Contains(text, "lxml") {
		p.note("Some dependencies may compile native code; the Python image is Alpine-based (musl), which most wheels support.")
	}
}

func detectGo(s *Snapshot, p *Plan, repoName string) {
	p.Runtime = "go"
	p.evidence("go.mod")
	p.Summary = "A Go module compiled to a static binary named app."
	root := false
	var others []string
	for _, d := range s.MainPackages {
		if d == "." {
			root = true
		} else {
			others = append(others, d)
		}
	}
	if !root && len(others) > 0 {
		pick := pickMain(others, strings.ToLower(repoName))
		p.BuildCommand = "CGO_ENABLED=0 go build -buildvcs=false -p 2 -o /workspace/app ./" + pick
		p.evidence(pick)
		if len(others) > 1 {
			p.note("Several programs were found (%s); %s was chosen.", strings.Join(others, ", "), pick)
		}
	}
	p.Argv = []string{"./app"}
	detectDeps(p, s.Files["go.mod"], map[string]string{"lib/pq": "postgres", "jackc/pgx": "postgres", "go-redis/redis": "redis",
		"redis/go-redis": "redis", "gomodule/redigo": "redis", "mediocregopher/radix": "redis", "mongo-driver": "mongodb",
		"go-sql-driver/mysql": "mariadb"})
}

// pickMain prefers cmd/<repo>, then <...>/<repo>, then the first cmd/*.
func pickMain(dirs []string, repo string) string {
	for _, d := range dirs {
		if d == "cmd/"+repo {
			return d
		}
	}
	for _, d := range dirs {
		if strings.ToLower(path.Base(d)) == repo {
			return d
		}
	}
	for _, d := range dirs {
		if strings.HasPrefix(d, "cmd/") {
			return d
		}
	}
	return dirs[0]
}

func detectDeps(p *Plan, text string, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if m[k] != "" && strings.Contains(text, k) {
			p.addAddon(m[k])
		}
	}
}

var envLineRe = lazyre.New(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// detectEnvFiles reads .env.example-style files: NAME=value with preceding
// comment lines as the description.
func detectEnvFiles(s *Snapshot, p *Plan) {
	files := make([]string, 0)
	for f := range s.Files {
		b := strings.ToLower(path.Base(f))
		if strings.Contains(b, "env") && (strings.HasPrefix(b, ".env") || strings.HasSuffix(b, ".env") || strings.HasPrefix(b, "env.")) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		p.evidence(f)
		var comment []string
		sc := bufio.NewScanner(strings.NewReader(s.Files[f]))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "#") {
				c := strings.TrimSpace(strings.TrimLeft(line, "#"))
				if envLineRe.MatchString(c) || c == "" {
					comment = nil // a commented-out variable, not a description
					continue
				}
				comment = append(comment, c)
				continue
			}
			m := envLineRe.FindStringSubmatch(line)
			if m == nil {
				comment = nil
				continue
			}
			name, val := m[1], strings.Trim(strings.TrimSpace(m[2]), `"'`)
			if strings.ToUpper(name) != name || ignoredEnv[name] {
				comment = nil
				continue
			}
			secret := IsSecretName(name)
			v := EnvVar{Name: name, Description: strings.Join(comment, " "), Secret: secret, Required: secret}
			// Example values are only kept when they look like real defaults.
			if !secret && val != "" && !placeholder(val) {
				v.Value = val
			}
			if len(v.Description) > 300 {
				v.Description = v.Description[:300]
			}
			p.addEnv(v)
			comment = nil
		}
	}
}

func placeholder(v string) bool {
	l := strings.ToLower(v)
	for _, w := range []string{"your", "insert", "here", "example", "xxx", "<", "changeme", "replace", "token", "secret", "id here", "...", "todo"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// detectCompose reads docker-compose files: database images become add-ons
// and the bot service's environment becomes variables.
func detectCompose(s *Snapshot, p *Plan) {
	for f, c := range s.Files {
		b := strings.ToLower(path.Base(f))
		if !strings.Contains(b, "compose") {
			continue
		}
		var doc struct {
			Services map[string]struct {
				Image       string `yaml:"image"`
				Environment any    `yaml:"environment"`
				Ports       []any  `yaml:"ports"`
			} `yaml:"services"`
		}
		if yaml.Unmarshal([]byte(c), &doc) != nil {
			continue
		}
		p.evidence(f)
		for _, svc := range doc.Services {
			img := strings.ToLower(svc.Image)
			switch {
			case strings.Contains(img, "postgres") || strings.Contains(img, "postgis"):
				p.addAddon("postgres")
			case strings.Contains(img, "redis") || strings.Contains(img, "valkey") || strings.Contains(img, "keydb"):
				p.addAddon("redis")
			case strings.Contains(img, "mongo"):
				p.addAddon("mongodb")
			case strings.Contains(img, "mariadb") || strings.Contains(img, "mysql"):
				p.addAddon("mariadb")
			case strings.Contains(img, "lavalink"):
				p.note("The compose file runs Lavalink (music); RivetPanel has no Lavalink add-on yet, so point the bot at an external Lavalink server.")
			}
		}
	}
}

var cmdRe = lazyre.New(`(?im)^\s*(?:CMD|ENTRYPOINT)\s+(.+)$`)

func detectDockerfile(s *Snapshot, p *Plan) {
	for f, c := range s.Files {
		if strings.ToLower(path.Base(f)) != "dockerfile" {
			continue
		}
		p.evidence(f)
		if m := cmdRe.FindAllStringSubmatch(c, -1); len(m) > 0 {
			p.note("%s starts the app with: %s", f, strings.TrimSpace(m[len(m)-1][1]))
		}
		for _, port := range regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d+)`).FindAllStringSubmatch(c, -1) {
			var n int
			fmt.Sscan(port[1], &n)
			if n > 0 && n < 65536 {
				p.Ports = append(p.Ports, n)
			}
		}
	}
}

// addonVars maps conventional variable names to add-on variables, so a
// detected DATABASE_URL is prefilled with the add-on's connection string.
var addonVars = map[string]map[string]string{
	"postgres": {"DATABASE_URL": "${DATABASE_URL}", "POSTGRES_URL": "${DATABASE_URL}", "PG_URL": "${DATABASE_URL}",
		"POSTGRES_HOST": "postgres", "PGHOST": "postgres", "DB_HOST": "postgres", "POSTGRES_PASSWORD": "${POSTGRES_PASSWORD}",
		"DB_PASSWORD": "${POSTGRES_PASSWORD}", "POSTGRES_USER": "bot", "DB_USER": "bot", "DB_NAME": "bot", "POSTGRES_DB": "bot",
		"DB_PORT": "5432", "POSTGRES_PORT": "5432"},
	"redis":   {"REDIS_URL": "${REDIS_URL}", "REDIS_HOST": "redis", "REDIS_PORT": "6379", "REDIS_URI": "${REDIS_URL}"},
	"mongodb": {"MONGODB_URI": "${MONGODB_URI}", "MONGO_URI": "${MONGODB_URI}", "MONGO_URL": "${MONGODB_URI}", "MONGODB_URL": "${MONGODB_URI}", "DB_URI": "${MONGODB_URI}"},
	"mariadb": {"MYSQL_URL": "${MYSQL_URL}", "MYSQL_HOST": "mariadb", "MYSQL_PASSWORD": "${MYSQL_PASSWORD}", "MYSQL_USER": "bot", "MYSQL_DATABASE": "bot"},
}

// linkAddonEnv prefills variables the add-ons provide and drops the ones
// they set automatically with the same name.
func linkAddonEnv(p *Plan) {
	for _, a := range p.Addons {
		for i, e := range p.Env {
			if v, ok := addonVars[a][e.Name]; ok && p.Env[i].Value == "" {
				p.Env[i].Value = v
				p.Env[i].Required = false
				p.Env[i].Description = strings.TrimSpace(p.Env[i].Description + " Filled from the " + a + " add-on.")
			}
		}
	}
	// Variables an add-on already sets with the same name and value need no entry.
	out := p.Env[:0]
	for _, e := range p.Env {
		if strings.HasPrefix(e.Value, "${") && e.Value == "${"+e.Name+"}" {
			continue
		}
		out = append(out, e)
	}
	p.Env = out
}
