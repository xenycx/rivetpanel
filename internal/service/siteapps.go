package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/reposcan"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// SiteBuilder runs a web unit's build once (the runner).
type SiteBuilder interface {
	BuildOnce(ctx context.Context, botID string, out io.Writer) error
}

// WebDeployHook finishes a deployment of a site's web unit. handled reports
// that the deployment is complete (a static build was published); otherwise
// the deployment continues as for a bot (an app restarts on the new code).
type WebDeployHook interface {
	AfterDeploy(ctx context.Context, bot domain.Bot, sha, op string) (msg string, handled bool, err error)
}

// CreateBuiltSiteInput describes a site deployed from a GitHub repository:
// published as it is, built into static files, or run as an app.
type CreateBuiltSiteInput struct {
	Name, Slug, DomainID, WorkspaceID string

	Kind      string // reposcan.WebStatic | WebStaticBuild | WebApp
	Framework string // preset id (informational)
	Repo      ConfigureInput

	Runtime        string
	BuildCommand   string
	OutputDir      string // static builds
	SPA            bool
	Argv           []string // apps
	Port           int
	HealthPath     string
	ReleaseCommand string
	MemoryBytes    int64
	NanoCPUs       int64
	MaxBodyBytes   int64
	Env            map[string]string
	Addons         []AddonInput
}

// AppsEnabled reports whether sites may be built from repositories on this
// panel; running apps also needs the panel's runner (RunsApps).
func (s *SiteService) AppsEnabled() bool {
	return s.Enabled() && s.Deploys != nil && s.Builder != nil && s.Bots != nil
}

// RunsApps reports whether app sites can run on this panel.
func (s *SiteService) RunsApps() bool { return s.AppsEnabled() && s.Bots.Notifier != nil }

func cleanOutputDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || dir == "." || dir == "./" {
		return ".", nil
	}
	root, err := cleanRoot(dir)
	if err != nil || root == "" {
		return "", domain.Invalid("output directory must be a folder inside the repository")
	}
	if strings.HasPrefix(path.Base(root), ".") && !strings.HasPrefix(root, ".output") && !strings.HasPrefix(root, ".vercel") {
		return "", domain.Invalid("output directory cannot be a hidden folder")
	}
	return root, nil
}

func cleanHealthPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/", nil
	}
	if !strings.HasPrefix(p, "/") || len(p) > 256 || strings.ContainsAny(p, " \t\r\n\x00#") {
		return "", domain.Invalid("health check path must start with / and contain no spaces")
	}
	return p, nil
}

const maxSiteBody = 4 << 30

// CreateBuilt makes a site deployed from a GitHub repository. The site gets
// a web unit (a bots row of kind web) that holds the checkout, the build,
// the environment and, for apps, the container; the first deployment is
// queued at once.
func (s *SiteService) CreateBuilt(ctx context.Context, actor domain.User, in CreateBuiltSiteInput) (domain.Site, error) {
	if !s.AppsEnabled() {
		return domain.Site{}, domain.Invalid("building and running sites is not available on this panel")
	}
	app := in.Kind == reposcan.WebApp
	if app && !s.RunsApps() {
		return domain.Site{}, domain.Invalid("running apps needs the panel's local runner (Docker)")
	}
	switch in.Kind {
	case reposcan.WebStatic, reposcan.WebStaticBuild, reposcan.WebApp:
	default:
		return domain.Site{}, domain.Invalid("site type must be static, static_build or app")
	}
	rt, ok := s.Bots.Catalog.Get(in.Runtime)
	if !ok {
		return domain.Site{}, domain.Invalid("unknown runtime")
	}
	out, err := cleanOutputDir(in.OutputDir)
	if err != nil {
		return domain.Site{}, err
	}
	health, err := cleanHealthPath(in.HealthPath)
	if err != nil {
		return domain.Site{}, err
	}
	if in.Port < 0 || in.Port > 65535 {
		return domain.Site{}, domain.Invalid("port must be between 1 and 65535")
	}
	if in.MaxBodyBytes < 0 || in.MaxBodyBytes > maxSiteBody {
		return domain.Site{}, domain.Invalid("request body limit must be between 0 and 4 GiB")
	}
	if len(in.Framework) > 32 {
		return domain.Site{}, domain.Invalid("framework id is too long")
	}
	argv, build, release, mem := in.Argv, strings.TrimSpace(in.BuildCommand), in.ReleaseCommand, in.MemoryBytes
	switch in.Kind {
	case reposcan.WebStatic:
		build = ""
		fallthrough
	case reposcan.WebStaticBuild:
		// Never started: the unit only holds the checkout and runs the build,
		// whose container gets the panel's build allowance anyway.
		if len(in.Addons) > 0 {
			return domain.Site{}, domain.Invalid("add-ons need an app site; a static site never runs")
		}
		argv, release, mem = rt.DefaultArgv, "", rt.MinMemoryBytes
		if mem == 0 {
			mem = 64 << 20
		}
	case reposcan.WebApp:
		if len(argv) == 0 {
			return domain.Site{}, domain.Invalid("an app needs a start command")
		}
	}
	if in.Kind == reposcan.WebStaticBuild && build == "" {
		return domain.Site{}, domain.Invalid("a static build needs a build command")
	}
	mode := domain.SiteModeFiles
	if app {
		mode = domain.SiteModeApp
		// The app listens where the sites listener sends traffic.
		port := in.Port
		if port == 0 {
			port = domain.DefaultAppPort
		}
		env := make(map[string]string, len(in.Env)+1)
		for k, v := range in.Env {
			env[k] = v
		}
		env["PORT"] = strconv.Itoa(port)
		in.Env, in.Port = env, port
	}
	st, err := s.Create(ctx, actor, CreateSiteInput{Name: in.Name, Slug: in.Slug, DomainID: in.DomainID, WorkspaceID: in.WorkspaceID, SPA: in.SPA})
	if err != nil {
		return domain.Site{}, err
	}
	undoSite := func() {
		if err := s.Store.DeleteSite(context.WithoutCancel(ctx), st.ID); err != nil {
			s.warn("remove a site that could not be set up", err)
		}
		_ = s.root.Remove(st.ID)
		s.reloadSoon()
	}
	b, err := s.Bots.Create(ctx, actor, CreateBotInput{
		Name: st.Name, Runtime: rt.ID, Argv: argv, MemoryBytes: mem, NanoCPUs: in.NanoCPUs, WorkspaceID: st.WorkspaceID,
		SourceType: "github", Env: in.Env, BuildCommand: build, Addons: in.Addons, Kind: domain.KindWeb, ReleaseCommand: release,
	})
	if err != nil {
		undoSite()
		return domain.Site{}, err
	}
	undo := func() {
		if err := s.Bots.delete(context.WithoutCancel(ctx), actor, b.ID, true); err != nil {
			s.warn("remove the app of a site that could not be set up", err)
		}
		undoSite()
	}
	st.AppID, st.Mode, st.Framework, st.BuildOutputDir = &b.ID, mode, in.Framework, out
	st.HealthPath, st.MaxBodyBytes = health, in.MaxBodyBytes
	if app {
		st.AppPort = in.Port
		st.BuildOutputDir, st.SPA = "", false
	}
	st.UpdatedAtMS = s.now().UnixMilli()
	if err := s.Store.UpdateSite(ctx, st); err != nil {
		undo()
		return domain.Site{}, err
	}
	if _, err := s.Deploys.configure(ctx, actor, b.ID, in.Repo); err != nil {
		undo()
		return domain.Site{}, err
	}
	if app && s.Health != nil {
		if err := s.Health.SetAppProbe(ctx, b.ID, st.Port(), health); err != nil {
			s.warn("create the app's health check", err)
		}
	}
	s.Deploys.enqueue(b.ID, DeployRequest{Trigger: "initial", ActorID: &actor.ID, StartAfter: app})
	s.reloadSoon()
	return s.Store.GetSite(ctx, st.ID)
}

// AfterDeploy builds and publishes a static site after its repository was
// deployed into its web unit. App sites are left to the runner, which
// rebuilds and restarts them on the new code.
func (s *SiteService) AfterDeploy(ctx context.Context, bot domain.Bot, sha, op string) (string, bool, error) {
	st, err := s.Store.GetSiteForApp(ctx, bot.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	if st.Mode == domain.SiteModeApp {
		return "", false, nil
	}
	ops := s.Deploys.Ops
	if bot.BuildCommand != "" {
		ops.Stage(ctx, op, "Building")
		w := ops.Output(op)
		if w == nil {
			w = nopWriteCloser{io.Discard}
		}
		err := s.Builder.BuildOnce(ctx, bot.ID, w)
		w.Close()
		if err != nil {
			var be *runner.BuildError
			if errors.As(err, &be) {
				return "", true, domain.Invalid(be.Msg)
			}
			return "", true, err
		}
	}
	ops.Stage(ctx, op, "Publishing")
	r, err := s.publishBuild(ctx, st, bot.ID, sha)
	if err != nil {
		return "", true, err
	}
	return fmt.Sprintf("Published %s (%d files)", sha[:7], r.Files), true, nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// publishBuild copies the build output directory of a web unit into a new
// release and serves it.
func (s *SiteService) publishBuild(ctx context.Context, st domain.Site, botID, sha string) (domain.SiteRelease, error) {
	src, err := s.Deploys.Files.Open(botID)
	if err != nil {
		return domain.SiteRelease{}, err
	}
	defer src.Close()
	rel, w, done, err := s.newRelease(st.ID)
	if err != nil {
		return domain.SiteRelease{}, err
	}
	ok := false
	defer func() { done(ok) }()
	_, lim := s.limits()
	dir := st.BuildOutputDir
	if dir == "" {
		dir = "."
	}
	if _, _, err := src.CopyTree(dir, w, lim); err != nil {
		var ae *filesystem.ErrArchive
		if errors.As(err, &ae) {
			if strings.HasSuffix(ae.Msg, "is not a directory") {
				return domain.SiteRelease{}, domain.Invalid("the build did not produce " + dir + "; check the output directory in the site's settings")
			}
			return domain.SiteRelease{}, domain.Invalid(ae.Msg)
		}
		return domain.SiteRelease{}, err
	}
	_ = w.Remove(filesystem.DataDir)
	label := "Build"
	if repo, err := s.Deploys.store().GetGitHubRepo(ctx, botID); err == nil {
		label = repo.FullName + "@" + repo.Branch
	}
	ref := sha
	r, err := s.finishRelease(ctx, st, rel, w, "github", &label, nil, &ref, false)
	if err != nil {
		return domain.SiteRelease{}, err
	}
	ok = true
	return r, nil
}

// DeployLatest deploys a site's repository: through its web unit (build and
// publish, or rebuild and restart the app) when it has one, otherwise as a
// plain copy of the branch into a release. sha redeploys a commit (web
// units only).
func (s *SiteService) DeployLatest(ctx context.Context, actor domain.User, id, sha string) error {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceDeveloper)
	if err != nil {
		return err
	}
	if st.AppID == nil {
		if sha != "" {
			return domain.Invalid("this site publishes its branch as it is; choose an earlier release under Releases instead")
		}
		return s.Deploy(ctx, actor, id)
	}
	if s.Deploys == nil {
		return domain.Invalid("deployments are not available on this panel")
	}
	return s.Deploys.DeployAs(ctx, actor, *st.AppID, sha, "manual")
}

// ForApp returns the site a web unit belongs to, for anyone who may see it.
func (s *SiteService) ForApp(ctx context.Context, actor domain.User, appID string) (domain.Site, error) {
	st, err := s.Store.GetSiteForApp(ctx, appID)
	if err != nil {
		return domain.Site{}, err
	}
	if _, _, err := s.siteRole(ctx, actor, st.ID); err != nil {
		return domain.Site{}, err
	}
	return st, nil
}
