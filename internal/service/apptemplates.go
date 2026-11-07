package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/xenycx/rivetpanel/internal/apptemplates"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// One-click apps: a built-in template (internal/apptemplates) is a reviewed
// Compose file plus the variables it needs. Installing one fills those
// variables in on the server (generating secrets), then imports the file
// through ImportCompose, so the app gets exactly the isolation and checks of a
// pasted Compose file. Only the template installer can grant a service one of
// the few trusted capabilities a template declares.

var installMu sync.Mutex

// AddressPort and AddressSite are how an installed app is reached.
const (
	AddressPort = "port" // a published host port
	AddressSite = "site" // a web address (site) proxied to the container
)

// TemplateInstall is a request to install a one-click app.
type TemplateInstall struct {
	TemplateID  string
	Name        string
	WorkspaceID string
	// Values are the person's answers by variable name; empty uses the
	// default or a generated value.
	Values map[string]string
	// Address is AddressPort (default) or AddressSite with Slug/DomainID.
	Address  string
	Slug     string
	DomainID string
	DryRun   bool
}

// TemplateResult is the outcome of an install (or a dry run).
type TemplateResult struct {
	Plan   ComposePlan
	Stack  *StackView
	URL    string
	SiteID string
}

// AppTemplate is a catalog entry with whether this panel can install it.
type AppTemplate struct {
	apptemplates.Template
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Images    []string `json:"images"`
}

// AppTemplates lists the catalog.
func (c *ContainerService) AppTemplates() []AppTemplate {
	list := apptemplates.List()
	out := make([]AppTemplate, 0, len(list))
	for _, t := range list {
		at := AppTemplate{Template: t, Available: true, Images: []string{}}
		if compose, err := apptemplates.Compose(t.ID); err == nil {
			for _, img := range composeImages(compose) {
				at.Images = append(at.Images, img)
				if _, err := c.Policy.Normalize(img); err != nil && at.Available {
					at.Available, at.Reason = false, "this panel does not allow the image "+img
				}
			}
		}
		if t.Web == nil && t.Console != nil && !c.TerminalAvailable() {
			at.Available, at.Reason = false, "this app is used through a terminal, which this panel cannot open"
		}
		out = append(out, at)
	}
	return out
}

// TerminalAvailable reports whether container terminals can be opened.
func (c *ContainerService) TerminalAvailable() bool { return c.Terminal != nil }

func composeImages(compose string) []string {
	var doc struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(compose), &doc) != nil {
		return nil
	}
	var out []string
	for _, s := range doc.Services {
		if s.Image != "" {
			out = append(out, s.Image)
		}
	}
	sort.Strings(out)
	return out
}

// TemplateVars resolves a template's variables: the person's value, else the
// default, else a generated one. Required values must end up non-empty.
func TemplateVars(t apptemplates.Template, values map[string]string) (map[string]string, error) {
	declared := map[string]bool{}
	out := map[string]string{}
	for _, v := range t.Vars {
		declared[v.Name] = true
		val := strings.TrimSpace(values[v.Name])
		if val == "" {
			val = v.Default
		}
		if val == "" && v.Generate != nil {
			g, err := apptemplates.Random(*v.Generate)
			if err != nil {
				return nil, err
			}
			val = g
		}
		if val == "" && v.Required {
			return nil, domain.Invalid(v.Label + " is required")
		}
		if strings.ContainsAny(val, "\r\n") {
			return nil, domain.Invalid(v.Label + " must be one line")
		}
		if err := validateEnvValue(val); err != nil {
			return nil, domain.Invalid(v.Label + ": " + err.Error())
		}
		out[v.Name] = val
	}
	for k := range values {
		if !declared[k] {
			return nil, domain.Invalid(k + " is not a setting of this app")
		}
	}
	return out, nil
}

// freeHostPort returns an unused port of the published-port range.
func (c *ContainerService) freeHostPort(ctx context.Context) (int, error) {
	used, err := c.Store.UsedBotHostPorts(ctx)
	if err != nil {
		return 0, err
	}
	s := c.Bots
	if s.LocalPublished != nil {
		if pub, err := s.LocalPublished(ctx); err == nil {
			for _, p := range pub {
				used[p.HostPort] = true
			}
		}
	}
	lo, hi := s.Limits.PortMin, s.Limits.PortMax
	if lo == 0 && hi == 0 {
		lo, hi = 20000, 29999
	}
	for p := lo; p <= hi; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, domain.Invalid(fmt.Sprintf("no free host port is left in %d-%d", lo, hi))
}

// portHost is the host people use for published ports.
func (c *ContainerService) portHost() string {
	if c.Bots.Limits.PortPublicBind && c.PublicHost != "" {
		return c.PublicHost
	}
	return "127.0.0.1"
}

// reservedVars fills the installer's own variables from an app's URL.
func reservedVars(rawURL, name string, port int) map[string]string {
	u, _ := url.Parse(rawURL)
	out := map[string]string{"APP_URL": strings.TrimRight(rawURL, "/"), "APP_PORT": strconv.Itoa(port), "STACK_NAME": name,
		"APP_SCHEME": "http", "APP_SECURE": "false", "APP_HOST": "", "APP_ORIGIN": strings.TrimRight(rawURL, "/")}
	if u != nil {
		out["APP_SCHEME"] = u.Scheme
		out["APP_HOST"] = u.Hostname()
		if u.Scheme == "https" {
			out["APP_SECURE"] = "true"
		}
		out["APP_ORIGIN"] = u.Scheme + "://" + u.Host
	}
	return out
}

// InstallTemplate installs a one-click app (or, with DryRun, previews it).
func (c *ContainerService) InstallTemplate(ctx context.Context, actor domain.User, in TemplateInstall) (TemplateResult, error) {
	var res TemplateResult
	if !actor.Can(domain.PermBotsCreate) {
		return res, domain.ErrForbidden
	}
	t, ok := apptemplates.Get(in.TemplateID)
	if !ok {
		return res, domain.ErrNotFound
	}
	compose, err := apptemplates.Compose(t.ID)
	if err != nil {
		return res, err
	}
	files, err := apptemplates.Files(t.ID)
	if err != nil {
		return res, err
	}
	if len(files) > 0 && c.Bots.Files == nil && !in.DryRun {
		return res, domain.Invalid("this app needs starter files, which this panel cannot write")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = t.ID
	}
	if _, err := validateName(name); err != nil {
		return res, err
	}
	vars, err := TemplateVars(t, in.Values)
	if err != nil {
		return res, err
	}
	// One install at a time picks its port and creates its services, so two
	// installs never choose the same free port.
	installMu.Lock()
	defer installMu.Unlock()
	port, err := c.freeHostPort(ctx)
	if err != nil {
		return res, err
	}
	address := in.Address
	if address == "" {
		address = AddressPort
	}
	var site domain.Site
	switch address {
	case AddressPort:
		res.URL = "http://" + net.JoinHostPort(c.portHost(), strconv.Itoa(port))
	case AddressSite:
		if t.Web == nil {
			return res, domain.Invalid("this app has no web interface to put at a web address")
		}
		if c.Sites == nil || !c.Sites.Enabled() {
			return res, domain.Invalid("site hosting is not enabled on this panel")
		}
		if in.DryRun {
			base, err := c.Sites.baseFor(ctx, in.DomainID)
			if err != nil {
				return res, err
			}
			slug := strings.ToLower(strings.TrimSpace(in.Slug))
			if slug == "" {
				slug = slugFrom(name)
			}
			if err := validateSlug(slug); err != nil {
				return res, err
			}
			res.URL = c.Sites.SiteURL(slug, base.Domain)
		} else {
			wsID, err := c.Bots.creatableWorkspace(ctx, actor, in.WorkspaceID)
			if err != nil {
				return res, err
			}
			if site, res.URL, err = c.Sites.ReserveContainerSite(ctx, actor, wsID, name, in.Slug, in.DomainID); err != nil {
				return res, err
			}
			res.SiteID = site.ID
		}
	default:
		return res, domain.Invalid("address must be port or site")
	}
	for k, v := range reservedVars(res.URL, name, port) {
		vars[k] = v
	}
	caps := map[string][]string{}
	for svc, sb := range t.Sandbox {
		caps[svc] = sb.CapAdd
	}
	undoSite := func() {
		if site.ID != "" {
			if err := c.Sites.deleteSite(context.WithoutCancel(ctx), site); err != nil {
				c.Sites.warn("remove the web address of an app that could not be installed", err)
			}
		}
	}
	plan, err := c.ImportCompose(ctx, actor, ComposeImport{Name: name, WorkspaceID: in.WorkspaceID, Compose: compose,
		DryRun: in.DryRun, vars: vars, templateID: t.ID, capAdd: caps})
	res.Plan = plan
	if err != nil || in.DryRun || plan.Stack == nil {
		undoSite()
		if err == nil && !in.DryRun && len(plan.Errors) > 0 {
			err = domain.Invalid("the app could not be set up: " + plan.Errors[0].Message)
		}
		return res, err
	}
	stack := plan.Stack
	undo := func() {
		undoSite()
		site = domain.Site{}
		if err := c.DeleteStack(context.WithoutCancel(ctx), actor, stack.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			_ = err // leave it for the person to delete; the error below explains
		}
	}
	bySvc := map[string]domain.Bot{}
	for _, b := range stack.Services {
		if b.Container != nil {
			bySvc[b.Container.Service] = b
		}
	}
	for _, f := range files {
		b, ok := bySvc[f.Service]
		if !ok {
			undo()
			return res, fmt.Errorf("template %s: seed file for unknown service %s", t.ID, f.Service)
		}
		data := f.Data
		if f.Expand {
			data = []byte(composeInterpolate(string(data), vars, map[string]bool{}))
		}
		if err := c.Bots.Files.WriteFile(b.ID, f.Path, bytes.NewReader(data), int64(len(data))+1); err != nil {
			undo()
			return res, fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	if site.ID != "" {
		web, ok := bySvc[t.Web.Service]
		if !ok {
			undo()
			return res, fmt.Errorf("template %s: no web service %s", t.ID, t.Web.Service)
		}
		if err := c.Sites.AttachContainer(ctx, actor, site.ID, web.ID, t.Web.Port, t.Web.Health); err != nil {
			undo()
			return res, err
		}
	}
	for _, b := range stack.Services {
		_, _ = c.Bots.Start(ctx, actor, b.ID)
	}
	v, err := c.GetStack(ctx, actor, stack.ID)
	if err != nil {
		return res, err
	}
	res.Stack = &v
	return res, nil
}

// StackTemplate describes the one-click app a stack was installed from.
type StackTemplate struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Website     string                `json:"website"`
	Notes       []string              `json:"notes"`
	Credentials []TemplateCredential  `json:"credentials"`
	Web         *apptemplates.Web     `json:"web,omitempty"`
	Console     *apptemplates.Console `json:"console,omitempty"`
	// ConsoleBotID is the service to open a terminal on.
	ConsoleBotID string `json:"console_bot_id,omitempty"`
	// WebBotID is the service that serves the web interface.
	WebBotID string `json:"web_bot_id,omitempty"`
}

// TemplateCredential is a sign-in value people can reveal: the variable of
// a service that holds it.
type TemplateCredential struct {
	Label string `json:"label"`
	BotID string `json:"bot_id"`
	Name  string `json:"name"`
}

// StackAddress describes how an installed app is reached.
func (c *ContainerService) StackAddress(ctx context.Context, v StackView) (tpl *StackTemplate, url, siteID string) {
	t, ok := apptemplates.Get(v.TemplateID)
	if v.TemplateID == "" || !ok {
		return nil, "", ""
	}
	bySvc := map[string]domain.Bot{}
	for _, b := range v.Services {
		if b.Container != nil {
			bySvc[b.Container.Service] = b
		}
	}
	tpl = &StackTemplate{ID: t.ID, Name: t.Name, Website: t.Website, Web: t.Web, Console: t.Console,
		Notes: []string{}, Credentials: []TemplateCredential{}}
	if t.Console != nil {
		tpl.ConsoleBotID = bySvc[t.Console.Service].ID
	}
	compose, _ := apptemplates.Compose(t.ID)
	refs := envRefs(compose)
	known := map[string]string{}
	for _, v := range t.Vars {
		if v.Credential {
			for _, r := range refs[v.Name] {
				if b, ok := bySvc[r.service]; ok {
					tpl.Credentials = append(tpl.Credentials, TemplateCredential{Label: v.Label, BotID: b.ID, Name: r.key})
					break
				}
			}
		}
		// Non-secret values can be shown in the notes: read them back from
		// the first service that has them (they are plain settings).
		if !v.Secret {
			known[v.Name] = v.Default
			for _, r := range refs[v.Name] {
				if b, ok := bySvc[r.service]; ok {
					if val, err := c.Bots.envValue(ctx, b, r.key); err == nil {
						known[v.Name] = val
					}
					break
				}
			}
		}
	}
	if t.Web != nil {
		web := bySvc[t.Web.Service]
		tpl.WebBotID = web.ID
		if c.Sites != nil {
			if st, ok := c.Sites.SitesForApps(ctx, []string{web.ID})[web.ID]; ok {
				url, siteID = c.Sites.URLOf(st), st.ID
			}
		}
		if url == "" {
			for _, p := range web.Ports {
				if p.ContainerPort == t.Web.Port {
					url = "http://" + net.JoinHostPort(c.portHost(), strconv.Itoa(p.HostPort))
					break
				}
			}
		}
	}
	for k, val := range reservedVars(url, v.Name, 0) {
		if k != "APP_PORT" {
			known[k] = val
		}
	}
	for _, n := range t.Notes {
		tpl.Notes = append(tpl.Notes, composeInterpolate(n, known, map[string]bool{}))
	}
	return tpl, url, siteID
}

type envRef struct{ service, key string }

// envRefs maps each ${VAR} to the service environment keys whose value is
// exactly that variable.
func envRefs(compose string) map[string][]envRef {
	var doc struct {
		Services map[string]struct {
			Environment yaml.Node `yaml:"environment"`
		} `yaml:"services"`
	}
	out := map[string][]envRef{}
	if yaml.Unmarshal([]byte(compose), &doc) != nil {
		return out
	}
	names := make([]string, 0, len(doc.Services))
	for n := range doc.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, svc := range names {
		env := doc.Services[svc].Environment
		add := func(k, v string) {
			v = strings.TrimSpace(v)
			if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") && !strings.ContainsAny(v[2:len(v)-1], ":-?+${}") {
				name := v[2 : len(v)-1]
				out[name] = append(out[name], envRef{svc, k})
			}
		}
		switch env.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(env.Content); i += 2 {
				add(env.Content[i].Value, env.Content[i+1].Value)
			}
		case yaml.SequenceNode:
			for _, it := range env.Content {
				if k, v, ok := strings.Cut(it.Value, "="); ok {
					add(k, v)
				}
			}
		}
	}
	return out
}
