package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/diag"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
	"github.com/xenycx/rivetpanel/internal/telemetry"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

// diagSources is what the probes may look at. Runner is nil when the local
// runner is off; RunnerStatus replaces it for "rivetpanel doctor".
type diagSources struct {
	cfg          config.Config
	db           *sqlite.DB
	files        *filesystem.Manager
	catalog      *runtimes.Catalog
	keys         *secrets.Keyring
	runnerStatus func(ctx context.Context) (runner.Status, bool)
	oauth        []string // enabled providers
	knownSchema  int      // newest migration this binary ships
}

func probes(s diagSources) []diag.Probe {
	const panel, host, bots, data = "Panel", "Host", "Bots", "Data"
	return []diag.Probe{
		// Database and schema.
		func(ctx context.Context) []diag.Check {
			c := diag.Check{ID: "database", Group: data, Title: "Database"}
			if err := s.db.PingContext(ctx); err != nil {
				c.Status, c.Detail, c.Fix = diag.Fail, "The database does not respond.", "Check the disk holding "+s.cfg.DBPath+"."
				return []diag.Check{c}
			}
			v, _ := s.db.SchemaVersion(ctx)
			var size int64
			for _, suf := range []string{"", "-wal"} {
				if st, err := os.Stat(s.cfg.DBPath + suf); err == nil {
					size += st.Size()
				}
			}
			c.Status, c.Detail = diag.OK, fmt.Sprintf("Schema version %d, %s on disk.", v, diag.Bytes(size))
			if s.knownSchema > 0 && v != s.knownSchema {
				c.Status, c.Detail = diag.Warn, fmt.Sprintf("Schema version %d; this build expects %d.", v, s.knownSchema)
			}
			return []diag.Check{c}
		},
		// Encryption keys.
		func(ctx context.Context) []diag.Check {
			c := diag.Check{ID: "keys", Group: data, Title: "Encryption keys"}
			ents, _ := os.ReadDir(s.cfg.KeyDir)
			n := 0
			for _, e := range ents {
				if strings.HasSuffix(e.Name(), ".key") {
					n++
				}
			}
			switch {
			case s.keys == nil || !s.keys.Has(s.cfg.ActiveKeyID):
				c.Status, c.Detail, c.Fix = diag.Fail, "The active key "+s.cfg.ActiveKeyID+" is not loaded.", "Restore the key file into "+s.cfg.KeyDir+" or run rivetpanel keygen."
			default:
				c.Status, c.Detail = diag.OK, fmt.Sprintf("Active key %q, %d key file(s) in the key directory.", s.cfg.ActiveKeyID, n)
				c.Fix = "Back up the key directory separately from the database; run rivetpanel verify after restoring."
			}
			return []diag.Check{c}
		},
		// Docker runner.
		func(ctx context.Context) []diag.Check {
			c := diag.Check{ID: "docker", Group: host, Title: "Docker runner"}
			if s.runnerStatus == nil {
				c.Status, c.Detail = diag.Info, "The local runner is turned off (RIVET_RUNNER_MODE=none). Bots cannot start on this panel."
				return []diag.Check{c}
			}
			st, ok := s.runnerStatus(ctx)
			if !ok {
				c.Status, c.Detail = diag.Info, "The runner has not reported yet."
				return []diag.Check{c}
			}
			if st.Ready != nil {
				c.Status, c.Detail, c.Fix = diag.Fail, "Docker is not usable: "+st.Ready.Error()+".", "Check that the Docker service runs and that the panel can reach "+dockerHost(s.cfg)+"."
				return []diag.Check{c}
			}
			caps := st.Capabilities
			parts := []string{"Docker " + caps.ServerVersion}
			if caps.CgroupV2 {
				parts = append(parts, "cgroup v2")
			}
			if caps.Rootless {
				parts = append(parts, "rootless")
			}
			c.Status, c.Detail = diag.OK, strings.Join(parts, ", ")+". Memory, CPU and process limits are enforced."
			out := []diag.Check{c}
			if !caps.SwapLimit {
				out = append(out, diag.Check{ID: "swap", Group: host, Title: "Swap limits", Status: diag.Warn,
					Detail: "The host cannot limit swap, so a bot may use swap beyond its memory limit.", Fix: "Enable swap accounting in the kernel (cgroup v2 does this by default)."})
			}
			q := diag.Check{ID: "queue", Group: bots, Title: "Reconciliation", Status: diag.OK,
				Detail: fmt.Sprintf("%d bot(s) waiting for %d worker(s).", st.Queued, st.Workers)}
			if lag, err := s.db.LaggingBots(ctx, time.Now().UnixMilli(), (2 * time.Minute).Milliseconds()); err == nil && lag > 0 {
				q.Status = diag.Warn
				q.Detail += fmt.Sprintf(" %d bot(s) have waited more than 2 minutes for a requested change (builds count as waiting).", lag)
				q.Fix = "Long builds are normal; otherwise check the Docker runner above."
			}
			return append(out, q)
		},
		// Workspace ownership: what fails as "workspace ownership could not be prepared".
		func(ctx context.Context) []diag.Check {
			c := diag.Check{ID: "ownership", Group: host, Title: "Workspace ownership"}
			owner := s.cfg.WorkspaceOwner
			if owner == "" {
				owner = s.cfg.ContainerUser
			}
			uid, gid, err := runner.ParseUser(owner)
			if err != nil {
				c.Status, c.Detail = diag.Fail, "The container user setting is invalid."
				return []diag.Check{c}
			}
			if err := s.files.ProbeOwnership(uid, gid); err != nil {
				c.Status = diag.Fail
				c.Detail = fmt.Sprintf("The panel cannot give files to %s, so bots cannot write their workspace.", owner)
				c.Fix = "Run the panel as root (the systemd unit does), give it CAP_CHOWN, or set RIVET_CONTAINER_USER to the panel's own uid:gid."
				return []diag.Check{c}
			}
			c.Status, c.Detail = diag.OK, fmt.Sprintf("Workspaces can be handed to %s.", owner)
			return []diag.Check{c}
		},
		// Disk and inodes.
		func(ctx context.Context) []diag.Check {
			total, free, err := s.files.Statfs()
			c := diag.Check{ID: "disk", Group: host, Title: "Disk space"}
			if err != nil || total == 0 {
				c.Status, c.Detail = diag.Warn, "Free space could not be read."
				return []diag.Check{c}
			}
			pct := float64(free) / float64(total) * 100
			c.Status, c.Detail = diag.OK, fmt.Sprintf("%s free of %s (%.0f%%) where workspaces live.", diag.Bytes(int64(free)), diag.Bytes(int64(total)), pct)
			switch {
			case free < 512<<20 || pct < 3:
				c.Status, c.Fix = diag.Fail, "Builds, deployments and backups will fail. Free space or delete old backups."
			case free < 2<<30 || pct < 10:
				c.Status, c.Fix = diag.Warn, "Free space soon: builds and backups need room for temporary copies."
			}
			out := []diag.Check{c}
			if it, ifree, err := s.files.Inodes(); err == nil && it > 0 {
				ipct := float64(ifree) / float64(it) * 100
				ic := diag.Check{ID: "inodes", Group: host, Title: "Files (inodes)", Status: diag.OK, Detail: fmt.Sprintf("%.0f%% of inodes free.", ipct)}
				if ipct < 5 {
					ic.Status, ic.Fix = diag.Warn, "Many small files (node_modules, caches) are using up inodes; remove unused bots or their caches."
				}
				out = append(out, ic)
			}
			return out
		},
		// Memory against build needs.
		func(ctx context.Context) []diag.Check {
			_, total, err := telemetry.ProcReader{}.Memory()
			c := diag.Check{ID: "memory", Group: host, Title: "Memory for builds"}
			if err != nil || total == 0 {
				c.Status, c.Detail = diag.Info, "Host memory could not be read."
				return []diag.Check{c}
			}
			var need int64
			needBy := ""
			for _, r := range s.catalog.List() {
				if r.BuildMemoryBytes > need {
					need, needBy = r.BuildMemoryBytes, r.DisplayName
				}
			}
			c.Status, c.Detail = diag.OK, fmt.Sprintf("The host has %s of memory.", diag.Bytes(total))
			if need > 0 && total < need+(512<<20) {
				c.Status = diag.Warn
				c.Detail += fmt.Sprintf(" %s builds need about %s while compiling.", needBy, diag.Bytes(need))
				c.Fix = "Add memory or swap before building " + needBy + " bots; the build container is stopped when it runs out."
			}
			return []diag.Check{c}
		},
		// Backups.
		func(ctx context.Context) []diag.Check {
			c := diag.Check{ID: "backups", Group: data, Title: "Scheduled backups"}
			sum, err := s.db.BackupSummary(ctx, time.Now().Add(-24*time.Hour).UnixMilli())
			if err != nil {
				c.Status, c.Detail = diag.Warn, "Backup records could not be read."
				return []diag.Check{c}
			}
			if s.cfg.BackupInterval == 0 {
				c.Status, c.Detail, c.Fix = diag.Info, "Scheduled backups are off.", "Set RIVET_BACKUP_INTERVAL (for example 24h) to back up every bot automatically."
			} else {
				c.Status, c.Detail = diag.OK, fmt.Sprintf("Every %s, %d kept per bot. %s stored in total.", human(s.cfg.BackupInterval), s.cfg.BackupKeep, diag.Bytes(sum.TotalBytes))
				if sum.LastReadyMS > 0 && time.Since(time.UnixMilli(sum.LastReadyMS)) > 2*s.cfg.BackupInterval+time.Hour {
					c.Status, c.Fix = diag.Warn, "No backup succeeded for more than two intervals. Check disk space and the backup directory."
				}
			}
			out := []diag.Check{c}
			if sum.FailedRecent > 0 {
				out = append(out, diag.Check{ID: "backup_failures", Group: data, Title: "Failed backups", Status: diag.Warn,
					Detail: fmt.Sprintf("%d backup(s) failed in the last 24 hours.", sum.FailedRecent), Fix: "Open the bots' Backups pages for the reasons."})
			}
			if sum.VerifyFailed > 0 {
				out = append(out, diag.Check{ID: "backup_verify", Group: data, Title: "Damaged backups", Status: diag.Warn,
					Detail: fmt.Sprintf("%d backup(s) failed verification.", sum.VerifyFailed), Fix: "Delete them and create fresh backups."})
			}
			out = append(out, diag.Check{ID: "install_backup", Group: data, Title: "Panel backup", Status: diag.Info,
				Detail: "Per-bot backups do not include the database or keys.", Fix: "Run rivetpanel backup regularly and store the key directory separately."})
			return out
		},
		// Configuration summary (no secrets).
		func(ctx context.Context) []diag.Check {
			var out []diag.Check
			pu := diag.Check{ID: "public_url", Group: panel, Title: "Public address"}
			switch {
			case s.cfg.PublicURL == "":
				pu.Status, pu.Detail, pu.Fix = diag.Info, "Not set. GitHub webhooks, OAuth sign-in and bot telemetry need it.", "Set RIVET_PUBLIC_URL to the https address people use."
			case strings.HasPrefix(s.cfg.PublicURL, "https://"):
				pu.Status, pu.Detail = diag.OK, s.cfg.PublicURL
			default:
				pu.Status, pu.Detail = diag.Warn, s.cfg.PublicURL+" is not https; only loopback addresses are safe without TLS."
			}
			out = append(out, pu)
			sign := diag.Check{ID: "oauth", Group: panel, Title: "Sign-in providers", Status: diag.Info, Detail: "Password sign-in only."}
			if len(s.oauth) > 0 {
				sign.Status, sign.Detail = diag.OK, "Enabled: "+strings.Join(s.oauth, ", ")+". Redirect URLs are printed in the startup log."
			}
			out = append(out, sign)
			sf := diag.Check{ID: "sftp", Group: panel, Title: "SFTP", Status: diag.Info, Detail: "Off."}
			if s.cfg.SFTPListen != "" {
				sf.Status, sf.Detail = diag.OK, "Listening on "+s.cfg.SFTPListen+". It does not work through an HTTP proxy or Cloudflare Tunnel."
			}
			out = append(out, sf)
			return out
		},
	}
}

func dockerHost(cfg config.Config) string {
	if cfg.DockerHost != "" {
		return cfg.DockerHost
	}
	return "the default Docker socket"
}

// migrationCount returns the newest migration version embedded in the binary.
func migrationCount(names []string) int {
	max := 0
	for _, n := range names {
		var v int
		if _, err := fmt.Sscanf(filepath.Base(n), "%04d_", &v); err == nil && v > max {
			max = v
		}
	}
	return max
}

// human formats a duration the way people write it ("6 hours", "30 minutes").
func human(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return plural(int(d/(24*time.Hour)), "day")
	case d >= time.Hour && d%time.Hour == 0:
		return plural(int(d/time.Hour), "hour")
	case d >= time.Minute && d%time.Minute == 0:
		return plural(int(d/time.Minute), "minute")
	}
	return d.String()
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
