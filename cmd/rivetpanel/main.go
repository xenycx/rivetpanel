// Command rivetpanel runs the API, the embedded UI and the optional local runner.
//
//	rivetpanel                       serve
//	rivetpanel create-admin EMAIL    create an administrator (password from
//	                               RIVET_ADMIN_PASSWORD or a hidden prompt)
//	rivetpanel reset-password EMAIL  set a new password (RIVET_ADMIN_PASSWORD or a
//	                               hidden prompt) and sign the account out everywhere
//	rivetpanel reset-mfa EMAIL       remove two-step sign-in from an account
//	rivetpanel keygen [--agent-ca] [ID]
//	                               create an encryption key file and the agent
//	                               certificate authority in the key dir
//	                               (--agent-ca: only the certificate authority)
//	rivetpanel backup [--include-keys] DEST
//	rivetpanel backup-verify DIR     check a backup against its manifest checksums
//	rivetpanel restore [--force] [--restore-keys] SRC   (panel must be stopped)
//	rivetpanel verify                trial-decrypt every stored environment value
//	rivetpanel reseal                re-encrypt every sealed value with the active key
//	                               (panel stopped; resumable)
//	rivetpanel doctor                read-only health checks (Docker, disk, keys, ...)
//	rivetpanel env [reset [NAME]]    show, or drop, the environment overrides saved
//	                               from the administration page (recovery path)
//	rivetpanel agent-token create|list|revoke|reissue|discard
//	                               manage one-use agent enrollment
//	rivetpanel health                ask the running panel's /api/v1/healthz
//	                               (the container image's HEALTHCHECK)
//	rivetpanel version               print the build version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata" // schedules need time zones on hosts without a zoneinfo database

	"github.com/gofiber/fiber/v3"
	"golang.org/x/term"

	"github.com/xenycx/rivetpanel/blueprints"
	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/agenthub"
	"github.com/xenycx/rivetpanel/internal/api"
	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/backup"
	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/console"
	"github.com/xenycx/rivetpanel/internal/diag"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/hostmon"
	"github.com/xenycx/rivetpanel/internal/logarchive"
	"github.com/xenycx/rivetpanel/internal/logbuf"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/noderoute"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/oplog"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/sftpd"
	"github.com/xenycx/rivetpanel/internal/sitehost"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
	"github.com/xenycx/rivetpanel/internal/telemetry"
	"github.com/xenycx/rivetpanel/internal/webui"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

// restartRequested is set when an administrator restarts the panel from the
// browser; main then exits with restartExitCode so the supervisor starts it
// again (a clean exit would be treated as a deliberate stop by systemd's
// Restart=on-failure).
var restartRequested atomic.Bool

const restartExitCode = 75 // EX_TEMPFAIL

// panelLogSink writes the panel's own log lines to day files once the log
// archive is set up (see service.LogArchiveService); until then, and when
// an administrator turns it off, it drops them. stderr always gets them.
var panelLogSink = &logarchive.Sink{}

func main() {
	logs := logbuf.New(logbuf.DefaultCapacity)
	log := slog.New(logs.Handler(slog.NewJSONHandler(io.MultiWriter(os.Stderr, panelLogSink), nil)))
	var err error
	switch {
	case len(os.Args) >= 2 && os.Args[1] == "create-admin":
		err = createAdmin(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "reset-password":
		err = resetPassword(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "reset-mfa":
		err = resetMFA(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "keygen":
		err = keygen(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "backup":
		err = backupCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "backup-verify":
		err = backupVerifyCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "restore":
		err = restoreCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "reseal":
		err = resealCmd()
	case len(os.Args) >= 2 && os.Args[1] == "verify":
		err = verifyCmd()
	case len(os.Args) >= 2 && os.Args[1] == "version":
		fmt.Println("rivetpanel", version)
	case len(os.Args) >= 2 && os.Args[1] == "doctor":
		err = doctorCmd()
	case len(os.Args) >= 2 && os.Args[1] == "health":
		err = healthCmd()
	case len(os.Args) >= 2 && os.Args[1] == "env":
		err = envCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "agent-token":
		err = agentTokenCmd(os.Args[2:])
	case len(os.Args) == 1:
		err = serve(log, logs)
	default:
		err = fmt.Errorf("unknown command %q (see the package comment for usage)", os.Args[1])
	}
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	if restartRequested.Load() {
		log.Info("exiting so the supervisor restarts the panel", "code", restartExitCode)
		os.Exit(restartExitCode)
	}
}

func openDB(ctx context.Context, cfg config.Config) (*sqlite.DB, error) {
	db, err := sqlite.Open(ctx, cfg.DBPath, cfg.DBMaxOpenConns)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := db.EnsureLocalNode(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("register local node: %w", err)
	}
	return db, nil
}

func keygen(args []string) error {
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	caOnly := fs.Bool("agent-ca", false, "only create the agent certificate authority (existing installations)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*caOnly {
		id := cfg.ActiveKeyID
		if fs.NArg() > 0 {
			id = fs.Arg(0)
		}
		if err := secrets.GenerateKeyFile(cfg.KeyDir, id); err != nil {
			return err
		}
		fmt.Printf("created %s/%s.key (mode 0600). Back it up separately from the database; without it, stored environment values cannot be decrypted.\n", cfg.KeyDir, id)
	}
	// The agent CA is created here, by the administrator, because the
	// systemd unit keeps the key directory read-only for the running panel.
	// LoadOrCreate keeps an existing CA.
	caDir := filepath.Join(cfg.KeyDir, "agent-ca")
	if _, err := agentcert.LoadOrCreate(caDir); err != nil {
		return fmt.Errorf("agent certificate authority: %w", err)
	}
	fmt.Printf("agent certificate authority ready in %s (used only when remote nodes are enabled).\n", caDir)
	return nil
}

func createAdmin(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: rivetpanel create-admin EMAIL")
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	pw := os.Getenv("RIVET_ADMIN_PASSWORD")
	if pw == "" {
		fmt.Fprint(os.Stderr, "Password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("read password (set RIVET_ADMIN_PASSWORD when not on a terminal): %w", err)
		}
		pw = string(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := &service.AuthService{Store: db, Hasher: auth.NewHasher(1), TTL: cfg.SessionTTL}
	u, err := svc.CreateUser(ctx, args[0], pw, domain.RoleAdmin)
	if err != nil {
		return err
	}
	fmt.Printf("created administrator %s (%s)\n", u.Email, u.ID)
	return nil
}

// readSecret returns RIVET_ADMIN_PASSWORD or reads a hidden line.
func readSecret(prompt string) (string, error) {
	if pw := os.Getenv("RIVET_ADMIN_PASSWORD"); pw != "" {
		return pw, nil
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password (set RIVET_ADMIN_PASSWORD when not on a terminal): %w", err)
	}
	return string(b), nil
}

// resetPassword is the recovery path for a locked-out account (including
// the only administrator). It needs shell access to the host.
func resetPassword(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: rivetpanel reset-password EMAIL")
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	pw, err := readSecret("New password: ")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := &service.AuthService{Store: db, Hasher: auth.NewHasher(1), TTL: cfg.SessionTTL}
	u, err := svc.ResetPassword(ctx, args[0], pw)
	if err != nil {
		return err
	}
	fmt.Printf("password changed for %s; every session of this account was signed out\n", u.Email)
	return nil
}

// resetMFA removes two-step sign-in from an account (lost phone and lost
// recovery codes). It needs shell access to the host.
func resetMFA(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: rivetpanel reset-mfa EMAIL")
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := &service.MFAService{Store: db, Auth: &service.AuthService{Store: db}}
	u, err := svc.Reset(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("two-step sign-in removed for %s; every session of this account was signed out\n", u.Email)
	return nil
}

func backupCmd(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	incl := fs.Bool("include-keys", false, "also copy encryption keys (store them separately from the rest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: rivetpanel backup [--include-keys] DEST")
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	m, err := backup.Create(ctx, backup.Source{DB: db, DataRoot: cfg.DataRoot, KeyDir: cfg.KeyDir}, fs.Arg(0), *incl, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("backup written to %s: database snapshot, %d bot workspace(s)\n", fs.Arg(0), m.Bots)
	if m.IncludesKeys {
		fmt.Println("WARNING: this directory contains encryption keys AND the database; protect it accordingly.")
		if m.IncludesAgentCA {
			fmt.Println("The agent certificate authority is included; it can mint node identities.")
		}
	} else {
		fmt.Printf("Encryption keys were NOT included (key ids in use: %v). Back them up separately;\nwithout them stored environment values cannot be restored.\n", m.KeyIDs)
	}
	return nil
}

func backupVerifyCmd(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: rivetpanel backup-verify DIR")
	}
	m, err := backup.Verify(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("backup OK: %d bot workspace(s), keys included: %v, agent CA included: %v\n", m.Bots, m.IncludesKeys, m.IncludesAgentCA)
	return nil
}

func restoreCmd(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	force := fs.Bool("force", false, "replace an existing database and bot directories")
	keys := fs.Bool("restore-keys", false, "also restore keys included in the backup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: rivetpanel restore [--force] [--restore-keys] SRC")
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	m, err := backup.Restore(ctx, fs.Arg(0), backup.RestoreOptions{DBPath: cfg.DBPath, DataRoot: cfg.DataRoot, KeyDir: cfg.KeyDir, Force: *force, RestoreKeys: *keys})
	if err != nil {
		return err
	}
	fmt.Printf("restored database and %d bot workspace(s)\n", m.Bots)
	if *keys && m.IncludesAgentCA {
		fmt.Println("restored the agent certificate authority")
	}
	return verifyCmd()
}

// verifyCmd reports whether every stored environment value decrypts with the
// available keys.
func verifyCmd() error {
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	kr, err := secrets.LoadDir(cfg.KeyDir, cfg.ActiveKeyID, false)
	if err != nil {
		// No usable keys at all: still report what is missing.
		fmt.Printf("no usable encryption keys in %s: %v\n", cfg.KeyDir, err)
		kr, _ = secrets.NewKeyring("none", map[string][]byte{"none": make([]byte, 32)})
	}
	rep, err := backup.VerifyEnv(ctx, db, kr)
	if err != nil {
		return err
	}
	fmt.Printf("sealed values (environment, OAuth tokens, webhooks): %d total, %d decrypt correctly\n", rep.Total, rep.OK)
	if len(rep.MissingKeys) > 0 {
		fmt.Printf("MISSING KEYS (restore these key files): %v\n", rep.MissingKeys)
	}
	if rep.Failed > 0 {
		fmt.Printf("%d value(s) failed authentication (wrong key material or tampering)\n", rep.Failed)
	}
	if !rep.Healthy() {
		return errors.New("some environment values cannot be decrypted")
	}
	return nil
}

// resealCmd moves every sealed value (environment variables, OAuth tokens,
// Discord webhooks, GitHub webhook secrets, TOTP secrets) to the active key,
// so an old key can eventually be retired. Old keys stay needed for per-bot
// backup archives made before the reseal; the command does not touch those.
func resealCmd() error {
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	unlock, err := lockInstallation(cfg.DBPath) // the panel must be stopped
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	kr, err := secrets.LoadDir(cfg.KeyDir, cfg.ActiveKeyID, false)
	if err != nil {
		return fmt.Errorf("encryption keys: %w", err)
	}
	st, err := db.Reseal(ctx, func(ns, name, keyID string, ct, nonce []byte) ([]byte, []byte, string, bool, error) {
		out, changed, err := kr.Reseal(ns, name, secrets.Sealed{Ciphertext: ct, Nonce: nonce, KeyID: keyID})
		return out.Ciphertext, out.Nonce, out.KeyID, changed, err
	})
	fmt.Printf("sealed values: %d checked, %d re-encrypted with key %q, %d could not be decrypted\n", st.Seen, st.Changed, kr.Active(), st.Failed)
	if err != nil {
		return err
	}
	if st.Failed > 0 {
		return errors.New("some values could not be decrypted (missing key files?); run rivetpanel verify for details")
	}
	fmt.Println("Keep the old key files while per-bot backups made before now exist: their environment is sealed with the key of that time.")
	return nil
}

func loadCatalog(cfg config.Config) (*runtimes.Catalog, error) {
	var fsys fs.FS = rtdefaults.FS
	if cfg.RuntimesDir != "" {
		fsys = os.DirFS(cfg.RuntimesDir)
	}
	return runtimes.Load(fsys)
}

func serve(log *slog.Logger, logs *logbuf.Buffer) error {
	// Before the heap grows: keep it out of transparent huge pages (see
	// memory_linux.go).
	thpOff := disableTransparentHugePages()
	// A soft ceiling makes the collector work harder near the footprint target.
	// It is not a hard cap (stacks, cgo-free runtime overhead and mapped files
	// are outside it); an explicit GOMEMLIMIT always wins.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(48 << 20)
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One panel per installation: a second process on the same database would
	// reconcile the same containers and corrupt each other's decisions.
	unlock, err := lockInstallation(cfg.DBPath)
	if err != nil {
		return err
	}
	defer unlock()
	started := time.Now()

	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := openDB(startCtx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	keys, err := secrets.LoadDir(cfg.KeyDir, cfg.ActiveKeyID, !cfg.Production)
	if err != nil {
		return fmt.Errorf("encryption keys: %w", err)
	}
	// Everything above is read from the environment alone (database, keys); the
	// settings an administrator saved on the Environment page apply from here on.
	envSvc := &service.PanelEnvService{Store: db, Keys: keys, Base: processEnv, Log: log,
		CanRestart: supervised(), Restart: func() { restartRequested.Store(true); stop() }}
	cfg = effectiveConfig(startCtx, cfg, envSvc, log)
	catalog, err := loadCatalog(cfg)
	if err != nil {
		return fmt.Errorf("runtime catalog: %w", err)
	}
	wsm, err := filesystem.NewManager(cfg.DataRoot)
	if err != nil {
		return fmt.Errorf("data root: %w", err)
	}
	defer wsm.Close()
	// Before anything can touch a workspace: roll back restores and
	// deployments a crash interrupted, so no bot starts on half-replaced files.
	if rolled, err := wsm.Recover(); err != nil {
		log.Error("workspace recovery incomplete; affected files are kept for inspection", "err", err)
	} else if len(rolled) > 0 {
		log.Warn("rolled back interrupted restores or deployments", "bots", rolled)
	}
	// Without explicit settings, a panel that can neither run as root nor
	// chown falls back to its own uid:gid so workspaces stay writable
	// (development, or production with RIVET_ALLOW_SHARED_UID=1).
	ownership := resolveOwnership(&cfg, wsm, log, true)
	if err := sharedUIDError(ownership); err != nil {
		return err
	}
	if uid, gid, err := runner.ParseUser(ownership.Owner); err == nil {
		wsm.SetOwner(uid, gid) // files the panel writes are usable by the bot immediately
	}

	authSvc := &service.AuthService{Store: db, Hasher: auth.NewHasher(cfg.HashWorkers), TTL: cfg.SessionTTL}
	gameProviders := &blueprint.Providers{}
	botSvc := &service.BotService{
		Store: db, Catalog: catalog, Keys: keys, Workspaces: wsm, LocalNode: domain.LocalNodeID,
		Limits: service.Limits{MinMemoryBytes: config.MinBotMemory, MaxMemoryBytes: cfg.MaxBotMemory,
			MinNanoCPUs: config.MinBotCPUs, MaxNanoCPUs: cfg.MaxBotCPUs,
			PortMin: cfg.PortMin, PortMax: cfg.PortMax, PortPublicBind: cfg.PortPublicBind,
			MaxBotsPerUser: cfg.MaxBotsPerUser, UserMemoryBytes: cfg.UserMemoryBytes, NodeMemoryBytes: cfg.NodeMemoryBytes},
	}
	// OAuth always exists: providers come from the environment or from the
	// settings page, and can change while the panel runs.
	oauthSvc := &service.OAuthService{Store: db, Auth: authSvc, Keys: keys, Providers: map[string]oauth.Provider{},
		PublicURL: cfg.PublicURL, AllowSignup: cfg.OAuthAllowSignup, States: oauth.NewStateStore(), Log: log}
	settingsSvc := &service.SettingsService{Store: db, Keys: keys, OAuth: oauthSvc, Auth: authSvc, Log: log,
		Env: service.EnvSettings{PublicURL: cfg.PublicURL, GitHubID: cfg.GitHubClientID, GitHubSecret: cfg.GitHubSecret,
			DiscordID: cfg.DiscordClientID, DiscordSecret: cfg.DiscordSecret, AllowSignup: cfg.OAuthAllowSignup,
			AllowSignupSet: cfg.OAuthAllowSignupSet, Production: cfg.Production,
			MailKey: cfg.MailgunAPIKey, MailDomain: cfg.MailgunDomain, MailRegion: cfg.MailgunRegion, MailFrom: cfg.MailFrom}}
	if err := settingsSvc.Apply(startCtx); err != nil {
		return fmt.Errorf("panel settings: %w", err)
	}
	setupCodeFile := filepath.Join(filepath.Dir(cfg.DBPath), "setup-code")
	if need, err := settingsSvc.SetupNeeded(startCtx); err == nil && need {
		code := settingsSvc.SetupCode()
		if err := os.WriteFile(setupCodeFile, []byte(code+"\n"), 0o600); err != nil {
			log.Warn("could not write the setup code file", "err", err)
		}
		log.Warn("FIRST-RUN SETUP: open the panel in a browser and enter this setup code", "setup_code", code, "file", setupCodeFile)
	} else {
		_ = os.Remove(setupCodeFile)
	}
	cancel()

	bus := events.NewBus()
	botSvc.Bus = bus
	botSvc.Files = wsm
	botSvc.Coord = &service.Coordinator{}
	opLogs, err := oplog.New(filepath.Join(filepath.Dir(cfg.DBPath), "oplogs"), oplog.DefaultMax)
	if err != nil {
		return fmt.Errorf("operation logs: %w", err)
	}
	ops := &service.Operations{Store: db, Bots: botSvc, Logs: opLogs, Log: log}
	// Log files by day and their daily archive (see internal/logarchive);
	// the settings also hold the retention of graph data.
	logFiles, err := logarchive.New(filepath.Dir(cfg.DBPath))
	if err != nil {
		return fmt.Errorf("log archive: %w", err)
	}
	logArchive := &service.LogArchiveService{Store: db, Files: logFiles, Sink: panelLogSink, Bots: botSvc,
		OpLogDir: filepath.Join(filepath.Dir(cfg.DBPath), "oplogs"), Defaults: service.DefaultLogSettings(cfg.TelemetryRetention), Log: log,
		MinFreeDisk: cfg.MinFreeDisk}
	if err := logArchive.Load(ctx); err != nil {
		return fmt.Errorf("log archive settings: %w", err)
	}
	panelLogSink.Attach(ctx, logFiles)
	telemetryRetention := func() time.Duration { return time.Duration(logArchive.Metrics().TelemetryHours) * time.Hour }
	ops.Recover(ctx)
	audit := &service.Audit{Store: db, Bots: botSvc, Log: log}
	// Alerts and GitHub deployments are always wired; they report clearly
	// when Discord or GitHub is not configured (yet).
	// Email (Mailgun) is configured in Panel settings or the environment and
	// costs nothing while idle: no queue and no goroutine until a message is sent.
	mailSvc := &service.MailService{Settings: settingsSvc, Recipients: db, Log: log}
	resetSvc := &service.PasswordResetService{Auth: authSvc, Store: db, Mail: mailSvc, Settings: settingsSvc, Log: log}
	verifySvc := &service.EmailVerificationService{Auth: authSvc, Mail: mailSvc, Settings: settingsSvc}
	// In-panel notifications (and their email) for alerts, deployments,
	// backups, sharing, nodes, announcements and support tickets.
	notices := &service.NotificationService{Store: db, Mail: mailSvc, Settings: settingsSvc, Log: log}
	alerts := &service.AlertService{Store: db, Keys: keys, Bots: db, Log: log, Prefs: db.GetAlertPrefs, Mail: mailSvc, Users: db, Notices: notices}
	botSvc.Notices = notices
	tickets := &service.TicketService{Store: db, Bots: botSvc, Notices: notices}
	go alerts.Watch(ctx, bus)
	deploySvc := &service.DeployService{Bots: botSvc, OAuth: oauthSvc, Files: wsm, GH: &github.Client{}, Keys: keys,
		Alerts: alerts, PublicURL: cfg.PublicURL, Log: log, Ops: ops}
	deploySvc.Limits = filesystem.DefaultBackupLimits
	deploySvc.MinFreeDisk = cfg.MinFreeDisk
	deploySvc.Start(ctx)
	botSvc.BeforeDelete = deploySvc.BeforeBotDelete
	var checks []api.Check
	var runnerReady func(context.Context) error
	var runnerStatus func(context.Context) (runner.Status, bool)
	var runnerDone chan struct{}
	var dk *docker.Adapter
	var rn *runner.Runner
	// Deployment problems found at start (former installation, container
	// paths, unencrypted public address) are logged and shown in Diagnostics.
	deployChecks := listenCheck(log, cfg)
	installID, err := db.InstallationID(ctx, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("installation identity: %w", err)
	}
	var aiDiagnostic service.DiagnosticRunner
	if cfg.RunnerMode == config.RunnerLocal {
		var err error
		dk, err = docker.New(cfg.DockerHost)
		if err != nil {
			return fmt.Errorf("docker client: %w", err)
		}
		defer dk.Close()
		// In a container, bind sources must be Docker-host paths.
		pathChecks, err := hostPathCheck(ctx, log, cfg, dk)
		if err != nil {
			return err
		}
		deployChecks = append(deployChecks, pathChecks...)
		// Add-on (database) data lives next to the database, outside every
		// bot workspace.
		addonRoot, err := filepath.Abs(filepath.Join(filepath.Dir(cfg.DBPath), "addons"))
		if err != nil {
			return fmt.Errorf("add-on data: %w", err)
		}
		addonData := addons.DataRoot{Dir: addonRoot}
		rn, err = runner.New(runner.Deps{Store: db, Docker: dk, Env: botSvc, Workspaces: wsm, Catalog: catalog, Log: log, Bus: bus, Builds: ops,
			Providers: gameProviders},
			runner.Options{NodeID: domain.LocalNodeID, InstallID: installID, User: cfg.ContainerUser, WorkspaceOwner: cfg.WorkspaceOwner, Network: cfg.ContainerNetwork,
				Workers: cfg.RunnerWorkers, BuildTimeout: cfg.BuildTimeout, MaxBuilds: cfg.MaxBuilds, AddonData: addonData})
		if err != nil {
			return fmt.Errorf("runner: %w", err)
		}
		botSvc.Notifier, botSvc.Purger, botSvc.Killer = rn, rn, rn
		// Allocations and starts skip host ports that any running container
		// on this Docker host publishes (another panel, any other tool).
		botSvc.LocalPublished = dk.PublishedPorts
		botSvc.Stdin = dk
		botSvc.AddonData, botSvc.AddonRuntime = addonData, addonRuntime{rn}
		checks = append(checks, api.Check{Name: "docker", Fn: rn.Check})
		runnerReady = rn.Check
		runnerStatus = func(context.Context) (runner.Status, bool) { return rn.Status(), true }
		runnerDone = make(chan struct{})
		go func() {
			defer close(runnerDone)
			if err := rn.Run(ctx); err != nil {
				log.Error("runner stopped", "err", err)
			}
		}()
		runner.CleanDiagnosticScratch(filepath.Join(filepath.Dir(cfg.DBPath), "ai-scratch"), log)
		aiDiagnostic = &runner.Diagnostic{Docker: dk, Files: wsm, Catalog: catalog, ScratchRoot: filepath.Join(filepath.Dir(cfg.DBPath), "ai-scratch"),
			User: cfg.ContainerUser, InstallID: installID, NodeID: domain.LocalNodeID, Timeout: 10 * time.Minute,
			MemoryBytes: 768 << 20, NanoCPUs: 1e9, PidsLimit: 256, TmpfsBytes: 128 << 20}
	}

	var consoleSvc *console.Service
	if dk != nil || cfg.Modules.Enabled("agents") {
		consoleSvc = &console.Service{Bus: bus}
		if dk != nil {
			consoleSvc.Src = dk
		}
	}
	sampler := &telemetry.Sampler{Store: db, Reader: telemetry.ProcReader{}, NodeID: domain.LocalNodeID,
		DiskPath: cfg.DataRoot, Interval: cfg.TelemetryInterval, Retention: cfg.TelemetryRetention, RetentionFunc: telemetryRetention, Log: log}
	go sampler.Run(ctx)

	var statsSrc api.StatsSource
	if dk != nil {
		statsSrc = dk
	}
	var sftpInfo *api.SFTPInfo
	var sftpDone chan struct{}
	startSFTP := func(*noderoute.Router) {}
	if cfg.SFTPListen != "" {
		hostKey, err := sftpd.LoadOrCreateHostKey(cfg.SFTPHostKey)
		if err != nil {
			return fmt.Errorf("sftp host key: %w", err)
		}
		sln, err := net.Listen("tcp", cfg.SFTPListen)
		if err != nil {
			return fmt.Errorf("sftp listen: %w", err)
		}
		_, port, _ := net.SplitHostPort(sln.Addr().String())
		sftpInfo = &api.SFTPInfo{Port: port, Fingerprint: sftpd.Fingerprint(hostKey)}
		log.Info("sftp listening", "addr", sln.Addr().String(), "host_key", sftpInfo.Fingerprint)
		sftpSrv := &sftpd.Server{Auth: authSvc, Bots: botSvc, Files: wsm, HostKey: hostKey, MaxFile: cfg.SFTPMaxFile, Log: log}
		// Started once node routing is final (below), so a remote server's
		// files are never looked up on this panel's disk.
		startSFTP = func(router *noderoute.Router) {
			sftpSrv.Remote = sftpRemote(botSvc, router)
			sftpDone = make(chan struct{})
			go func() { defer close(sftpDone); sftpSrv.Serve(ctx, sln) }()
		}
	}

	backupSvc := &service.BackupService{Bots: botSvc, Files: wsm, Keys: keys, Dir: cfg.BackupDir, Keep: cfg.BackupKeep,
		Interval: cfg.BackupInterval, Log: log, Ops: ops, Alerts: alerts, MinFreeDisk: cfg.MinFreeDisk}
	backupSvc.Limits = filesystem.DefaultBackupLimits
	backupSvc.Limits.MaxBytes = cfg.BackupMaxBytes
	botSvc.OnDelete = func(id string) { backupSvc.PurgeBot(id); logArchive.PurgeBot(id) }
	scheduler := &service.Scheduler{Store: db, Bots: botSvc, Backups: backupSvc, Deploy: deploySvc, Log: log}
	health := &service.HealthService{Store: db, Bots: botSvc, Alerts: alerts, Log: log}
	go health.Run(ctx)
	analytics := &service.Analytics{Store: db, Heartbeat: health.Heartbeat}
	// Pruning of old rows, one task at a time (see runMaintenance).
	go runMaintenance(ctx, log, []maintenanceTask{
		{name: "prune activity record", every: time.Hour, run: audit.Prune},
		{name: "prune notifications", every: time.Hour, run: func(ctx context.Context) error { _, err := notices.Prune(ctx); return err }},
		{name: "prune sessions", every: 15 * time.Minute, run: func(ctx context.Context) error { return pruneSessions(ctx, db) }},
		{name: "prune bot telemetry", every: time.Hour, run: func(ctx context.Context) error { return analytics.Prune(ctx, telemetryRetention()) }},
	}, nil)

	var enabledOAuth []string
	for _, p := range []string{"github", "discord"} {
		if oauthSvc != nil && oauthSvc.Enabled(p) {
			enabledOAuth = append(enabledOAuth, p)
		}
	}
	// Static site hosting: its own listener, never the panel's origin.
	var sitesSvc *service.SiteService
	var sitesSrv *http.Server
	if cfg.SitesListen != "" {
		panelURL := oauthSvc.CurrentPublicURL()
		if panelURL == "" {
			panelURL = cfg.PublicURL
		}
		panelHost := ""
		if u, err := url.Parse(panelURL); err == nil {
			panelHost = u.Hostname()
		}
		sitesSvc = &service.SiteService{Store: db, Bots: botSvc, OAuth: oauthSvc, GH: &github.Client{}, Dir: cfg.SitesDir,
			BaseURL: cfg.SitesBaseURL, ExtraDomains: cfg.SitesDomains, DNSTarget: cfg.SitesDNSTarget, PanelHost: panelHost, PanelURL: cfg.PublicURL, MaxBytes: cfg.SiteMaxBytes,
			MaxPerUser: cfg.MaxSitesPerUser, Log: log}
		if err := sitesSvc.Start(ctx); err != nil {
			return err
		}
		sln, err := net.Listen("tcp", cfg.SitesListen)
		if err != nil {
			return fmt.Errorf("sites listen: %w", err)
		}
		sitesSrv = sitehost.NewServer(&sitehost.Handler{Sites: sitesSvc, Log: log})
		log.Info("sites listening", "addr", sln.Addr().String(), "base_url", cfg.SitesBaseURL)
		go func() {
			if err := sitesSrv.Serve(sln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("sites server", "err", err)
			}
		}()
	}
	migs, _ := fs.Glob(migrations.FS, "*.sql")
	deployChecks = append(deployChecks, legacyChecks(ctx, log, cfg, dk)...)
	dsrc := diagSources{cfg: cfg, db: db, files: wsm, catalog: catalog, keys: keys, runnerStatus: runnerStatus, ownership: ownership,
		oauth: enabledOAuth, knownSchema: migrationCount(migs), deploy: deployChecks}
	diagnostics := func(ctx context.Context) diag.Report { return diag.Run(ctx, version, started, probes(dsrc)) }
	var aiLogs service.LogTail
	if dk != nil {
		aiLogs = dk
	}
	monitor := &hostmon.Monitor{Proc: telemetry.ProcReader{}, Store: db, Users: db, Runner: runnerStatus, Logs: logs, Version: version,
		Started: started, DBPath: cfg.DBPath, DataRoot: cfg.DataRoot, BackupDir: cfg.BackupDir, NodeBudget: cfg.NodeMemoryBytes,
		WorkspaceUsage: wsm.Usage}
	if cfg.SitesListen != "" {
		monitor.SitesDir = cfg.SitesDir
	}
	if dk != nil {
		monitor.Stats, monitor.Containers = dk, dk
	}
	aiSvc := &service.AIService{Store: db, Keys: keys, Bots: botSvc, Sites: sitesSvc, Files: wsm, Ops: ops, Audit: audit, Diagnostic: aiDiagnostic, Logs: aiLogs, Log: log}
	aiSvc.Start(ctx)
	deploySvc.AI = aiSvc
	var gameSvc *service.GameService
	if cfg.Modules.Enabled("game_servers") {
		gameSvc = &service.GameService{Bots: botSvc, Store: db, Providers: gameProviders, Builtins: blueprints.FS, Files: wsm}
		if err := gameSvc.SyncBuiltins(ctx); err != nil {
			return fmt.Errorf("game server blueprints: %w", err)
		}
	}
	var enrollmentSvc *service.AgentEnrollmentService
	var router *noderoute.Router
	var agentControl api.AgentControl
	if cfg.Modules.Enabled("agents") {
		ca, err := agentcert.LoadOrCreate(filepath.Join(cfg.KeyDir, "agent-ca"))
		if err != nil {
			return fmt.Errorf("agent certificate authority: %w (the key directory is read-only to the service; create the CA once as root with: rivetpanel keygen --agent-ca)", err)
		}
		enrollmentSvc = &service.AgentEnrollmentService{Store: db, CA: ca}
		// Pushes that arrived while a node was offline run when it reconnects.
		// Accounts that manage nodes hear about a node offline for 2 minutes.
		nodeNotices := &service.NodeNotices{Notices: notices, Grace: 2 * time.Minute,
			Name: func(ctx context.Context, id string) string {
				if n, err := db.GetNode(ctx, id); err == nil {
					return n.Name
				}
				return ""
			}}
		onConnect := func(nodeID string) {
			nodeNotices.Connected(ctx, nodeID)
			deploySvc.RunPendingPushes(ctx, nodeID)
		}
		onDisconnect := func(nodeID string) { nodeNotices.Disconnected(ctx, nodeID) }
		hub, addr, err := startAgentHub(ctx, log, cfg, db, ca, botSvc, ops, installID, onConnect, onDisconnect)
		if err != nil {
			return err
		}
		nodeNotices.Online = hub.Connected
		enrollmentSvc.AgentAddress = addr
		agentControl = hub
		router = &noderoute.Router{LocalNode: domain.LocalNodeID, Hub: hub, Bots: db}
		go router.RunNodeSampler(ctx, db, cfg.TelemetryInterval)
		if rn != nil {
			router.Local, router.LocalConsole, router.LocalStats, router.LocalStdin = rn, dk, dk, dk
		}
		botSvc.Notifier, botSvc.Purger, botSvc.Killer = router, router, router
		botSvc.RemoteNode = router.Remote
		botSvc.NodeFiles = router
		botSvc.RemoteAddons = nodeAddons{router}
		botSvc.RemotePorts = func(ctx context.Context, nodeID, ip string, ports []int) ([]service.PortState, error) {
			res, err := router.ProbePorts(ctx, nodeID, ip, ports)
			if err != nil {
				return nil, err
			}
			out := make([]service.PortState, len(res))
			for i, r := range res {
				out[i] = service.PortState{Port: r.Port, Free: r.Free, Reason: r.Reason}
			}
			return out, nil
		}
		botSvc.StdinFor = func(n string) interface {
			AttachStdin(ctx context.Context, containerID string) (io.WriteCloser, error)
		} {
			return router.StdinFor(n)
		}
		if consoleSvc != nil {
			consoleSvc.SourceFor = router.Console
		}
		if gameSvc != nil {
			gameSvc.RemoteQuery = router.QueryMinecraft
		}
		backupSvc.Remote = router
		aiSvc.RemoteDiagnostic = router
		deploySvc.Remote = router
		deploySvc.RemotePush = router
	}
	startSFTP(router)
	// Knowledgebase and public status page (sampled every 5 minutes while
	// the page is enabled; 90 days of daily counts are kept).
	kbSvc := &service.KBService{Store: db}
	statusSvc := &service.StatusService{Store: db, Bots: botSvc, Log: log, RetentionDays: func() int { return logArchive.Metrics().StatusDays }}
	if router != nil {
		statusSvc.NodeOnline = router.Online
	}
	go statusSvc.RunSampler(ctx, service.StatusSampleInterval)
	// Usage analytics: one collector pass a minute, 5-minute buckets rolled
	// up into hourly and daily rows (see service.UsageService).
	usageSvc := &service.UsageService{Store: db, Bots: botSvc, LocalNode: domain.LocalNodeID, Log: log, WorkspaceUsage: wsm.Usage, Retention: logArchive.Metrics}
	usageSvc.StatsFor = func(nodeID string) service.UsageStats {
		if router != nil && router.Remote(nodeID) {
			if !router.Online(nodeID) {
				return nil
			}
			return router.StatsFor(nodeID)
		}
		if dk != nil && nodeID == domain.LocalNodeID {
			return dk
		}
		return nil
	}
	// Console output of local and remote servers is copied into day files
	// (remote output streams through the agent; nothing changes on nodes).
	if dk != nil || router != nil {
		logArchive.Capture = &logarchive.Capturer{Store: logFiles,
			Targets: func(ctx context.Context) ([]logarchive.Target, error) {
				bots, err := db.ListBots(ctx, "")
				if err != nil {
					return nil, err
				}
				out := make([]logarchive.Target, 0, len(bots))
				for _, b := range bots {
					if b.ContainerID != nil && *b.ContainerID != "" {
						out = append(out, logarchive.Target{BotID: b.ID, NodeID: b.NodeID, ContainerID: *b.ContainerID})
					}
				}
				return out, nil
			},
			SourceFor: func(nodeID string) logarchive.Source {
				if router != nil && router.Remote(nodeID) {
					if !router.Online(nodeID) {
						return nil
					}
					return router.Console(nodeID)
				}
				if dk != nil && (nodeID == "" || nodeID == domain.LocalNodeID) {
					return dk
				}
				return nil
			}}
	}
	go logArchive.Run(ctx, 30*time.Second)
	usageDone := make(chan struct{})
	go func() { defer close(usageDone); usageSvc.Run(ctx, service.UsageSampleInterval) }()
	// Start backup scheduling only after node routing is final. Otherwise the
	// startup pass could mistake a remote workspace for a local directory.
	if err := backupSvc.Start(ctx); err != nil {
		return fmt.Errorf("backups: %w", err)
	}
	go scheduler.Run(ctx)
	mfaSvc := &service.MFAService{Store: db, Keys: keys, Auth: authSvc}
	app := api.New(api.Deps{Diagnostics: diagnostics, Log: log, Deploy: deploySvc, Backups: backupSvc, Stats: statsSrc, SFTP: sftpInfo, Analytics: analytics, PublicURL: cfg.PublicURL, DB: db, UI: webui.FS(), Auth: authSvc, Bots: botSvc, OAuth: oauthSvc,
		Catalog: catalog, SecureCookies: cfg.Production, ProxyHeader: cfg.ProxyHeader, MetricsToken: cfg.MetricsToken, Modules: cfg.Modules, Checks: checks, Nodes: db, Files: wsm, MaxUpload: cfg.MaxUploadBytes,
		Console: consoleSvc, BaseCtx: ctx, RunnerReady: runnerReady, Ops: ops, Audit: audit, Schedules: scheduler,
		MFA: mfaSvc, Tokens: &service.TokenService{Store: db, Bots: botSvc}, Clients: &service.APIClientService{Store: db, Bots: botSvc}, Passkeys: &service.PasskeyService{Store: db, Auth: authSvc, MFA: mfaSvc, PublicURL: oauthSvc.CurrentPublicURL}, OIDC: &service.OIDCService{Store: db, Auth: authSvc, Keys: keys, PublicURL: oauthSvc.CurrentPublicURL, States: oauthSvc.States, Log: log}, Enrollment: enrollmentSvc, AgentControl: agentControl, Games: gameSvc, Router: router, Health: health,
		Settings: settingsSvc, Notifications: notices, Tickets: tickets, KB: kbSvc, Status: statusSvc, Usage: usageSvc, Mail: mailSvc, Resets: resetSvc, Verify: verifySvc, MailPrefs: db, Sites: sitesSvc, AI: aiSvc, Env: envSvc, Host: monitor, Logs: logs, LogArchive: logArchive, SetupCodeFile: setupCodeFile, OnSetupDone: func() { _ = os.Remove(setupCodeFile) }})
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	// Startup (migrations, blueprint checks, route registration) leaves a few
	// MiB of garbage; return it now rather than holding it until the
	// collector and scavenger catch up on an idle panel.
	debug.FreeOSMemory()
	log.Info("listening", "addr", ln.Addr().String(), "db", cfg.DBPath, "runner", cfg.RunnerMode, "thp_disabled", thpOff)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shCtx, shCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shCancel()
	if err := app.ShutdownWithContext(shCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("shutdown: %w", err)
	}
	if sitesSrv != nil {
		_ = sitesSrv.Shutdown(shCtx)
		sitesSvc.Wait()
	}
	backupSvc.Wait()
	if deploySvc != nil {
		deploySvc.Wait()
	}
	if sftpDone != nil {
		select {
		case <-sftpDone:
		case <-shCtx.Done():
		}
	}
	if runnerDone != nil {
		select {
		case <-runnerDone: // ctx is cancelled; workers drain
		case <-shCtx.Done():
		}
	}
	select {
	case <-usageDone: // the open usage bucket is written
	case <-shCtx.Done():
	}
	return nil
}

// pruneSessions deletes expired sessions in bounded batches.
func pruneSessions(ctx context.Context, db *sqlite.DB) error {
	for {
		n, err := db.PruneSessions(ctx, time.Now().UnixMilli(), 500)
		if err != nil || n < 500 {
			return err
		}
	}
}

// lockInstallation takes an exclusive lock next to the database for the life
// of the process.
func lockInstallation(dbPath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dbPath+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("installation lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another RivetPanel process is already using %s; stop it first", dbPath)
	}
	return func() { f.Close() }, nil
}

// doctorCmd runs the diagnostics without starting the panel.
func doctorCmd() error {
	cfg, err := config.LoadEnv()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	fmt.Println("configuration: ok")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Read-only: no directory or database is created and no migration runs,
	// so a wrong path is reported instead of silently initialised.
	for _, p := range []struct{ what, path, env string }{{"database", cfg.DBPath, "RIVET_DB_PATH"}, {"data root", cfg.DataRoot, "RIVET_DATA_ROOT"}} {
		if _, err := os.Stat(p.path); err != nil {
			fmt.Printf("FAIL  %-22s %s does not exist or cannot be read: %v\n", p.what, p.path, err)
			fmt.Printf("      %-22s -> check %s, or start the panel once to initialise it\n", "", p.env)
			return fmt.Errorf("%s not found", p.what)
		}
	}
	db, err := sqlite.OpenReadOnly(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	keys, _ := secrets.LoadDir(cfg.KeyDir, cfg.ActiveKeyID, false)
	catalog, err := loadCatalog(cfg)
	if err != nil {
		return err
	}
	wsm, err := filesystem.NewManager(cfg.DataRoot) // exists (checked above): nothing is created
	if err != nil {
		return err
	}
	defer wsm.Close()
	ownership := resolveOwnership(&cfg, wsm, nil, false)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := diagSources{cfg: cfg, db: db, files: wsm, catalog: catalog, keys: keys, ownership: ownership, deploy: listenCheck(quiet, cfg)}
	if cfg.RunnerMode == config.RunnerLocal {
		dk, err := docker.New(cfg.DockerHost)
		if err == nil {
			defer dk.Close()
			src.deploy = append(src.deploy, legacyChecks(ctx, quiet, cfg, dk)...)
			src.runnerStatus = func(ctx context.Context) (runner.Status, bool) {
				caps, err := dk.Capabilities(ctx)
				if err == nil {
					err = caps.Validate()
				}
				return runner.Status{Ready: err, Capabilities: caps, Workers: cfg.RunnerWorkers}, true
			}
		}
	}
	if src.runnerStatus == nil { // no Docker client: environment and directories only
		src.deploy = append(src.deploy, legacyChecks(ctx, quiet, cfg, nil)...)
	}
	migs, _ := fs.Glob(migrations.FS, "*.sql")
	src.knownSchema = migrationCount(migs)
	r := diag.Run(ctx, version, time.Now(), probes(src))
	for _, c := range r.Checks {
		mark := map[diag.Status]string{diag.OK: "ok  ", diag.Warn: "WARN", diag.Fail: "FAIL", diag.Info: "info"}[c.Status]
		fmt.Printf("%s  %-22s %s\n", mark, c.Title, c.Detail)
		if c.Fix != "" && c.Status != diag.OK {
			fmt.Printf("      %-22s -> %s\n", "", c.Fix)
		}
	}
	if fail, _ := r.Summary(); fail > 0 {
		return fmt.Errorf("%d check(s) failed", fail)
	}
	return nil
}

// addonRuntime adapts the runner's add-on observations to the service.
type addonRuntime struct{ rn *runner.Runner }

func (a addonRuntime) AddonStates(ctx context.Context, botID string) (map[string]service.AddonState, error) {
	st, err := a.rn.AddonStates(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]service.AddonState, len(st))
	for k, v := range st {
		out[k] = service.AddonState{State: v.State, Health: v.Health, ExitCode: v.ExitCode}
	}
	return out, nil
}

func (a addonRuntime) AddonLogs(ctx context.Context, botID, kind string, lines int) (string, error) {
	return a.rn.AddonLogs(ctx, botID, kind, lines)
}

// startAgentHub opens the mutually authenticated listener remote agents dial
// and returns the address announced to them at enrollment.
func startAgentHub(ctx context.Context, log *slog.Logger, cfg config.Config, db *sqlite.DB, ca *agentcert.Authority,
	env agenthub.EnvSource, builds agenthub.BuildRecorder, installID string, onConnect, onDisconnect func(nodeID string)) (*agenthub.Hub, string, error) {
	_, port, err := net.SplitHostPort(cfg.AgentListen)
	if err != nil {
		return nil, "", fmt.Errorf("RIVET_AGENT_LISTEN %q: %w", cfg.AgentListen, err)
	}
	addr := cfg.AgentAddress
	if addr == "" && cfg.PublicURL != "" {
		if u, err := url.Parse(cfg.PublicURL); err == nil && u.Hostname() != "" {
			addr = net.JoinHostPort(u.Hostname(), port)
		}
	}
	hosts := cfg.AgentHosts
	if len(hosts) == 0 {
		hosts = []string{"localhost", "127.0.0.1"}
		if h, _, err := net.SplitHostPort(addr); err == nil && h != "" {
			hosts = append([]string{h}, hosts...)
		}
	}
	serverCert, err := ca.IssueServer(hosts, 365*24*time.Hour)
	if err != nil {
		return nil, "", fmt.Errorf("agent listener certificate: %w", err)
	}
	hub := &agenthub.Hub{Store: db, CA: ca, Env: env, Builds: builds, InstallID: installID, Log: log, OnConnect: onConnect, OnDisconnect: onDisconnect}
	if err := db.ResetAgentConnections(ctx, time.Now().UnixMilli()); err != nil {
		return nil, "", err
	}
	ln, err := net.Listen("tcp", cfg.AgentListen)
	if err != nil {
		return nil, "", fmt.Errorf("agent listen: %w", err)
	}
	go func() {
		if err := hub.Serve(ctx, ln, serverCert); err != nil {
			log.Error("agent listener stopped", "err", err)
		}
	}()
	log.Info("agent listener", "addr", ln.Addr().String(), "announced", addr, "certificate_hosts", strings.Join(hosts, ","))
	return hub, addr, nil
}

// sftpRemote decides where SFTP finds a server's files. Any server assigned
// to a node other than this panel's own is remote: it is served through its
// agent, refused while that node is offline, and refused outright when the
// agents module is off. The panel's disk is never used for it.
func sftpRemote(bots *service.BotService, router *noderoute.Router) func(domain.Bot) (sftpd.NodeFiles, bool, error) {
	return func(b domain.Bot) (sftpd.NodeFiles, bool, error) {
		if b.NodeID == "" || b.NodeID == domain.LocalNodeID {
			return nil, false, nil
		}
		if router == nil {
			return nil, true, domain.Invalid("this server runs on a remote node, which needs the agents module")
		}
		if _, _, err := bots.RemoteFiles(b, "SFTP"); err != nil {
			return nil, true, err
		}
		return router, true, nil
	}
}

// nodeAddons adapts the router's remote add-on observations to the service.
type nodeAddons struct{ r *noderoute.Router }

func (a nodeAddons) AddonStates(ctx context.Context, nodeID, botID string) (map[string]service.AddonState, error) {
	st, err := a.r.AddonStates(ctx, nodeID, botID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]service.AddonState, len(st))
	for k, v := range st {
		out[k] = service.AddonState{State: v.State, Health: v.Health, ExitCode: v.ExitCode}
	}
	return out, nil
}

func (a nodeAddons) AddonLogs(ctx context.Context, nodeID, botID, kind string, lines int) (string, error) {
	return a.r.AddonLogs(ctx, nodeID, botID, kind, lines)
}

func (a nodeAddons) RemoveAddonData(ctx context.Context, nodeID, botID, kind string) error {
	return a.r.RemoveAddonData(ctx, nodeID, botID, kind)
}
