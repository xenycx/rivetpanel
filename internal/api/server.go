// Package api contains the Fiber routes, middleware and error handling.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	fws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/console"
	"github.com/xenycx/rivetpanel/internal/diag"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/hostmon"
	"github.com/xenycx/rivetpanel/internal/logbuf"
	"github.com/xenycx/rivetpanel/internal/modules"
	"github.com/xenycx/rivetpanel/internal/noderoute"
	"github.com/xenycx/rivetpanel/internal/pkgmgr"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Pinger reports whether a dependency is reachable.
// SFTPInfo describes the embedded SFTP server for the settings page.
type SFTPInfo struct {
	Port        string `json:"port"`
	Fingerprint string `json:"fingerprint"`
}

type Pinger interface {
	PingContext(ctx context.Context) error
}

// Deps are the dependencies of the HTTP server. Docker is deliberately absent:
// no readiness check may claim a dependency that does not exist yet.
type Deps struct {
	Log *slog.Logger
	DB  Pinger
	UI  fs.FS // built static frontend; may be empty

	Auth          *service.AuthService // nil disables the authenticated API (tests/foundation)
	Bots          *service.BotService
	OAuth         *service.OAuthService             // nil disables OAuth routes
	SFTP          *SFTPInfo                         // nil when the SFTP server is disabled
	Analytics     *service.Analytics                // nil disables bot telemetry routes
	PublicURL     string                            // externally reachable origin, handed to bots
	Registry      *pkgmgr.Registry                  // package registry client; default used when nil
	Stats         StatsSource                       // Docker stats stream; nil disables live gauges
	Backups       *service.BackupService            // nil disables the backup routes
	Deploy        *service.DeployService            // nil disables GitHub deployment routes
	Ops           *service.Operations               // nil disables operation history routes
	Audit         *service.Audit                    // nil disables the activity record
	Schedules     *service.Scheduler                // nil disables scheduled actions
	MFA           *service.MFAService               // nil disables two-step sign-in
	Tokens        *service.TokenService             // nil disables the automation API
	Clients       *service.APIClientService         // nil disables API clients (bearer access to the whole API)
	OIDC          *service.OIDCService              // nil disables OpenID Connect sign-in
	Passkeys      *service.PasskeyService           // nil disables passkeys (WebAuthn)
	Enrollment    *service.AgentEnrollmentService   // nil disables agent enrollment
	AgentControl  AgentControl                      // nil when no agent listener is running
	Games         *service.GameService              // nil disables game servers
	Router        *noderoute.Router                 // per-node routing (remote agents); nil = local only
	Health        *service.HealthService            // nil disables application health and alert rules
	Settings      *service.SettingsService          // nil disables the setup wizard and panel settings
	Mail          *service.MailService              // nil disables email (alerts, invitations, notices)
	Notifications *service.NotificationService      // nil disables the in-panel notification inbox
	Tickets       *service.TicketService            // nil disables support tickets
	KB            *service.KBService                // nil disables the knowledgebase (help center)
	Status        *service.StatusService            // nil disables the status page
	Usage         *service.UsageService             // nil disables usage analytics
	Resets        *service.PasswordResetService     // nil disables "Forgot password?"
	Verify        *service.EmailVerificationService // nil disables email verification links
	MailPrefs     MailPrefs                         // per-account alert-email switch; nil hides it
	Sites         *service.SiteService              // nil (or not started) disables static site hosting
	AI            *service.AIService                // nil disables the AI operator
	Env           *service.PanelEnvService          // nil disables the environment editor
	Host          *hostmon.Monitor                  // nil disables the host monitoring routes
	LogArchive    *service.LogArchiveService        // nil disables the log archive routes
	Logs          *logbuf.Buffer                    // nil disables the panel log viewer
	SetupCodeFile string                            // shown by the setup wizard
	OnSetupDone   func()                            // called after the first administrator is created
	Catalog       *runtimes.Catalog
	SecureCookies bool   // Secure flag on the session cookie (production)
	ProxyHeader   string // trusted client-IP header from a loopback reverse proxy; empty = none
	MetricsToken  string // bearer token for /metrics; empty disables it
	Modules       modules.Registry

	Nodes        NodeStore           // nil disables node/telemetry routes
	Files        *filesystem.Manager // nil disables the file manager
	MaxUpload    int64               // bytes per upload; default 32 MiB
	Console      *console.Service    // nil disables the WebSocket console
	ConsoleLimit *console.Limiter
	BaseCtx      context.Context // cancelled on shutdown to end WebSocket sessions

	// Checks are extra readiness probes (e.g. Docker when the local runner is on).
	Checks []Check
	// RunnerReady reports whether the runner can act now; bots waiting on an
	// unavailable runner are shown as such. Nil means always ready.
	RunnerReady func(ctx context.Context) error
	// BuildMemory is the runner's default builder memory (shown to users).
	BuildMemory int64
	// Diagnostics runs the administrator health report; nil disables it.
	Diagnostics func(ctx context.Context) diag.Report
}

// Check is a named readiness probe.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

type panel struct {
	log           *slog.Logger
	files         *filesystem.Manager
	maxUpload     int64
	nodes         NodeStore
	console       *console.Service
	consoleLimit  *console.Limiter
	baseCtx       context.Context
	auth          *service.AuthService
	bots          *service.BotService
	oauth         *service.OAuthService
	sftp          *SFTPInfo
	analytics     *service.Analytics
	publicURL     string
	registry      *pkgmgr.Registry
	stats         StatsSource
	backups       *service.BackupService
	deploy        *service.DeployService
	ops           *service.Operations
	audit         *service.Audit
	schedules     *service.Scheduler
	mfa           *service.MFAService
	tokens        *service.TokenService
	clients       *service.APIClientService
	oidc          *service.OIDCService
	passkeys      *service.PasskeyService
	enrollment    *service.AgentEnrollmentService
	agentControl  AgentControl
	games         *service.GameService
	trustedFiles  bool // rivet-agent file routes (see NodeFiles)
	router        *noderoute.Router
	health        *service.HealthService
	settings      *service.SettingsService
	mail          *service.MailService
	notifications *service.NotificationService
	tickets       *service.TicketService
	kb            *service.KBService
	status        *service.StatusService
	usage         *service.UsageService
	logArchive    *service.LogArchiveService
	resets        *service.PasswordResetService
	verify        *service.EmailVerificationService
	mailPrefs     MailPrefs
	sites         *service.SiteService
	ai            *service.AIService
	env           *service.PanelEnvService
	host          *hostmon.Monitor
	logs          *logbuf.Buffer
	setupCodeFile string
	onSetupDone   func()
	statsLimit    *console.Limiter
	disk          diskCache
	catalog       *runtimes.Catalog
	secureCookies bool
	runnerReady   func(ctx context.Context) error
	buildMemory   int64
	diagnostics   func(ctx context.Context) diag.Report
	modules       modules.Registry
}

const readyTimeout = 2 * time.Second

// New builds the Fiber application.
func New(d Deps) *fiber.App {
	maxUpload := d.MaxUpload
	if maxUpload <= 0 {
		maxUpload = defaultMaxUpload
	}
	cfg := fiber.Config{
		AppName:      "rivetpanel",
		ErrorHandler: errorHandler(d.Log),
		// Bodies are streamed so uploads never sit in memory; every route other
		// than the two upload routes is capped at 1 MiB by smallBody below.
		StreamRequestBody: true,
		BodyLimit:         int(maxUpload) + 1<<20,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadBufferSize:    4096,
		Concurrency:       4096,
	}
	if d.ProxyHeader != "" {
		// Only a proxy on this host may set the header; remote peers cannot spoof it.
		cfg.ProxyHeader, cfg.TrustProxy = d.ProxyHeader, true
		cfg.TrustProxyConfig = fiber.TrustProxyConfig{Loopback: true}
	}
	if len(d.Modules) == 0 {
		d.Modules = modules.Default()
	}
	app := fiber.New(cfg)

	metrics := newPanelMetrics()
	if d.Host != nil && d.Host.HTTP == nil {
		d.Host.HTTP = metrics.stats
	}
	app.Use(securityHeaders, hsts(d), metrics.observe, smallBody)
	app.Get("/metrics", metrics.handler(d))
	if d.Enrollment != nil {
		agent := app.Group("/api/agent/v1")
		agent.Post("/enroll", newLimiter(limiter.Config{
			Max: 10, Expiration: time.Minute,
			KeyGenerator: func(c fiber.Ctx) string { return "agent-enroll:" + c.IP() },
			LimitReached: func(c fiber.Ctx) error {
				return fiber.NewError(fiber.StatusTooManyRequests, "too many enrollment attempts; try again later")
			},
		}), enrollAgent(d.Enrollment))
	}

	v1 := app.Group("/api/v1")
	v1.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	v1.Get("/readyz", func(c fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), readyTimeout)
		defer cancel()
		checks := fiber.Map{}
		ok := true
		probe := func(name string, fn func(context.Context) error) {
			if err := fn(ctx); err != nil {
				d.Log.Warn("readiness check failed", "check", name, "err", err)
				checks[name], ok = "unavailable", false
			} else {
				checks[name] = "ok"
			}
		}
		probe("database", d.DB.PingContext)
		for _, ch := range d.Checks {
			probe(ch.Name, ch.Fn)
		}
		if !ok {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "unavailable", "checks": checks})
		}
		return c.JSON(fiber.Map{"status": "ok", "checks": checks})
	})
	if d.Auth != nil && d.Bots != nil {
		s := &panel{log: d.Log, auth: d.Auth, bots: d.Bots, oauth: d.OAuth, sftp: d.SFTP, analytics: d.Analytics, publicURL: d.PublicURL, registry: d.Registry, stats: d.Stats, backups: d.Backups, deploy: d.Deploy, ops: d.Ops, audit: d.Audit, schedules: d.Schedules, mfa: d.MFA, tokens: d.Tokens, clients: d.Clients, oidc: d.OIDC, passkeys: d.Passkeys, enrollment: d.Enrollment, agentControl: d.AgentControl, games: d.Games, router: d.Router, health: d.Health, settings: d.Settings, mail: d.Mail, notifications: d.Notifications, tickets: d.Tickets, kb: d.KB, status: d.Status, usage: d.Usage, resets: d.Resets, verify: d.Verify, mailPrefs: d.MailPrefs, sites: d.Sites, ai: d.AI, env: d.Env, host: d.Host, logs: d.Logs, logArchive: d.LogArchive, setupCodeFile: d.SetupCodeFile, onSetupDone: d.OnSetupDone, statsLimit: console.NewLimiter(0, 0, 0), catalog: d.Catalog, secureCookies: d.SecureCookies, modules: d.Modules,
			console: d.Console, consoleLimit: d.ConsoleLimit, baseCtx: d.BaseCtx, nodes: d.Nodes, files: d.Files, maxUpload: d.MaxUpload,
			runnerReady: d.RunnerReady, buildMemory: d.BuildMemory, diagnostics: d.Diagnostics}
		if s.buildMemory == 0 {
			s.buildMemory = 768 << 20
		}
		if s.maxUpload <= 0 {
			s.maxUpload = defaultMaxUpload
		}
		if s.registry == nil {
			s.registry = &pkgmgr.Registry{}
		}
		if s.baseCtx == nil {
			s.baseCtx = context.Background()
		}
		if s.consoleLimit == nil {
			s.consoleLimit = console.NewLimiter(0, 0, 0)
		}
		s.routes(v1)
	}
	// Unknown API paths are JSON errors, never the HTML fallback.
	app.All("/api/v1/*", func(c fiber.Ctx) error { return fiber.ErrNotFound })
	app.All("/api/agent/*", func(c fiber.Ctx) error { return fiber.ErrNotFound })
	app.All("/api", func(c fiber.Ctx) error { return fiber.ErrNotFound })
	app.All("/api/*", func(c fiber.Ctx) error { return fiber.ErrNotFound })

	app.Use(staticHandler(d.UI))
	return app
}

// smallBody caps request bodies at 1 MiB (and rejects unknown-length bodies)
// everywhere except the streaming upload routes.
func smallBody(c fiber.Ctx) error {
	if c.Method() == fiber.MethodGet || c.Method() == fiber.MethodHead {
		return c.Next()
	}
	p := c.Path()
	if (c.Method() == fiber.MethodPut && strings.HasSuffix(p, "/files/content")) ||
		(c.Method() == fiber.MethodPost && strings.HasSuffix(p, "/files/extract")) ||
		(c.Method() == fiber.MethodPost && strings.HasPrefix(p, "/api/v1/sites/") && strings.HasSuffix(p, "/upload")) {
		return c.Next()
	}
	switch n := c.RequestCtx().Request.Header.ContentLength(); {
	case n < 0:
		return fiber.NewError(fiber.StatusLengthRequired, "a Content-Length is required")
	case n > 1<<20:
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "request body too large")
	}
	return c.Next()
}

// hsts sends Strict-Transport-Security (one year, without includeSubDomains,
// which could break other services on sibling names) when the panel runs in
// production (secure cookies) and its public address is https. The address
// follows changes made on the settings page.
func hsts(d Deps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if d.SecureCookies {
			u := d.OAuth.CurrentPublicURL()
			if u == "" {
				u = d.PublicURL
			}
			if len(u) > 8 && strings.EqualFold(u[:8], "https://") {
				c.Set("Strict-Transport-Security", "max-age=31536000")
			}
		}
		return c.Next()
	}
}

func securityHeaders(c fiber.Ctx) error {
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("X-Frame-Options", "DENY")
	c.Set("Referrer-Policy", "same-origin")
	return c.Next()
}

// MailPrefs stores each account's alert-email switch.
type MailPrefs interface {
	UserEmailAlerts(ctx context.Context, userID string) (bool, error)
	SetUserEmailAlerts(ctx context.Context, userID string, on bool) error
	UserEmailNews(ctx context.Context, userID string) (bool, error)
	SetUserEmailNews(ctx context.Context, userID string, on bool) error
}

func (s *panel) routes(v1 fiber.Router) {
	v1.Use(s.checkOrigin)
	v1.Post("/auth/login", newLimiter(limiter.Config{
		Max: 10, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return c.IP() },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many login attempts; try again later")
		},
	}), s.login)

	oauthLimit := newLimiter(limiter.Config{
		Max: 30, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return c.IP() },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many sign-in attempts; try again later")
		},
	})
	if s.deploy != nil {
		// GitHub authenticates deliveries with an HMAC, not a session, so there
		// is no CSRF token; the limit caps signature guessing.
		hookLimit := newLimiter(limiter.Config{
			Max: 120, Expiration: time.Minute,
			KeyGenerator: func(c fiber.Ctx) string { return c.IP() },
			LimitReached: func(c fiber.Ctx) error {
				return fiber.NewError(fiber.StatusTooManyRequests, "too many requests")
			},
		})
		v1.Post("/webhooks/github", hookLimit, s.githubWebhook)
	}
	if s.mfa != nil {
		v1.Post("/auth/mfa", newLimiter(limiter.Config{
			Max: 10, Expiration: time.Minute,
			KeyGenerator: func(c fiber.Ctx) string { return "mfa:" + c.IP() },
			LimitReached: func(c fiber.Ctx) error {
				return fiber.NewError(fiber.StatusTooManyRequests, "too many attempts; try again in a minute")
			},
		}), s.completeMFA)
	}
	if s.settings != nil {
		setupLimit := newLimiter(limiter.Config{
			Max: 10, Expiration: time.Minute,
			KeyGenerator: func(c fiber.Ctx) string { return "setup:" + c.IP() },
			LimitReached: func(c fiber.Ctx) error {
				return fiber.NewError(fiber.StatusTooManyRequests, "too many attempts; try again in a minute")
			},
		})
		v1.Get("/setup/status", s.setupStatus)
		v1.Post("/setup/check", setupLimit, s.setupCheck)
		v1.Post("/setup/complete", setupLimit, s.setupComplete)
	}
	if s.resets != nil {
		resetLimit := newLimiter(limiter.Config{
			Max: 10, Expiration: 10 * time.Minute,
			KeyGenerator: func(c fiber.Ctx) string { return "reset:" + c.IP() },
			LimitReached: func(c fiber.Ctx) error {
				return fiber.NewError(fiber.StatusTooManyRequests, "too many password reset attempts; try again in a few minutes")
			},
		})
		v1.Get("/auth/password-reset", s.resetAvailable)
		v1.Post("/auth/password-reset/request", resetLimit, s.requestPasswordReset)
		v1.Post("/auth/password-reset/confirm", resetLimit, s.confirmPasswordReset)
	}
	v1.Get("/auth/providers", s.oauthProviders)
	v1.Get("/registration", s.registrationStatus)
	v1.Post("/registration/preview", oauthLimit, s.previewAccountInvite)
	v1.Post("/registration", oauthLimit, s.registerAccount)
	v1.Get("/auth/:provider/login", oauthLimit, s.oauthLogin)
	v1.Get("/auth/:provider/callback", oauthLimit, s.oauthCallback)
	if s.oidc != nil {
		s.oidcRoutes(v1, oauthLimit)
	}
	if s.passkeys != nil {
		s.passkeyPublicRoutes(v1)
	}

	if s.analytics != nil {
		// Bots authenticate with a bearer key, not a session, so there is no
		// CSRF; the per-IP limit caps key guessing, the per-bot limit caps volume.
		botLimit := newLimiter(limiter.Config{
			Max: 300, Expiration: time.Minute,
			KeyGenerator: func(c fiber.Ctx) string { return c.IP() },
			LimitReached: func(c fiber.Ctx) error {
				return fiber.NewError(fiber.StatusTooManyRequests, "too many requests")
			},
		})
		v1.Post("/bot-telemetry", botLimit, s.botAuth, s.botTelemetryPost)
		v1.Get("/bot-telemetry/ws", botLimit, s.botAuth, fws.New(s.botTelemetryWS, fws.Config{
			ReadBufferSize: 1024, WriteBufferSize: 1024, AllowEmptyOrigin: true,
		}))
	}

	s.publicVerifyRoutes(v1)
	if s.kb != nil {
		s.kbPublicRoutes(v1)
	}
	if s.status != nil {
		s.statusPublicRoutes(v1)
	}
	if s.tokens != nil {
		s.automationRoutes(v1)
	}
	authed := v1.Group("", s.requireAuth, clientLimit(), s.auditMW)
	if s.ai != nil {
		s.aiRoutes(authed)
	}
	registryLimit := newLimiter(limiter.Config{
		Max: 60, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return "reg:" + currentUser(c).ID },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many registry lookups; try again shortly")
		},
	})
	authed.Get("/templates", s.listTemplates)
	// Lookups of other people's repositories spend the panel's (or the
	// person's) GitHub API budget.
	ghLimit := newLimiter(limiter.Config{
		Max: 30, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return "gh:" + currentUser(c).ID },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many repository lookups; try again shortly")
		},
	})
	if s.deploy != nil {
		authed.Get("/me/github/repos", s.githubRepos)
		authed.Get("/me/github/branches", ghLimit, s.githubBranches)
		authed.Get("/github/lookup", ghLimit, s.githubLookup)
		authed.Post("/github/analyze", ghLimit, s.analyzeGitHub)
		authed.Get("/github/recipes", s.listRecipes)
		authed.Get("/bots/:id/github", s.requirePerm(domain.PermBotsDeploy), s.getGitHub)
		authed.Put("/bots/:id/github", s.requirePerm(domain.PermBotsDeploy), s.putGitHub)
		authed.Delete("/bots/:id/github", s.requirePerm(domain.PermBotsDeploy), s.deleteGitHub)
		authed.Post("/bots/:id/github/deploy", s.requirePerm(domain.PermBotsDeploy), s.deployGitHub)
		authed.Get("/bots/:id/github/preview", s.requirePerm(domain.PermBotsDeploy), s.previewGitHub)
		authed.Get("/me/github/owners", s.githubOwners)
		authed.Get("/bots/:id/github/push-plan", s.requirePerm(domain.PermBotsDeploy), s.pushPlan)
		authed.Post("/bots/:id/github/publish", s.requirePerm(domain.PermBotsDeploy), s.publishGitHub)
		authed.Post("/bots/:id/github/push", s.requirePerm(domain.PermBotsDeploy), s.pushGitHub)
	}
	if s.tokens != nil {
		authed.Get("/me/tokens", s.listTokens)
		authed.Post("/me/tokens", s.requirePerm(domain.PermAPIKeys), s.createToken)
		authed.Delete("/me/tokens/:id", s.deleteToken)
	}
	if s.clients != nil {
		s.apiClientRoutes(authed)
	}
	if s.notifications != nil {
		s.notificationRoutes(authed)
	}
	if s.tickets != nil {
		s.ticketRoutes(authed)
	}
	if s.kb != nil {
		s.kbManageRoutes(authed)
	}
	if s.status != nil {
		s.statusManageRoutes(authed)
	}
	if s.oidc != nil {
		s.oidcAuthedRoutes(authed)
	} else {
		authed.Get("/me/identities", s.listIdentities)
	}
	authed.Get("/me/capacity", s.capacity)
	authed.Get("/me/sftp", s.sftpInfo)
	authed.Get("/me/api-keys", s.listAPIKeys)
	authed.Post("/me/api-keys", s.requirePerm(domain.PermAPIKeys), s.createAPIKey)
	authed.Delete("/me/api-keys/:id", s.deleteAPIKey)
	authed.Get("/me/connections", s.listConnections)
	authed.Post("/me/connections/:provider/start", s.startConnection)
	authed.Delete("/me/connections/:provider", s.deleteConnection)
	authed.Post("/auth/logout", s.logout)
	passwordLimit := newLimiter(limiter.Config{
		Max: 10, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return "pw:" + currentUser(c).ID },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many attempts; try again in a minute")
		},
	})
	authed.Get("/me/sessions", s.listSessions)
	authed.Put("/me/profile", s.updateProfile)
	if s.mailPrefs != nil {
		authed.Put("/me/email-alerts", s.putEmailAlerts)
		authed.Put("/me/email-news", s.putEmailNews)
	}
	authed.Get("/users/:id/avatar", s.userAvatar)
	authed.Delete("/me/sessions/:sid", s.revokeSession)
	authed.Post("/me/sessions/revoke-others", s.revokeOtherSessions)
	authed.Post("/me/password", passwordLimit, s.changePassword)
	if s.mfa != nil {
		authed.Get("/me/mfa", s.mfaStatus)
		authed.Post("/me/mfa/setup", passwordLimit, s.mfaSetup)
		authed.Post("/me/mfa/enable", passwordLimit, s.mfaEnable)
		authed.Post("/me/mfa/disable", passwordLimit, s.mfaDisable)
		authed.Post("/me/mfa/recovery-codes", passwordLimit, s.mfaRecoveryCodes)
	}
	if s.passkeys != nil {
		s.passkeyRoutes(authed, passwordLimit)
	}
	authed.Get("/auth/me", s.me)
	s.verifyRoutes(authed)
	authed.Get("/runtimes", s.listRuntimes)
	authed.Get("/modules", s.listModules)

	authed.Get("/workspaces", s.listWorkspaces)
	authed.Post("/workspaces", s.requirePerm(domain.PermWorkspacesCreate), s.createWorkspace)
	authed.Get("/workspaces/:wid", s.getWorkspace)
	authed.Patch("/workspaces/:wid", s.patchWorkspace)
	authed.Delete("/workspaces/:wid", s.deleteWorkspace)
	authed.Put("/workspaces/:wid/members", s.addWorkspaceMember)
	authed.Patch("/workspaces/:wid/members/:uid", s.patchWorkspaceMember)
	authed.Delete("/workspaces/:wid/members/:uid", s.removeWorkspaceMember)
	authed.Put("/bots/:id/workspace", s.moveBot)
	authed.Get("/admin/workspaces", s.requirePerm(domain.PermWorkspacesView), s.adminListWorkspaces)
	authed.Get("/admin/workspaces/:wid", s.requirePerm(domain.PermWorkspacesView), s.adminGetWorkspace)
	authed.Get("/admin/users/:id", s.requirePerm(domain.PermUsersView), s.adminGetUser)

	authed.Get("/sites-info", s.sitesInfo)
	if s.sites.Enabled() {
		authed.Get("/bots/:id/site", s.getBotSite)
		authed.Post("/bots/:id/site", s.requirePerm(domain.PermSitesCreate), s.createBotSite)
		authed.Get("/sites", s.listSites)
		authed.Post("/sites", s.requirePerm(domain.PermSitesCreate), s.createSite)
		authed.Get("/site-templates", s.listSiteTemplates)
		authed.Get("/site-templates/:tid/preview", s.previewSiteTemplate)
		authed.Get("/sites/:sid", s.getSite)
		authed.Patch("/sites/:sid", s.patchSite)
		authed.Delete("/sites/:sid", s.deleteSite)
		authed.Get("/sites/:sid/icon", s.getSiteIcon)
		authed.Put("/sites/:sid/logo", s.putSiteLogo)
		authed.Delete("/sites/:sid/logo", s.deleteSiteLogo)
		authed.Post("/sites/:sid/upload", s.uploadSite)
		authed.Get("/sites/:sid/files", s.listFiles)
		authed.Get("/sites/:sid/files/content", s.readFile)
		authed.Put("/sites/:sid/files/content", s.writeFile)
		authed.Delete("/sites/:sid/files", s.deleteFile)
		authed.Post("/sites/:sid/files/mkdir", s.mkdirFile)
		authed.Post("/sites/:sid/files/move", s.moveFile)
		authed.Post("/sites/:sid/files/extract", s.extractZip)
		authed.Post("/sites/:sid/files/publish", s.publishSiteFiles)
		authed.Post("/sites/:sid/deploy", s.deploySite)
		authed.Post("/sites/:sid/releases/:rid/activate", s.activateRelease)
		authed.Post("/sites/:sid/domains", s.addSiteDomain)
		authed.Post("/sites/:sid/domains/:domain/verify", s.verifySiteDomain)
		authed.Delete("/sites/:sid/domains/:domain", s.removeSiteDomain)
		authed.Get("/admin/sites", s.requirePerm(domain.PermSitesManage), s.adminListSites)
		authed.Patch("/admin/sites/:sid", s.requirePerm(domain.PermSitesManage), s.adminPatchSite)
		authed.Get("/admin/site-base-domains", s.requirePerm(domain.PermSitesManage), s.adminListBaseDomains)
		authed.Post("/admin/site-base-domains", s.requirePerm(domain.PermSitesManage), s.adminAddBaseDomain)
		authed.Post("/admin/site-base-domains/:domain/verify", s.requirePerm(domain.PermSitesManage), s.adminVerifyBaseDomain)
		authed.Post("/admin/site-base-domains/:domain/move-sites", s.requirePerm(domain.PermSitesManage), s.adminMoveBaseDomainSites)
		authed.Patch("/admin/site-base-domains/:domain", s.requirePerm(domain.PermSitesManage), s.adminPatchBaseDomain)
		authed.Delete("/admin/site-base-domains/:domain", s.requirePerm(domain.PermSitesManage), s.adminDeleteBaseDomain)
	}

	authed.Post("/users", s.requirePerm(domain.PermUsersManage), s.createUser)
	authed.Get("/users", s.requirePerm(domain.PermUsersView), s.listUsers)
	authed.Patch("/users/:id", s.requirePerm(domain.PermUsersManage), s.patchUser)
	authed.Get("/admin/account-invites", s.requirePerm(domain.PermUsersManage), s.listAccountInvites)
	authed.Post("/admin/account-invites", s.requirePerm(domain.PermUsersManage), s.createAccountInvite)
	authed.Delete("/admin/account-invites/:id", s.requirePerm(domain.PermUsersManage), s.deleteAccountInvite)
	s.roleRoutes(authed)

	if s.diagnostics != nil {
		authed.Get("/admin/diagnostics", s.requirePerm(domain.PermSystemView), s.getDiagnostics)
	}
	if s.settings != nil {
		authed.Get("/admin/settings", s.requirePerm(domain.PermSettingsManage), s.getSettings)
		authed.Put("/admin/settings", s.requirePerm(domain.PermSettingsManage), s.putSettings)
		if s.mail != nil {
			authed.Post("/admin/settings/mail/test", s.requirePerm(domain.PermSettingsManage), s.testMail)
			authed.Get("/admin/mail/audience", s.requirePerm(domain.PermMailAnnounce), s.mailAudience)
			authed.Post("/admin/mail/announcements", s.requirePerm(domain.PermMailAnnounce), s.sendAnnouncement)
		}
	}
	if s.env != nil {
		authed.Get("/admin/environment", s.requireAdmin, s.getEnvironment)
		authed.Put("/admin/environment", s.requireAdmin, s.putEnvironment)
		authed.Post("/admin/environment/restart", s.requireAdmin, s.restartPanel)
	}
	if s.nodes != nil {
		nodes := authed.Group("/nodes", s.requirePerm(domain.PermNodesManage))
		nodes.Get("", s.listNodes)
		nodes.Patch("/:id", s.patchNode)
		nodes.Delete("/:id", s.deleteNode)
		nodes.Post("/:id/revoke-certificates", s.revokeNodeCertificates)
		nodes.Get("/locations", s.listLocations)
		nodes.Post("/locations", s.createLocation)
		nodes.Patch("/locations/:id", s.patchLocation)
		nodes.Delete("/locations/:id", s.deleteLocation)
		if s.enrollment != nil {
			nodes.Post("/enrollments", s.createAgentEnrollment)
			nodes.Post("/:id/enrollment", s.reissueAgentEnrollment)
		}
		nodes.Get("/:id/telemetry", s.nodeTelemetry)
		nodes.Get("/:id/history", s.nodeHistory)
	}
	if s.host != nil {
		authed.Get("/admin/host", s.requirePerm(domain.PermSystemView), s.hostSnapshot)
		authed.Get("/admin/host/bots", s.requirePerm(domain.PermSystemView), s.hostBots)
	}
	if s.logs != nil {
		authed.Get("/admin/logs", s.requirePerm(domain.PermSystemView), s.panelLogs)
	}
	s.logArchiveRoutes(authed)

	s.gameRoutes(authed)
	authed.Post("/bots/:id/command", s.requirePerm(domain.PermBotsPower), s.sendCommand)
	authed.Post("/bots", s.requirePerm(domain.PermBotsCreate), s.createBot)
	authed.Post("/bots/batch", s.requirePerm(domain.PermBotsPower), s.batchBots)
	authed.Get("/bots", s.listBots)
	authed.Get("/bots/:id", s.getBot)
	authed.Patch("/bots/:id", s.patchBot)
	authed.Delete("/bots/:id", s.requirePerm(domain.PermBotsDelete), s.deleteBot)
	authed.Post("/bots/:id/start", s.requirePerm(domain.PermBotsPower), s.lifecycle(func(b *service.BotService, c fiber.Ctx, id string) (domain.Bot, error) {
		return b.Start(c.Context(), currentUser(c), id)
	}))
	authed.Post("/bots/:id/stop", s.requirePerm(domain.PermBotsPower), s.lifecycle(func(b *service.BotService, c fiber.Ctx, id string) (domain.Bot, error) {
		return b.Stop(c.Context(), currentUser(c), id)
	}))
	authed.Post("/bots/:id/restart", s.requirePerm(domain.PermBotsPower), s.lifecycle(func(b *service.BotService, c fiber.Ctx, id string) (domain.Bot, error) {
		return b.Restart(c.Context(), currentUser(c), id)
	}))
	authed.Post("/bots/:id/kill", s.requirePerm(domain.PermBotsPower), s.lifecycle(func(b *service.BotService, c fiber.Ctx, id string) (domain.Bot, error) {
		return b.Kill(c.Context(), currentUser(c), id)
	}))
	authed.Put("/bots/:id/ports", s.setPorts)
	authed.Get("/bots/:id/logo", s.getBotLogo)
	authed.Put("/bots/:id/logo", s.putBotLogo)
	authed.Delete("/bots/:id/logo", s.deleteBotLogo)
	authed.Post("/bots/:id/logo/discord", s.discordBotLogo)
	authed.Get("/addons", s.listAddonKinds)
	authed.Get("/bots/:id/addons", s.listBotAddons)
	authed.Post("/bots/:id/addons", s.addBotAddon)
	authed.Patch("/bots/:id/addons/:kind", s.patchBotAddon)
	authed.Delete("/bots/:id/addons/:kind", s.deleteBotAddon)
	authed.Post("/bots/:id/addons/:kind/reveal", s.revealBotAddon)
	authed.Get("/bots/:id/addons/:kind/logs", s.botAddonLogs)
	authed.Put("/bots/:id/tags", s.setTags)
	authed.Put("/bots/:id/favorite", s.setFavorite)
	if s.console != nil {
		authed.Get("/bots/:id/console", s.requirePerm(domain.PermBotsConsole), s.consoleGuard, fws.New(s.consoleWS, fws.Config{
			ReadBufferSize: 1024, WriteBufferSize: 4096, AllowEmptyOrigin: true,
		}))
	}
	if s.files != nil {
		authed.Get("/bots/:id/stats/stream", s.statsStream)
		authed.Get("/bots/:id/packages", s.requirePerm(domain.PermBotsFiles), s.listPackages)
		authed.Put("/bots/:id/packages", s.requirePerm(domain.PermBotsFiles), s.editPackages)
		authed.Get("/bots/:id/packages/search", s.requirePerm(domain.PermBotsFiles), registryLimit, s.searchPackages)
		authed.Get("/bots/:id/packages/latest", s.requirePerm(domain.PermBotsFiles), registryLimit, s.latestPackage)
		authed.Get("/bots/:id/files", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.listFiles)
		authed.Get("/bots/:id/files/content", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.readFile)
		authed.Put("/bots/:id/files/content", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.writeFile)
		authed.Delete("/bots/:id/files", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.deleteFile)
		authed.Post("/bots/:id/files/mkdir", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.mkdirFile)
		authed.Post("/bots/:id/files/move", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.moveFile)
		authed.Post("/bots/:id/files/extract", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.extractZip)
		authed.Post("/bots/:id/files/compress", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.compressFiles)
		authed.Post("/bots/:id/files/decompress", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.decompressFile)
		authed.Get("/bots/:id/files/zip", s.requirePerm(domain.PermBotsFiles), s.forwardFiles, s.downloadZip)
	}
	if s.usage != nil {
		authed.Get("/bots/:id/usage", s.botUsage)
		authed.Get("/admin/analytics", s.requirePerm(domain.PermAnalyticsView), s.adminAnalytics)
	}
	if s.analytics != nil {
		authed.Get("/sdk/:lang", s.sdkFile)
		authed.Get("/bots/:id/analytics", s.botAnalytics)
		authed.Delete("/bots/:id/widgets/:key", s.deleteBotWidget)
		authed.Post("/bots/:id/telemetry-key", s.rotateTelemetryKey)
		authed.Delete("/bots/:id/telemetry-key", s.revokeTelemetryKey)
	}
	if s.backups != nil {
		authed.Get("/bots/:id/backups", s.requirePerm(domain.PermBotsBackups), s.listBackups)
		authed.Post("/bots/:id/backups", s.requirePerm(domain.PermBotsBackups), s.createBackup)
		authed.Get("/bots/:id/backups/:bid/download", s.requirePerm(domain.PermBotsBackups), s.downloadBackup)
		authed.Post("/bots/:id/backups/:bid/restore", s.requirePerm(domain.PermBotsBackups), s.restoreBackup)
		authed.Delete("/bots/:id/backups/:bid", s.requirePerm(domain.PermBotsBackups), s.deleteBackup)
		authed.Patch("/bots/:id/backups/:bid", s.requirePerm(domain.PermBotsBackups), s.patchBackup)
		authed.Post("/bots/:id/backups/:bid/verify", s.requirePerm(domain.PermBotsBackups), s.verifyBackup)
	}
	if s.ops != nil {
		authed.Get("/operations", s.listActivity)
		authed.Get("/bots/:id/operations", s.listBotOperations)
		authed.Get("/bots/:id/operations/:op", s.getOperation)
		authed.Get("/bots/:id/operations/:op/output", s.operationOutput)
	}
	if s.health != nil {
		authed.Get("/bots/:id/health", s.getHealth)
		authed.Get("/bots/:id/health-probe", s.getHealthProbe)
		authed.Put("/bots/:id/health-probe", s.putHealthProbe)
		authed.Put("/bots/:id/alerts", s.putAlerts)
		authed.Post("/bots/:id/alerts/test", s.testAlert)
	}
	if s.schedules != nil {
		authed.Get("/schedules/preview", s.previewSchedule)
		authed.Get("/bots/:id/schedules", s.listSchedules)
		authed.Post("/bots/:id/schedules", s.createSchedule)
		authed.Patch("/bots/:id/schedules/:sid", s.patchSchedule)
		authed.Delete("/bots/:id/schedules/:sid", s.deleteSchedule)
		authed.Post("/bots/:id/schedules/:sid/run", s.runSchedule)
	}
	if s.audit != nil {
		authed.Get("/activity/changes", s.visibleAudit)
		authed.Get("/bots/:id/changes", s.botAudit)
	}
	authed.Post("/bots/:id/transfer", s.requirePerm(domain.PermBotsShare), s.transferBot)
	authed.Get("/bots/:id/invites", s.requirePerm(domain.PermBotsShare), s.listInvites)
	authed.Post("/bots/:id/invites", s.requirePerm(domain.PermBotsShare), s.createInvite)
	authed.Delete("/bots/:id/invites/:iid", s.requirePerm(domain.PermBotsShare), s.deleteInvite)
	authed.Post("/invites/preview", s.previewInvite)
	authed.Post("/invites/accept", s.acceptInvite)
	authed.Get("/bots/:id/users", s.listSubUsers)
	authed.Put("/bots/:id/users", s.requirePerm(domain.PermBotsShare), s.shareBot)
	authed.Delete("/bots/:id/users/:uid", s.unshareBot)
	authed.Get("/bots/:id/env", s.requirePerm(domain.PermBotsEnv), s.listEnv)
	authed.Put("/bots/:id/env", s.requirePerm(domain.PermBotsEnv), s.setEnv)
	authed.Post("/bots/:id/env/:name/reveal", s.requirePerm(domain.PermBotsEnv), s.revealEnv)
	authed.Delete("/bots/:id/env/:name", s.requirePerm(domain.PermBotsEnv), s.deleteEnv)
}

// currentPublicURL follows changes made on the settings page.
func (s *panel) currentPublicURL() string {
	if s.oauth != nil {
		if u := s.oauth.CurrentPublicURL(); u != "" {
			return u
		}
	}
	return s.publicURL
}
