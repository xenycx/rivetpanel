package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/a2ui"
	operator "github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/paneldocs"
	"github.com/xenycx/rivetpanel/internal/reposcan"
)

// PanelAPI runs requests against the panel's own HTTP API as a person
// (api.AgentBridge). Every route keeps its own permission checks.
type PanelAPI interface {
	Do(ctx context.Context, actor domain.User, method, pathAndQuery string, body []byte) (int, []byte, error)
	Routes() []string
}

// agentTools are the panel-wide tools: deploying anything, configuring what
// exists, the panel API and the person's own browser preferences. They work
// without a bot or site in focus; tools that act on one take an optional id
// and default to the focused one.
func agentTools() []operator.Tool {
	str := map[string]any{"type": "string"}
	strMap := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
	strList := map[string]any{"type": "array", "items": str, "maxItems": 40}
	botID := map[string]any{"type": "string", "description": "Bot or app id; defaults to the focused one"}
	return []operator.Tool{
		toolSchema("panel_docs", "Search the RivetPanel guide (every feature, limit, setting and how it works: apps, game servers, containers, websites, domains, teams, backups, nodes, security, administration). Use it before answering how-to questions or acting on a feature you are unsure about. Returns the best pages with the matching section and its /docs link.", map[string]any{"query": str}, "query"),
		toolSchema("read_doc", "Read one guide page, or one section of it, from panel_docs results: page is an id such as websites/domains, a /docs/ link or a short name such as domains.", map[string]any{"page": str, "section": map[string]any{"type": "string", "description": "Optional heading, for example \"verify\" or \"limits\""}}, "page"),
		toolSchema("panel_info", "Describe this panel right now: version, enabled features and modules, the person's role and permissions, runtimes, add-on kinds, the container image policy, workspaces and capacity. Call it once when what is possible here matters.", map[string]any{}),
		toolSchema("analyze_repo", "Download a GitHub repository and work out how to host it: runtime, start and build commands, environment variables it reads, add-ons (databases), ports. as=app for bots/workers/APIs/containers, as=site for websites. Nothing is created.",
			map[string]any{"repo": map[string]any{"type": "string", "description": "owner/name or any GitHub URL (a /tree/<branch>/<dir> URL selects branch and folder)"}, "branch": str, "root_dir": str, "as": map[string]any{"type": "string", "enum": []string{"app", "site"}}}, "repo"),
		toolSchema("deploy_github", "Create an app or bot from a GitHub repository, pull its code and start it. Fields left out come from analyze_repo's plan (run it first unless you already know). Returns the new id; then call wait_for_ready.",
			map[string]any{"repo": str, "branch": str, "root_dir": str, "name": str,
				"runtime":       map[string]any{"type": "string", "description": "Runtime id: nodejs, python, go, java, ruby, rust or php; omit to use the plan's"},
				"start_command": map[string]any{"type": "array", "items": str, "description": "argv, for example [\"node\",\"index.js\"]; omit to use the plan's"},
				"build_command": str, "env": strMap,
				"addons":       map[string]any{"type": "array", "items": str, "description": "Add-on kinds such as postgres, mysql, redis, mongodb"},
				"memory_mb":    map[string]any{"type": "integer", "minimum": 64, "maximum": 65536},
				"auto_deploy":  map[string]any{"type": "boolean", "description": "Redeploy on every push (default true)"},
				"start":        map[string]any{"type": "boolean", "description": "Start once deployed (default true)"},
				"workspace_id": str}, "repo"),
		toolSchema("deploy_site", "Create a website from a GitHub repository (static, built static such as Vite/Next export/Astro, or a running web app) and publish it. Fields left out come from analyze_repo as=site. Returns the site and its address.",
			map[string]any{"repo": str, "branch": str, "root_dir": str, "name": str, "slug": map[string]any{"type": "string", "description": "Address label; defaults from the name"},
				"kind":      map[string]any{"type": "string", "enum": []string{reposcan.WebStatic, reposcan.WebStaticBuild, reposcan.WebApp}},
				"framework": str, "runtime": map[string]any{"type": "string", "description": "nodejs, python, go, java, ruby, rust or php"}, "build_command": str, "output_dir": str, "spa": map[string]any{"type": "boolean"},
				"start_command": map[string]any{"type": "array", "items": str}, "port": map[string]any{"type": "integer"}, "env": strMap,
				"auto_deploy": map[string]any{"type": "boolean"}, "workspace_id": str}, "repo"),
		toolSchema("deploy_image", "Run a Docker image (Docker Hub, GHCR or another allowed registry) as an app. Publishes the given container ports and starts it.",
			map[string]any{"image": map[string]any{"type": "string", "description": "for example nginx:alpine, ghcr.io/owner/app:latest"}, "name": str,
				"ports":        map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535}, "description": "Container ports to publish"},
				"env":          strMap,
				"command":      strList,
				"volumes":      map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"name": str, "path": str}, "required": []string{"name", "path"}}, "description": "Persistent named volumes mounted at absolute paths"},
				"writable":     map[string]any{"type": "array", "items": str, "description": "Extra writable folders (the root filesystem is read-only)"},
				"memory_mb":    map[string]any{"type": "integer", "minimum": 64, "maximum": 65536},
				"start":        map[string]any{"type": "boolean", "description": "default true"},
				"workspace_id": str}, "image"),
		toolSchema("deploy_compose", "Import a docker-compose file as a stack of apps and start it. Use dry_run first to see what the panel accepts.",
			map[string]any{"compose": map[string]any{"type": "string", "description": "The compose YAML"}, "name": str, "env_file": map[string]any{"type": "string", "description": "Optional .env content for ${VAR} interpolation"}, "dry_run": map[string]any{"type": "boolean"}, "workspace_id": str}, "compose"),
		toolSchema("list_app_templates", "Search the one-click app catalog (databases, dashboards, CMSs, tools) with each app's variables.", map[string]any{"query": str}),
		toolSchema("install_app_template", "Install a one-click app from the catalog. Leave values out to use defaults and generated passwords.",
			map[string]any{"template_id": str, "name": str, "values": strMap, "address": map[string]any{"type": "string", "enum": []string{"port", "site"}, "description": "site gives it a web address (needs slug)"}, "slug": str, "workspace_id": str}, "template_id"),
		toolSchema("wait_for_ready", "Wait until a bot or app finishes deploying and starting (or fails), then report its state and the last console lines. Use it after every deploy or restart.",
			map[string]any{"bot_id": botID, "seconds": map[string]any{"type": "integer", "minimum": 5, "maximum": 300, "description": "default 120"}}),
		toolSchema("set_environment", "Set environment variables on a bot or app (creates or replaces them). Restart afterwards for a running bot to see them.",
			map[string]any{"bot_id": botID, "vars": strMap, "restart": map[string]any{"type": "boolean"}}, "vars"),
		toolSchema("delete_environment", "Remove environment variables from a bot or app.", map[string]any{"bot_id": botID, "names": strList}, "names"),
		toolSchema("power", "Start, stop, restart or kill a bot or app.", map[string]any{"bot_id": botID, "action": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "kill"}}}, "action"),
		toolSchema("update_bot_settings", "Change a bot's configuration: name, start command, build command, runtime, memory, CPU, network, restart policy, automatic backups.",
			map[string]any{"bot_id": botID, "name": str, "start_command": strList, "build_command": str, "runtime": map[string]any{"type": "string", "description": "nodejs, python, go, java, ruby, rust or php"},
				"memory_mb": map[string]any{"type": "integer", "minimum": 64, "maximum": 65536}, "cpus": map[string]any{"type": "number", "minimum": 0.1, "maximum": 64},
				"network_enabled": map[string]any{"type": "boolean"}, "restart_policy": map[string]any{"type": "string", "enum": []string{"no", "on-failure", "always", "unless-stopped"}},
				"auto_backup": map[string]any{"type": "boolean"}}),
		toolSchema("console_command", "Send one line to a running bot's or game server's console input (stdin), for example a game server command.", map[string]any{"bot_id": botID, "line": str}, "line"),
		toolSchema("redeploy", "Pull the latest commit of a GitHub-linked bot or app and deploy it now.", map[string]any{"bot_id": botID}),
		toolSchema("delete_bot", "Delete a bot or app and its files permanently.", map[string]any{"bot_id": map[string]any{"type": "string"}}, "bot_id"),
		toolSchema("panel_routes", "List the panel API routes you can call with panel_api (METHOD /path). Filter with a word such as backups, schedules, domains, workspaces, notifications, settings.", map[string]any{"query": str}),
		toolSchema("panel_api", "Call any panel API route as the person, with their permissions: everything the panel's pages can do (backups, schedules, domains, sites, workspaces, members, add-ons, notifications, profile, admin settings for administrators). Paths are relative to /api/v1, for example GET /bots or PATCH /bots/{id}. Find routes with panel_routes. Read with GET before you change something so you send the right fields.",
			map[string]any{"method": map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}}, "path": str, "body": map[string]any{"type": "object", "description": "JSON body for POST, PUT and PATCH"}}, "method", "path"),
		toolSchema("show_ui", a2ui.ToolDescription, map[string]any{"messages": map[string]any{"type": "array", "minItems": 1, "maxItems": a2ui.MaxMessages, "items": map[string]any{"type": "object"}, "description": "A2UI v1.0 messages, applied in order"}}, "messages"),
		toolSchema("client_action", "Change this person's own browser preferences or move their screen: theme (light, dark, system), accent colour (ember, ocean, teal, violet, orchid, rose, amber, lime, cyan, indigo, slate, clay), corner radius (none, subtle, default, round), or navigate to a panel page such as /bots/{id} or /settings.",
			map[string]any{"action": map[string]any{"type": "string", "enum": []string{"set_theme", "set_accent", "set_radius", "navigate"}}, "value": str}, "action", "value"),
	}
}

var agentToolNames = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range agentTools() {
		m[t.Function.Name] = true
	}
	return m
}()

// agentReadOnly agent tools never change anything.
var agentReadOnly = map[string]bool{"panel_docs": true, "read_doc": true, "panel_info": true, "list_app_templates": true, "panel_routes": true, "wait_for_ready": true, "analyze_repo": true, "show_ui": true}

// secretArgs lists the argument keys whose values are secrets the tool-call
// record must not keep. The live run still passes them to the panel.
var secretArgs = map[string][]string{
	"set_environment":      {"vars"},
	"deploy_github":        {"env"},
	"deploy_site":          {"env"},
	"deploy_image":         {"env"},
	"install_app_template": {"values"},
	"deploy_compose":       {"env_file"},
	"panel_api":            {"body"},
}

// redactToolArgs replaces secret values in a tool call's stored arguments:
// variables whose names look secret (or any template value) become
// "[hidden]", other variables stay readable, and panel_api bodies hide
// fields whose names look secret.
func redactToolArgs(name, raw string) string {
	keys := secretArgs[name]
	if len(keys) == 0 {
		return raw
	}
	var a map[string]any
	if json.Unmarshal([]byte(raw), &a) != nil {
		return raw
	}
	for _, k := range keys {
		switch v := a[k].(type) {
		case map[string]any:
			if name == "panel_api" {
				a[k] = hideSecretFields(v)
				continue
			}
			out := map[string]any{}
			for n, x := range v {
				if k == "values" || reposcan.IsSecretName(n) || strings.Contains(strings.ToLower(n), "password") {
					out[n] = "[hidden]"
				} else {
					out[n] = x
				}
			}
			a[k] = out
		case string:
			if v != "" {
				a[k] = "[hidden]"
			}
		}
	}
	b, _ := json.Marshal(a)
	return string(b)
}

func hideSecretFields(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch t := v.(type) {
		case map[string]any:
			if k == "env" || k == "values" || k == "vars" {
				h := map[string]any{}
				for n := range t {
					h[n] = "[hidden]"
				}
				out[k] = h
			} else {
				out[k] = hideSecretFields(t)
			}
		case string:
			if reposcan.IsSecretName(k) || strings.Contains(strings.ToLower(k), "password") {
				out[k] = "[hidden]"
			} else {
				out[k] = t
			}
		default:
			out[k] = v
		}
	}
	return out
}

// agentArgs reads tool arguments.
type agentArgs map[string]json.RawMessage

func (a agentArgs) str(k string) string {
	var v string
	_ = json.Unmarshal(a[k], &v)
	return strings.TrimSpace(v)
}
func (a agentArgs) strs(k string) []string {
	var v []string
	_ = json.Unmarshal(a[k], &v)
	return v
}
func (a agentArgs) strMap(k string) map[string]string {
	var v map[string]any
	if json.Unmarshal(a[k], &v) != nil {
		return nil
	}
	out := make(map[string]string, len(v))
	for n, x := range v {
		switch t := x.(type) {
		case string:
			out[n] = t
		case nil:
			out[n] = ""
		default:
			b, _ := json.Marshal(t)
			out[n] = string(b)
		}
	}
	return out
}
func (a agentArgs) boolOr(k string, d bool) bool {
	var v *bool
	if json.Unmarshal(a[k], &v) != nil || v == nil {
		return d
	}
	return *v
}
func (a agentArgs) int(k string) int64 {
	var v float64
	_ = json.Unmarshal(a[k], &v)
	return int64(v)
}
func (a agentArgs) float(k string) float64 {
	var v float64
	_ = json.Unmarshal(a[k], &v)
	return v
}
func (a agentArgs) has(k string) bool { r, ok := a[k]; return ok && string(r) != "null" }

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", " ")
	return string(b)
}

// botFor resolves the bot a tool acts on: its bot_id argument, else the
// focused bot.
func (s *AIService) botFor(ctx context.Context, actor domain.User, c domain.AIConversation, a agentArgs, perm int) (domain.Bot, error) {
	id := a.str("bot_id")
	if id == "" && c.BotID != nil {
		id = *c.BotID
	}
	if id == "" {
		return domain.Bot{}, domain.Invalid("say which bot or app: pass bot_id (list_targets shows them) or focus one")
	}
	return s.Bots.Authorize(ctx, actor, id, perm)
}

// approve asks the person in approval mode; Autopilot just proceeds.
func (s *AIService) approve(ctx context.Context, run *domain.AIRun, call *domain.AIToolCall, title string, detail any) bool {
	if run.Mode == AIAuto {
		return true
	}
	return s.awaitMutation(ctx, run, call, title, detail)
}

// adopt points the run at a bot or site it just created, so the next tools
// (logs, files, environment) work on it without another focus call.
func (s *AIService) adopt(ctx context.Context, run *domain.AIRun, kind, id, name string) {
	switch kind {
	case "bot":
		run.BotID, run.SiteID = &id, nil
	case "site":
		run.BotID, run.SiteID = nil, &id
	}
	_ = s.Store.UpdateAIRun(context.WithoutCancel(ctx), *run)
	s.emit(run.ID, "focus", map[string]any{"kind": kind, "id": id, "name": name})
}

func mib(mb int64) int64 {
	if mb <= 0 {
		return 0
	}
	return mb << 20
}

// executeAgentTool runs the panel-wide tools. handled is false for other tools.
func (s *AIService) executeAgentTool(ctx context.Context, actor domain.User, c domain.AIConversation, run *domain.AIRun, call *domain.AIToolCall, budget *aiBudget, raw string) (out string, mutated bool, err error, handled bool) {
	if !agentToolNames[call.Name] {
		return "", false, nil, false
	}
	var a agentArgs
	if e := json.Unmarshal([]byte(raw), &a); e != nil {
		return "", false, domain.Invalid("tool arguments are invalid"), true
	}
	if !agentReadOnly[call.Name] {
		if e := budget.action(); e != nil {
			return "", false, e, true
		}
	}
	out, mutated, err = s.agentTool(ctx, actor, c, run, call, budget, a)
	if mutated {
		budget.actions++
	}
	return out, mutated, err, true
}

func (s *AIService) agentTool(ctx context.Context, actor domain.User, c domain.AIConversation, run *domain.AIRun, call *domain.AIToolCall, budget *aiBudget, a agentArgs) (string, bool, error) {
	red := s.redactor(ctx, c)
	switch call.Name {
	case "panel_docs":
		hits := paneldocs.Search(a.str("query"), 5)
		if len(hits) == 0 {
			return "Nothing in the guide matches; try other words (feature names work best).", false, nil
		}
		var b strings.Builder
		for i, h := range hits {
			fmt.Fprintf(&b, "%d. %s (page %s, %s)\n", i+1, h.Page.Title, h.Page.ID, h.Page.URL)
			if h.Page.Lede != "" {
				b.WriteString("   " + h.Page.Lede + "\n")
			}
			if i < 3 {
				if h.Section.Heading != "" {
					fmt.Fprintf(&b, "   Section \"%s\" (%s#%s):\n", h.Section.Heading, h.Page.URL, h.Section.Anchor)
				}
				b.WriteString("   " + strings.ReplaceAll(clipService(h.Section.Text, 1800), "\n", "\n   ") + "\n")
			}
			b.WriteString("\n")
		}
		b.WriteString("Read a whole page or another section with read_doc.")
		return b.String(), false, nil

	case "read_doc":
		pg, ok := paneldocs.Find(a.str("page"))
		if !ok {
			var ids []string
			for _, p := range paneldocs.Pages() {
				ids = append(ids, p.ID)
			}
			return "", false, domain.Invalid("no such guide page; pages are: " + strings.Join(ids, ", "))
		}
		return clipService(pg.Text(a.str("section")), 24000), false, nil

	case "panel_info":
		return s.panelInfo(ctx, actor), false, nil

	case "analyze_repo":
		if s.Deploy == nil {
			return "", false, domain.Invalid("GitHub is not configured on this panel")
		}
		in := AnalyzeInput{Repo: a.str("repo"), Branch: a.str("branch"), RootDir: a.str("root_dir")}
		if a.str("as") == "site" {
			r, e := s.Deploy.AnalyzeWeb(ctx, actor, in)
			if e != nil {
				return "", false, e
			}
			return jsonText(map[string]any{"repo": r.Repo.FullName, "branch": r.Branch, "root_dir": r.RootDir, "sha": r.SHA, "files": r.Files, "plan": r.Plan}), false, nil
		}
		r, e := s.Deploy.Analyze(ctx, actor, in)
		if e != nil {
			return "", false, e
		}
		return jsonText(map[string]any{"repo": r.Repo.FullName, "private": r.Repo.Private, "branch": r.Branch, "root_dir": r.RootDir, "sha": r.SHA, "files": r.Files, "plan": r.Plan}), false, nil

	case "deploy_github":
		return s.agentDeployGitHub(ctx, actor, run, call, a)

	case "deploy_site":
		return s.agentDeploySite(ctx, actor, run, call, a)

	case "deploy_image":
		if s.Containers == nil {
			return "", false, domain.Invalid("container apps are not enabled on this panel")
		}
		image := a.str("image")
		if image == "" {
			return "", false, domain.Invalid("image is required")
		}
		name := a.str("name")
		if name == "" {
			name = imageName(image)
		}
		var ports []PortInput
		var nums []int
		_ = json.Unmarshal(a["ports"], &nums)
		for _, p := range nums {
			ports = append(ports, PortInput{ContainerPort: p})
		}
		var vols []domain.Volume
		_ = json.Unmarshal(a["volumes"], &vols)
		if !s.approve(ctx, run, call, "Run Docker image "+image, map[string]any{"image": image, "name": name, "ports": nums}) {
			return "Rejected by user.", false, nil
		}
		b, e := s.Containers.Create(ctx, actor, ContainerInput{Name: name, WorkspaceID: a.str("workspace_id"), Image: image, Command: a.strs("command"),
			Writable: a.strs("writable"), Volumes: vols, Ports: ports, Env: a.strMap("env"), MemoryBytes: mib(a.int("memory_mb"))})
		s.audit(ctx, actor, run, "bot", b.ID, "ai.deploy_image", image, outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		s.adopt(ctx, run, "bot", b.ID, b.Name)
		msg := fmt.Sprintf("Created app %q (id %s) from %s.", b.Name, b.ID, image)
		if a.boolOr("start", true) {
			if _, e := s.Bots.Start(ctx, actor, b.ID); e != nil {
				msg += " Starting it failed: " + safeToolError(e)
			} else {
				msg += " Starting now (the image is pulled first); call wait_for_ready."
			}
		}
		return msg, true, nil

	case "deploy_compose":
		if s.Containers == nil {
			return "", false, domain.Invalid("container apps are not enabled on this panel")
		}
		in := ComposeImport{Name: a.str("name"), WorkspaceID: a.str("workspace_id"), Compose: a.str("compose"), Env: a.str("env_file"), DryRun: a.boolOr("dry_run", false), Start: true}
		if in.Name == "" {
			in.Name = "stack"
		}
		if in.DryRun {
			p, e := s.Containers.PlanCompose(ctx, actor, in)
			if e != nil {
				return "", false, e
			}
			return jsonText(p), false, nil
		}
		if !s.approve(ctx, run, call, "Import compose stack "+in.Name, map[string]any{"name": in.Name}) {
			return "Rejected by user.", false, nil
		}
		p, e := s.Containers.ImportCompose(ctx, actor, in)
		s.audit(ctx, actor, run, "stack", "", "ai.deploy_compose", in.Name, outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		return red.Text(jsonText(p)), true, nil

	case "list_app_templates":
		if s.Containers == nil {
			return "", false, domain.Invalid("one-click apps are not enabled on this panel")
		}
		q := strings.ToLower(a.str("query"))
		type v struct {
			ID, Name, Category, Tagline string
			Available                   bool
			Reason                      string `json:",omitempty"`
			Vars                        []map[string]any
		}
		var list []v
		for _, t := range s.Containers.AppTemplates() {
			hay := strings.ToLower(t.ID + " " + t.Name + " " + t.Tagline + " " + t.Category + " " + t.Description)
			if q != "" && !strings.Contains(hay, q) {
				continue
			}
			var vars []map[string]any
			for _, x := range t.Vars {
				vars = append(vars, map[string]any{"name": x.Name, "label": x.Label, "required": x.Required, "secret": x.Secret, "default": x.Default, "generated": x.Generate != nil})
			}
			list = append(list, v{t.ID, t.Name, t.Category, t.Tagline, t.Available, t.Reason, vars})
			if len(list) == 40 {
				break
			}
		}
		if len(list) == 0 {
			return "No app in the catalog matches. Try a broader word, or deploy_image with the app's Docker image.", false, nil
		}
		return jsonText(list), false, nil

	case "install_app_template":
		if s.Containers == nil {
			return "", false, domain.Invalid("one-click apps are not enabled on this panel")
		}
		in := TemplateInstall{TemplateID: a.str("template_id"), Name: a.str("name"), WorkspaceID: a.str("workspace_id"), Values: a.strMap("values"), Slug: a.str("slug")}
		if a.str("address") == "site" {
			in.Address = AddressSite
		}
		if !s.approve(ctx, run, call, "Install "+in.TemplateID, map[string]any{"template": in.TemplateID, "name": in.Name}) {
			return "Rejected by user.", false, nil
		}
		res, e := s.Containers.InstallTemplate(ctx, actor, in)
		s.audit(ctx, actor, run, "stack", "", "ai.install_app", in.TemplateID, outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		out := map[string]any{"url": res.URL, "warnings": res.Plan.Warnings, "errors": res.Plan.Errors}
		if res.Stack != nil {
			out["stack_id"] = res.Stack.ID
			out["stack_name"] = res.Stack.Name
		}
		return red.Text(jsonText(out)), true, nil

	case "wait_for_ready":
		b, e := s.botFor(ctx, actor, c, a, 0)
		if e != nil {
			return "", false, e
		}
		return s.waitReady(ctx, actor, b.ID, int(a.int("seconds")), red)

	case "set_environment":
		b, e := s.botFor(ctx, actor, c, a, domain.PermManageEnv)
		if e != nil {
			return "", false, e
		}
		vars := a.strMap("vars")
		if len(vars) == 0 {
			return "", false, domain.Invalid("vars is empty")
		}
		names := sortedKeys(vars)
		if !s.approve(ctx, run, call, "Set "+strings.Join(names, ", ")+" on "+b.Name, map[string]any{"names": names}) {
			return "Rejected by user.", false, nil
		}
		e = s.Bots.SetEnv(ctx, actor, b.ID, vars)
		s.audit(ctx, actor, run, "bot", b.ID, "ai.env_set", strings.Join(names, ", "), outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		msg := "Set " + strings.Join(names, ", ") + " on " + b.Name + "."
		if a.boolOr("restart", false) && b.DesiredState == domain.DesiredRunning {
			if _, e := s.Bots.Restart(ctx, actor, b.ID); e != nil {
				msg += " Restart failed: " + safeToolError(e)
			} else {
				msg += " Restarting so it picks them up."
			}
		}
		return msg, true, nil

	case "delete_environment":
		b, e := s.botFor(ctx, actor, c, a, domain.PermManageEnv)
		if e != nil {
			return "", false, e
		}
		names := a.strs("names")
		if len(names) == 0 {
			return "", false, domain.Invalid("names is empty")
		}
		if !s.approve(ctx, run, call, "Remove "+strings.Join(names, ", ")+" from "+b.Name, map[string]any{"names": names}) {
			return "Rejected by user.", false, nil
		}
		var failed []string
		for _, n := range names {
			if e := s.Bots.DeleteEnv(ctx, actor, b.ID, n); e != nil {
				failed = append(failed, n+": "+safeToolError(e))
			}
		}
		s.audit(ctx, actor, run, "bot", b.ID, "ai.env_delete", strings.Join(names, ", "), map[bool]string{true: "ok", false: "error"}[len(failed) == 0])
		if len(failed) > 0 {
			return "Some were not removed: " + strings.Join(failed, "; "), true, nil
		}
		return "Removed " + strings.Join(names, ", ") + ".", true, nil

	case "power":
		b, e := s.botFor(ctx, actor, c, a, domain.PermPower)
		if e != nil {
			return "", false, e
		}
		act := a.str("action")
		if act != "start" && act != "stop" && act != "restart" && act != "kill" {
			return "", false, domain.Invalid("action must be start, stop, restart or kill")
		}
		if !s.approve(ctx, run, call, strings.ToUpper(act[:1])+act[1:]+" "+b.Name, nil) {
			return "Rejected by user.", false, nil
		}
		switch act {
		case "start":
			b, e = s.Bots.Start(ctx, actor, b.ID)
		case "stop":
			b, e = s.Bots.Stop(ctx, actor, b.ID)
		case "restart":
			b, e = s.Bots.Restart(ctx, actor, b.ID)
		case "kill":
			b, e = s.Bots.Kill(ctx, actor, b.ID)
		default:
			return "", false, domain.Invalid("action must be start, stop, restart or kill")
		}
		s.audit(ctx, actor, run, "bot", b.ID, "ai."+act, "", outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		return fmt.Sprintf("%s requested for %s: desired state %s.", act, b.Name, b.DesiredState), true, nil

	case "update_bot_settings":
		b, e := s.botFor(ctx, actor, c, a, domain.PermConfigure)
		if e != nil {
			return "", false, e
		}
		var in UpdateBotInput
		var changed []string
		if v := a.str("name"); v != "" {
			in.Name, changed = &v, append(changed, "name")
		}
		if a.has("start_command") {
			v := a.strs("start_command")
			in.Argv, changed = &v, append(changed, "start command")
		}
		if a.has("build_command") {
			v := a.str("build_command")
			in.BuildCommand, changed = &v, append(changed, "build command")
		}
		if v := a.str("runtime"); v != "" {
			in.Runtime, changed = &v, append(changed, "runtime")
		}
		if v := mib(a.int("memory_mb")); v > 0 {
			in.MemoryBytes, changed = &v, append(changed, "memory")
		}
		if v := int64(a.float("cpus") * 1e9); v > 0 {
			in.NanoCPUs, changed = &v, append(changed, "CPU")
		}
		if a.has("network_enabled") {
			v := a.boolOr("network_enabled", true)
			in.NetworkEnabled, changed = &v, append(changed, "network")
		}
		if v := a.str("restart_policy"); v != "" {
			in.RestartPolicy, changed = &v, append(changed, "restart policy")
		}
		if a.has("auto_backup") {
			v := a.boolOr("auto_backup", false)
			in.AutoBackup, changed = &v, append(changed, "automatic backups")
		}
		if len(changed) == 0 {
			return "", false, domain.Invalid("name at least one setting to change")
		}
		if !s.approve(ctx, run, call, "Change "+strings.Join(changed, ", ")+" of "+b.Name, nil) {
			return "Rejected by user.", false, nil
		}
		nb, e := s.Bots.Update(ctx, actor, b.ID, in)
		s.audit(ctx, actor, run, "bot", b.ID, "ai.settings", strings.Join(changed, ", "), outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		msg := "Updated " + strings.Join(changed, ", ") + " of " + nb.Name + "."
		if nb.DesiredState == domain.DesiredRunning {
			msg += " Restart it (power restart) if the change needs a new process."
		}
		return msg, true, nil

	case "console_command":
		b, e := s.botFor(ctx, actor, c, a, domain.PermViewConsole)
		if e != nil {
			return "", false, e
		}
		line := a.str("line")
		if !s.approve(ctx, run, call, "Send to the console of "+b.Name, map[string]any{"line": line}) {
			return "Rejected by user.", false, nil
		}
		e = s.Bots.SendCommand(ctx, actor, b.ID, line)
		s.audit(ctx, actor, run, "bot", b.ID, "ai.console", clipService(line, 200), outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		return "Sent. Read the console with read_logs to see the answer.", true, nil

	case "redeploy":
		if s.Deploy == nil {
			return "", false, domain.Invalid("GitHub is not configured on this panel")
		}
		b, e := s.botFor(ctx, actor, c, a, domain.PermConfigure)
		if e != nil {
			return "", false, e
		}
		if !s.approve(ctx, run, call, "Redeploy "+b.Name, nil) {
			return "Rejected by user.", false, nil
		}
		e = s.Deploy.DeployAs(ctx, actor, b.ID, "", "ai")
		s.audit(ctx, actor, run, "bot", b.ID, "ai.deploy", "", outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		return "Deployment of the latest commit started; call wait_for_ready.", true, nil

	case "delete_bot":
		b, e := s.Bots.Authorize(ctx, actor, a.str("bot_id"), permOwnerOnly)
		if e != nil {
			return "", false, e
		}
		if !s.approve(ctx, run, call, "Delete "+b.Name+" permanently", nil) {
			return "Rejected by user.", false, nil
		}
		e = s.Bots.Delete(ctx, actor, b.ID)
		s.audit(ctx, actor, run, "bot", b.ID, "ai.delete", b.Name, outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		if run.BotID != nil && *run.BotID == b.ID {
			run.BotID = nil
			_ = s.Store.UpdateAIRun(context.WithoutCancel(ctx), *run)
		}
		return "Deleted " + b.Name + ".", true, nil

	case "panel_routes":
		if s.API == nil {
			return "", false, domain.Invalid("the panel API is not available to the assistant")
		}
		q := strings.ToLower(a.str("query"))
		var out []string
		for _, r := range s.API.Routes() {
			if q == "" || strings.Contains(strings.ToLower(r), q) {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return "No route matches; try another word or no filter.", false, nil
		}
		if len(out) > 400 {
			out = append(out[:400], "… (narrow the query)")
		}
		return strings.Join(out, "\n"), false, nil

	case "panel_api":
		return s.agentPanelAPI(ctx, actor, run, call, budget, a)

	case "show_ui":
		out, e := s.showUI(ctx, c, a)
		return out, false, e

	case "client_action":
		act, val := a.str("action"), a.str("value")
		ok := false
		switch act {
		case "set_theme":
			ok = val == "light" || val == "dark" || val == "system"
		case "set_accent":
			ok = strings.Contains(" ember ocean teal violet orchid rose amber lime cyan indigo slate clay ", " "+val+" ")
		case "set_radius":
			ok = val == "none" || val == "subtle" || val == "default" || val == "round"
		case "navigate":
			ok = strings.HasPrefix(val, "/") && !strings.HasPrefix(val, "//") && !strings.Contains(val, "\\") && len(val) < 300
		}
		if !ok {
			return "", false, domain.Invalid("unsupported value for " + act)
		}
		s.emit(run.ID, "client_action", map[string]string{"action": act, "value": val})
		return "Done in the person's browser: " + act + " " + val + ".", false, nil
	}
	return "", false, domain.Invalid("unknown tool")
}

func imageName(image string) string {
	n := image
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	if i := strings.IndexAny(n, ":@"); i >= 0 {
		n = n[:i]
	}
	if n == "" {
		n = "app"
	}
	return n
}

func (s *AIService) agentDeployGitHub(ctx context.Context, actor domain.User, run *domain.AIRun, call *domain.AIToolCall, a agentArgs) (string, bool, error) {
	if s.Deploy == nil {
		return "", false, domain.Invalid("GitHub is not configured on this panel")
	}
	an, e := s.Deploy.Analyze(ctx, actor, AnalyzeInput{Repo: a.str("repo"), Branch: a.str("branch"), RootDir: a.str("root_dir")})
	if e != nil {
		return "", false, e
	}
	p := an.Plan
	name := a.str("name")
	if name == "" {
		name = an.Repo.FullName[strings.Index(an.Repo.FullName, "/")+1:]
	}
	in := CreateBotInput{Name: name, Runtime: p.Runtime, Argv: p.Argv, BuildCommand: p.BuildCommand, MemoryBytes: p.MemoryBytes,
		NanoCPUs: p.NanoCPUs, PidsLimit: p.PidsLimit, WorkspaceID: a.str("workspace_id"), Env: map[string]string{}}
	if v := a.str("runtime"); v != "" {
		in.Runtime = v
	}
	if v := a.strs("start_command"); len(v) > 0 {
		in.Argv = v
	}
	if a.has("build_command") {
		in.BuildCommand = a.str("build_command")
	}
	if v := mib(a.int("memory_mb")); v > 0 {
		in.MemoryBytes = v
	}
	addons := p.Addons
	if a.has("addons") {
		addons = a.strs("addons")
	}
	for _, k := range addons {
		in.Addons = append(in.Addons, AddonInput{Kind: k})
	}
	// Plan values prefill non-secret defaults (often add-on references such
	// as ${POSTGRES_URL}); the agent's values win.
	var missing []string
	for _, v := range p.Env {
		if v.Value != "" {
			in.Env[v.Name] = v.Value
		}
	}
	for k, v := range a.strMap("env") {
		in.Env[k] = v
	}
	for _, v := range p.Env {
		if v.Required && in.Env[v.Name] == "" {
			missing = append(missing, v.Name)
		}
	}
	if len(in.Env) == 0 {
		in.Env = nil
	}
	if in.Runtime == "" {
		return "", false, domain.Invalid("the runtime could not be detected; pass runtime (see analyze_repo)")
	}
	detail := map[string]any{"repo": an.Repo.FullName, "branch": an.Branch, "name": name, "runtime": in.Runtime, "start_command": in.Argv, "addons": addons}
	if !s.approve(ctx, run, call, "Deploy "+an.Repo.FullName, detail) {
		return "Rejected by user.", false, nil
	}
	// An app missing required secrets would only crash-loop: deploy the code
	// now and start once the variables are set, unless told to start anyway.
	start := a.boolOr("start", len(missing) == 0)
	b, rv, e := s.Deploy.CreateFromGitHub(ctx, actor, in, ConfigureInput{FullName: an.Repo.FullName, Branch: an.Branch, RootDir: an.RootDir, AutoDeploy: a.boolOr("auto_deploy", true)}, start)
	s.audit(ctx, actor, run, "bot", b.ID, "ai.deploy_github", an.Repo.FullName, outcomeOf(e))
	if e != nil {
		return "", false, e
	}
	s.adopt(ctx, run, "bot", b.ID, b.Name)
	out := map[string]any{"id": b.ID, "name": b.Name, "runtime": b.Runtime, "start_command": b.Argv, "repo": rv.FullName, "branch": rv.Branch,
		"auto_deploy": rv.AutoDeploy, "webhook_installed": rv.HookCreated, "polling": rv.Polling, "starts_after_deploy": start, "page": "/bots/" + b.ID}
	if len(missing) > 0 {
		out["missing_required_env"] = missing
		out["next"] = "The code is deployed but not started. Ask the person for the missing values (or call request_environment_values), set them with set_environment, then power start and wait_for_ready."
	} else {
		out["next"] = "Call wait_for_ready."
	}
	if len(p.Notes) > 0 {
		out["notes"] = p.Notes
	}
	if len(p.Ports) > 0 {
		out["listens_on"] = p.Ports
	}
	return jsonText(out), true, nil
}

func (s *AIService) agentDeploySite(ctx context.Context, actor domain.User, run *domain.AIRun, call *domain.AIToolCall, a agentArgs) (string, bool, error) {
	if s.Deploy == nil || s.Sites == nil {
		return "", false, domain.Invalid("sites from GitHub are not available on this panel")
	}
	an, e := s.Deploy.AnalyzeWeb(ctx, actor, AnalyzeInput{Repo: a.str("repo"), Branch: a.str("branch"), RootDir: a.str("root_dir")})
	if e != nil {
		return "", false, e
	}
	p := an.Plan
	name := a.str("name")
	if name == "" {
		name = an.Repo.FullName[strings.Index(an.Repo.FullName, "/")+1:]
	}
	in := CreateBuiltSiteInput{Name: name, Slug: a.str("slug"), WorkspaceID: a.str("workspace_id"), Kind: p.Kind, Framework: p.Framework,
		Repo:    ConfigureInput{FullName: an.Repo.FullName, Branch: an.Branch, RootDir: an.RootDir, AutoDeploy: a.boolOr("auto_deploy", true)},
		Runtime: p.Runtime, BuildCommand: p.BuildCommand, OutputDir: p.OutputDir, SPA: p.SPA, Argv: p.Argv, Port: p.Port,
		HealthPath: p.HealthPath, ReleaseCommand: p.ReleaseCommand, MemoryBytes: p.MemoryBytes, Env: map[string]string{}}
	for k, v := range map[string]*string{"kind": &in.Kind, "framework": &in.Framework, "runtime": &in.Runtime, "output_dir": &in.OutputDir} {
		if x := a.str(k); x != "" {
			*v = x
		}
	}
	if a.has("build_command") {
		in.BuildCommand = a.str("build_command")
	}
	if a.has("spa") {
		in.SPA = a.boolOr("spa", false)
	}
	if v := a.strs("start_command"); len(v) > 0 {
		in.Argv = v
	}
	if v := a.int("port"); v > 0 {
		in.Port = int(v)
	}
	for _, v := range p.Env {
		if v.Value != "" {
			in.Env[v.Name] = v.Value
		}
	}
	for k, v := range a.strMap("env") {
		in.Env[k] = v
	}
	if len(in.Env) == 0 {
		in.Env = nil
	}
	for _, k := range p.Addons {
		in.Addons = append(in.Addons, AddonInput{Kind: k})
	}
	if in.Runtime == "" {
		in.Runtime = "nodejs"
	}
	if !s.approve(ctx, run, call, "Publish site from "+an.Repo.FullName, map[string]any{"repo": an.Repo.FullName, "kind": in.Kind, "framework": in.Framework, "name": name}) {
		return "Rejected by user.", false, nil
	}
	st, e := s.Sites.CreateBuilt(ctx, actor, in)
	s.audit(ctx, actor, run, "site", st.ID, "ai.deploy_site", an.Repo.FullName, outcomeOf(e))
	if e != nil {
		return "", false, e
	}
	s.adopt(ctx, run, "site", st.ID, st.Name)
	return jsonText(map[string]any{"id": st.ID, "name": st.Name, "url": s.Sites.URLOf(st), "kind": in.Kind, "framework": in.Framework,
		"page": "/sites/" + st.ID, "next": "The first build runs now. Check it with target_status in a minute; the address answers once it is published."}), true, nil
}

// waitReady polls a bot until it runs steadily, stops, or fails.
func (s *AIService) waitReady(ctx context.Context, actor domain.User, botID string, secs int, red *operator.Redactor) (string, bool, error) {
	if secs <= 0 || secs > 300 {
		secs = 120
	}
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	var b domain.Bot
	var e error
	steady := 0
	for {
		b, e = s.Bots.Authorize(ctx, actor, botID, 0)
		if e != nil {
			return "", false, e
		}
		deploying := s.Deploy != nil && s.Deploy.isRunning(b.ID)
		if !deploying {
			switch {
			case b.ObservedState == domain.DesiredRunning && b.Generation == b.ObservedGeneration:
				steady++
			case b.DesiredState == domain.DesiredStopped && b.ObservedState != domain.DesiredRunning:
				steady = 99 // stopped on purpose or not started
			case b.ObservedState == "failed" && b.Generation == b.ObservedGeneration:
				steady = 99 // crashed
			default:
				steady = 0
			}
		}
		// Running for a few polls means it did not crash on startup.
		if steady >= 3 || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s: desired=%s observed=%s", b.Name, b.DesiredState, b.ObservedState)
	if b.LastExitCode != nil {
		fmt.Fprintf(&out, " last_exit_code=%d", *b.LastExitCode)
	}
	if b.LastError != nil && *b.LastError != "" {
		out.WriteString("\nlast_error: " + *b.LastError)
	}
	if time.Now().After(deadline) {
		out.WriteString("\n(still settling when the wait ended; check again or read the build output)")
	}
	if s.Logs != nil && b.ContainerID != nil {
		if t, e := s.Logs.Tail(ctx, *b.ContainerID, 40); e == nil && strings.TrimSpace(t) != "" {
			out.WriteString("\n--- last console lines ---\n" + clipTail(t, 6000))
		}
	}
	if s.Ops != nil {
		if ops, e := s.Ops.ListForBot(ctx, actor, b.ID, nil, 0, 3); e == nil && len(ops) > 0 {
			o := ops[0]
			fmt.Fprintf(&out, "\nlatest operation: %s %s %s", o.ID, o.Kind, o.Status)
			if o.Message != nil {
				out.WriteString(" — " + clipService(*o.Message, 300))
			}
		}
	}
	return red.Text(out.String()), false, nil
}

func (s *AIService) agentPanelAPI(ctx context.Context, actor domain.User, run *domain.AIRun, call *domain.AIToolCall, budget *aiBudget, a agentArgs) (string, bool, error) {
	if s.API == nil {
		return "", false, domain.Invalid("the panel API is not available to the assistant")
	}
	method := strings.ToUpper(a.str("method"))
	p := a.str("path")
	if p == "" {
		return "", false, domain.Invalid("path is required")
	}
	if u, err := url.Parse(p); err != nil || u.Scheme != "" || u.Host != "" {
		return "", false, domain.Invalid("path must be a panel API path such as /bots")
	}
	var body []byte
	if a.has("body") && method != "GET" && method != "DELETE" {
		body = a["body"]
	}
	write := method != "GET"
	if write {
		if !s.approve(ctx, run, call, method+" "+p, map[string]any{"method": method, "path": p}) {
			return "Rejected by user.", false, nil
		}
	}
	fresh, e := s.Store.GetUserByID(ctx, actor.ID)
	if e != nil || fresh.Disabled {
		return "", false, domain.ErrForbidden
	}
	// Keep the client scope of the person (an API client acts as its creator
	// limited to the client) — the in-process request carries the same user.
	fresh.Client = actor.Client
	status, out, e := s.API.Do(ctx, fresh, method, p, body)
	if write {
		s.audit(ctx, actor, run, "panel", "", "ai.api", clipService(method+" "+p, 200), map[bool]string{true: "ok", false: "error"}[e == nil && status < 400])
	}
	if e != nil {
		return "", false, e
	}
	text := string(out)
	// Pretty JSON reads better for the model and survives clipping.
	var v any
	if json.Unmarshal(out, &v) == nil {
		text = jsonText(v)
	}
	if status >= 400 {
		return fmt.Sprintf("HTTP %d\n%s", status, clipService(text, 4000)), false, nil
	}
	return s.redactor(ctx, domain.AIConversation{BotID: run.BotID}).Text(fmt.Sprintf("HTTP %d\n%s", status, clipService(text, 60000))), write, nil
}

// panelInfo summarises this panel for the assistant through its own API,
// so it sees exactly what the person would.
func (s *AIService) panelInfo(ctx context.Context, actor domain.User) string {
	out := map[string]any{"version": s.Version}
	if s.API == nil {
		return jsonText(out)
	}
	fresh, e := s.Store.GetUserByID(ctx, actor.ID)
	if e != nil || fresh.Disabled {
		return jsonText(out)
	}
	fresh.Client = actor.Client
	get := func(p string) any {
		st, b, e := s.API.Do(ctx, fresh, "GET", p, nil)
		if e != nil || st >= 400 {
			return nil
		}
		var v any
		_ = json.Unmarshal(b, &v)
		return v
	}
	if me, ok := get("/auth/me").(map[string]any); ok {
		if u, ok := me["user"].(map[string]any); ok {
			out["you"] = map[string]any{"role": u["role"], "role_name": u["role_name"], "email_verified": u["email_verified"]}
		}
		out["features"] = me["features"]
		out["permissions"] = me["permissions"]
	}
	if rt, ok := get("/runtimes").(map[string]any); ok {
		var ids []string
		if list, ok := rt["runtimes"].([]any); ok {
			for _, r := range list {
				if m, ok := r.(map[string]any); ok {
					ids = append(ids, fmt.Sprint(m["id"]))
				}
			}
		}
		out["runtimes"] = ids
		out["limits"] = rt["limits"]
	}
	if ad, ok := get("/addons").(map[string]any); ok {
		var kinds []string
		for _, k := range []string{"addons", "kinds"} {
			if list, ok := ad[k].([]any); ok {
				for _, x := range list {
					if m, ok := x.(map[string]any); ok {
						kinds = append(kinds, fmt.Sprint(m["id"]))
					}
				}
			}
		}
		out["addon_kinds"] = kinds
	}
	out["container_policy"] = get("/containers/policy")
	out["modules"] = get("/modules")
	if ws, ok := get("/workspaces").(map[string]any); ok {
		out["workspaces"] = ws["workspaces"]
	}
	out["capacity"] = get("/me/capacity")
	out["github_deploys"] = s.Deploy != nil
	out["sites"] = s.Sites != nil && s.Sites.Enabled()
	text := jsonText(out)
	return clipService(text, 20000)
}
