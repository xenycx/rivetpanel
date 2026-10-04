package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// VarKind tells the administration page which control to show and how a value
// is checked before it is stored. Validate remains the authority on whether a
// whole configuration is acceptable.
type VarKind string

const (
	KindText     VarKind = "text"
	KindInt      VarKind = "int"
	KindBytes    VarKind = "bytes"    // integer number of bytes
	KindDuration VarKind = "duration" // Go duration such as 30s, 24h
	KindBool     VarKind = "bool"     // 1 or 0
	KindEnum     VarKind = "enum"
	KindPath     VarKind = "path"
	KindURL      VarKind = "url"
	KindSecret   VarKind = "secret"
)

// VarSpec describes one RIVET_* variable.
type VarSpec struct {
	Name        string   `json:"name"`
	Group       string   `json:"group"`
	Description string   `json:"description"`
	Kind        VarKind  `json:"kind"`
	Default     string   `json:"default"`           // built-in default in words; empty = unset/off
	Options     []string `json:"options,omitempty"` // KindEnum
	Example     string   `json:"example,omitempty"`
	// Boot variables are read before the database opens, locate the data the
	// panel needs to start, or widen what the panel may do to the host. They are
	// changed in the environment file so a mistake cannot lock the panel out.
	Boot bool `json:"boot,omitempty"`
	// Managed names the page that owns a variable the panel already stores
	// itself (sign-in providers and the public address).
	Managed string `json:"managed,omitempty"`
	// Danger is an extra warning shown (and confirmed) before saving.
	Danger string `json:"danger,omitempty"`
}

// Secret reports whether the value is a credential that is stored sealed and
// never shown again.
func (v VarSpec) Secret() bool { return v.Kind == KindSecret }

// Editable reports whether an administrator may override the variable from the
// panel.
func (v VarSpec) Editable() bool { return !v.Boot && v.Managed == "" }

// Group names, in display order.
const (
	GroupPanel    = "Panel"
	GroupModules  = "Preview modules"
	GroupRunner   = "Bots and Docker"
	GroupCapacity = "Capacity and limits"
	GroupBackups  = "Backups"
	GroupPorts    = "Published ports"
	GroupSites    = "Static sites"
	GroupSFTP     = "SFTP"
	GroupMonitor  = "Monitoring"
	GroupAccounts = "Sign-in and address"
)

// Groups lists the group names in display order.
var Groups = []string{GroupPanel, GroupModules, GroupRunner, GroupCapacity, GroupBackups, GroupPorts, GroupSites, GroupSFTP, GroupMonitor, GroupAccounts}

// Vars is every variable the panel reads, in display order. A test keeps it in
// step with Load, so a new variable cannot ship without a description.
var Vars = []VarSpec{
	// Panel
	{Name: "RIVET_ENV", Group: GroupPanel, Kind: KindEnum, Options: []string{"production", "development"}, Default: "production", Boot: true,
		Description: "production uses system directories and requires https for public addresses; development keeps everything under ./.dev-data."},
	{Name: "RIVET_LISTEN", Group: GroupPanel, Kind: KindText, Default: "127.0.0.1:8080", Boot: true, Example: "127.0.0.1:8080",
		Description: "Address the panel listens on. Put a TLS reverse proxy in front of it."},
	{Name: "RIVET_DB_PATH", Group: GroupPanel, Kind: KindPath, Default: "/var/lib/rivetpanel/rivetpanel.db", Boot: true,
		Description: "SQLite database file. Read before anything else, so it cannot be changed here."},
	{Name: "RIVET_DB_MAX_CONNS", Group: GroupPanel, Kind: KindInt, Default: "4", Boot: true,
		Description: "Database connections the panel may open (1 to 16). Applied when the database opens."},
	{Name: "RIVET_DATA_ROOT", Group: GroupPanel, Kind: KindPath, Default: "/var/lib/rivetpanel/bots", Boot: true,
		Description: "Parent directory of every bot workspace. Command-line backups and restores read it too, so it stays in the environment file."},
	{Name: "RIVET_KEY_DIR", Group: GroupPanel, Kind: KindPath, Default: "/etc/rivetpanel/keys", Boot: true,
		Description: "Directory of encryption key files. Stored values cannot be read without them."},
	{Name: "RIVET_ACTIVE_KEY_ID", Group: GroupPanel, Kind: KindText, Default: "k1", Boot: true,
		Description: "Key used to encrypt new values. Rotate with rivetpanel keygen and rivetpanel reseal."},
	{Name: "RIVET_RUNTIMES_DIR", Group: GroupPanel, Kind: KindPath, Boot: true,
		Description: "Optional directory that replaces the built-in runtime catalog."},
	{Name: "RIVET_SESSION_TTL", Group: GroupPanel, Kind: KindDuration, Default: "168h", Example: "168h",
		Description: "How long a sign-in lasts without activity being renewed (1 minute to 90 days)."},
	{Name: "RIVET_SHUTDOWN_TIMEOUT", Group: GroupPanel, Kind: KindDuration, Default: "10s", Example: "10s",
		Description: "How long a restart waits for requests in progress. Bot containers keep running either way."},
	{Name: "RIVET_MAX_UPLOAD_BYTES", Group: GroupPanel, Kind: KindBytes, Default: "32 MiB",
		Description: "Largest single file or archive an upload may carry (1 KiB to 1 GiB)."},
	{Name: "RIVET_PROXY_HEADER", Group: GroupPanel, Kind: KindText, Boot: true, Example: "CF-Connecting-IP",
		Description: "Header that carries the real client address behind a reverse proxy on this host. It decides which address rate limits count, so it stays in the environment file."},

	// Preview modules
	{Name: "RIVET_MODULES", Group: GroupModules, Kind: KindText, Example: "agents=preview,game_servers=preview",
		Description: "Comma-separated module states (key=off, key=preview or key=stable). New preview modules are off until explicitly enabled."},
	{Name: "RIVET_AGENT_LISTEN", Group: GroupModules, Kind: KindText, Default: ":8444", Example: ":8444",
		Description: "Address of the listener remote rivet-agents connect to (mutual TLS with the panel's agent certificate authority). Used when the agents module is on. Open this TCP port to your nodes; do not put it behind an HTTP reverse proxy."},
	{Name: "RIVET_AGENT_ADDRESS", Group: GroupModules, Kind: KindText, Example: "panel.example.com:8444",
		Description: "The host:port agents dial, told to them when they enroll. Empty: the public URL's host with the listener's port."},
	{Name: "RIVET_AGENT_HOSTS", Group: GroupModules, Kind: KindText, Example: "panel.example.com,10.0.0.5",
		Description: "Comma-separated host names and IP addresses in the agent listener's certificate. Empty: the agent address's host, localhost and 127.0.0.1."},

	// Bots and Docker
	{Name: "RIVET_RUNNER_MODE", Group: GroupRunner, Kind: KindEnum, Options: []string{"local", "none"}, Default: "local",
		Description: "local runs bots in Docker on this host; none turns the runner off so only the control plane runs.",
		Danger:      "Setting this to none stops managing bot containers after the restart: bots can no longer be started, stopped or built from the panel."},
	{Name: "RIVET_DOCKER_HOST", Group: GroupRunner, Kind: KindText, Default: "unix:///var/run/docker.sock", Boot: true,
		Description: "Docker endpoint. A wrong value stops every bot from starting, so it stays in the environment file."},
	{Name: "RIVET_CONTAINER_USER", Group: GroupRunner, Kind: KindText, Default: "65532:65532", Boot: true, Example: "65532:65532",
		Description: "uid:gid bot processes run as. Changing it changes who owns every workspace, so it stays in the environment file. When this and RIVET_WORKSPACE_OWNER are unset and the panel is neither root nor holds CAP_CHOWN, bots run as the panel's own uid:gid instead (development, or production with RIVET_ALLOW_SHARED_UID=1; otherwise the panel refuses to start)."},
	{Name: "RIVET_WORKSPACE_OWNER", Group: GroupRunner, Kind: KindText, Boot: true,
		Description: "uid:gid that owns workspace files when it differs from the container user."},
	{Name: "RIVET_ALLOW_ROOT_CONTAINER_USER", Group: GroupRunner, Kind: KindBool, Default: "0", Boot: true,
		Description: "Permits uid 0 inside bot containers. Only sensible with a rootless Docker daemon, so it stays in the environment file."},
	{Name: "RIVET_ALLOW_SHARED_UID", Group: GroupRunner, Kind: KindBool, Default: "0", Boot: true,
		Description: "In production, lets a panel that is neither root nor holds CAP_CHOWN run bot containers as its own uid:gid, the uid that also owns the database and keys. Without it such a panel refuses to start; development falls back automatically."},
	{Name: "RIVET_CONTAINER_NETWORK", Group: GroupRunner, Kind: KindText, Default: "bridge", Boot: true,
		Description: "Docker network mode for bot and build containers. It decides what bots can reach, so it stays in the environment file."},
	{Name: "RIVET_RUNNER_WORKERS", Group: GroupRunner, Kind: KindInt, Default: "2", Example: "2",
		Description: "Bots that may start, stop or restart at the same time (1 to 16)."},
	{Name: "RIVET_MAX_BUILDS", Group: GroupRunner, Kind: KindInt, Default: "1", Example: "1",
		Description: "Build containers that may run at once (1 to 16). Each can use its runtime's build memory, 3 GiB for Rust."},
	{Name: "RIVET_BUILD_TIMEOUT", Group: GroupRunner, Kind: KindDuration, Default: "10m", Example: "10m",
		Description: "How long one build may run before it is stopped (1 minute to 2 hours)."},

	// Capacity
	{Name: "RIVET_MAX_BOT_MEMORY_BYTES", Group: GroupCapacity, Kind: KindBytes, Default: "4 GiB",
		Description: "Largest memory limit a single bot may be given."},
	{Name: "RIVET_MAX_BOT_NANO_CPUS", Group: GroupCapacity, Kind: KindInt, Default: "4000000000", Example: "4000000000",
		Description: "Largest CPU limit a single bot may be given, in billionths of a CPU (1000000000 is one CPU)."},
	{Name: "RIVET_NODE_MEMORY_BYTES", Group: GroupCapacity, Kind: KindBytes, Default: "unlimited",
		Description: "Admission budget: the sum of memory limits of bots wanted running on this host. A start that would exceed it is refused. Bookkeeping, not a kernel limit; 0 is unlimited."},
	{Name: "RIVET_USER_MEMORY_BYTES", Group: GroupCapacity, Kind: KindBytes, Default: "unlimited",
		Description: "Sum of memory limits one account's bots may reserve. Administrators are exempt; 0 is unlimited."},
	{Name: "RIVET_MAX_BOTS_PER_USER", Group: GroupCapacity, Kind: KindInt, Default: "unlimited",
		Description: "Bots one account may own. Administrators are exempt; 0 is unlimited."},
	{Name: "RIVET_MIN_FREE_DISK_BYTES", Group: GroupCapacity, Kind: KindBytes, Default: "1 GiB",
		Description: "Free space backups, restores and deployments need before they start; 0 turns the check off."},

	// Backups
	{Name: "RIVET_BACKUP_DIR", Group: GroupBackups, Kind: KindPath, Default: "/var/lib/rivetpanel/backups",
		Description: "Where per-bot backup archives are written. Existing archives are not moved when this changes."},
	{Name: "RIVET_BACKUP_INTERVAL", Group: GroupBackups, Kind: KindDuration, Default: "24h", Example: "24h",
		Description: "Time between scheduled backups of every bot (1 hour to 90 days); 0 turns scheduled backups off."},
	{Name: "RIVET_BACKUP_KEEP", Group: GroupBackups, Kind: KindInt, Default: "7", Example: "7",
		Description: "Scheduled backups kept per bot (1 to 45); older ones are deleted."},
	{Name: "RIVET_BACKUP_MAX_BYTES", Group: GroupBackups, Kind: KindBytes, Default: "2 GiB",
		Description: "Largest workspace a backup will include."},

	// Ports
	{Name: "RIVET_PORT_RANGE", Group: GroupPorts, Kind: KindText, Default: "20000-29999", Example: "20000-29999",
		Description: "Host ports bots may publish (within 1024 to 65535). Bots opt in per bot."},
	{Name: "RIVET_PORT_PUBLIC_BIND", Group: GroupPorts, Kind: KindBool, Default: "0",
		Description: "1 lets published ports bind beyond 127.0.0.1, which exposes them to the network.",
		Danger:      "Published bot ports become reachable from outside this host. Make sure a firewall limits them."},

	// Sites
	{Name: "RIVET_SITES_LISTEN", Group: GroupSites, Kind: KindText, Example: "127.0.0.1:8081",
		Description: "Separate listener that only serves hosted sites, never the panel. Empty turns site hosting off. It must differ from the panel address."},
	{Name: "RIVET_SITES_BASE_URL", Group: GroupSites, Kind: KindURL, Example: "https://sites.example.com",
		Description: "Public origin sites are served under (a site named docs is served at docs.<host>). Use a different domain than the panel."},
	{Name: "RIVET_SITES_DOMAINS", Group: GroupSites, Kind: KindText, Example: "pages.example.net,example-sites.org",
		Description: "Further domains sites can live under, comma-separated, trusted like the base URL's host. Administrators can also add domains in Administration → Sites and domains, verified by DNS."},
	{Name: "RIVET_SITES_DIR", Group: GroupSites, Kind: KindPath, Default: "/var/lib/rivetpanel/sites",
		Description: "Directory holding site releases."},
	{Name: "RIVET_SITE_MAX_BYTES", Group: GroupSites, Kind: KindBytes, Default: "100 MiB",
		Description: "Largest site release, uncompressed (1 MiB to 4 GiB)."},
	{Name: "RIVET_MAX_SITES_PER_USER", Group: GroupSites, Kind: KindInt, Default: "10",
		Description: "Sites one account may create. Administrators are exempt; 0 is unlimited."},
	{Name: "RIVET_SITES_DNS_TARGET", Group: GroupSites, Kind: KindText, Example: "sites.example.com",
		Description: "Host name custom domains should point a CNAME at. Defaults to the host of the sites base URL."},

	// SFTP
	{Name: "RIVET_SFTP_LISTEN", Group: GroupSFTP, Kind: KindText, Example: "0.0.0.0:2022",
		Description: "Address of the embedded SFTP server; empty turns it off. SFTP is plain TCP and does not pass through an HTTP proxy or Cloudflare Tunnel."},
	{Name: "RIVET_SFTP_MAX_FILE_BYTES", Group: GroupSFTP, Kind: KindBytes, Default: "256 MiB",
		Description: "Largest file an SFTP client may write (1 MiB to 64 GiB)."},
	{Name: "RIVET_SFTP_HOST_KEY", Group: GroupSFTP, Kind: KindPath, Default: "next to the database",
		Description: "SSH host key file. Created on first start; changing it makes every SFTP client warn about a new key."},

	// Monitoring
	{Name: "RIVET_TELEMETRY_INTERVAL", Group: GroupMonitor, Kind: KindDuration, Default: "30s", Example: "30s",
		Description: "How often host CPU, memory, disk and network are sampled (30 to 60 seconds)."},
	{Name: "RIVET_TELEMETRY_RETENTION", Group: GroupMonitor, Kind: KindDuration, Default: "168h", Example: "168h",
		Description: "How long host samples are kept (1 hour to 365 days). The charts on the Host page can show at most this much."},
	{Name: "RIVET_METRICS_TOKEN", Group: GroupMonitor, Kind: KindSecret,
		Description: "Bearer secret (24 or more characters) that enables the Prometheus /metrics endpoint. Empty keeps the endpoint off."},

	// Sign-in and address: stored by the panel itself, see Panel settings.
	{Name: "RIVET_PUBLIC_URL", Group: GroupAccounts, Kind: KindURL, Managed: "/admin/settings",
		Description: "The https address people use. Edited under Panel settings."},
	{Name: "RIVET_OAUTH_ALLOW_SIGNUP", Group: GroupAccounts, Kind: KindBool, Managed: "/admin/settings",
		Description: "Whether unknown GitHub or Discord identities may create accounts. Edited under Panel settings."},
	{Name: "RIVET_GITHUB_CLIENT_ID", Group: GroupAccounts, Kind: KindText, Managed: "/admin/settings",
		Description: "GitHub OAuth client ID. Edited under Panel settings."},
	{Name: "RIVET_GITHUB_CLIENT_SECRET", Group: GroupAccounts, Kind: KindSecret, Managed: "/admin/settings",
		Description: "GitHub OAuth client secret. Edited under Panel settings."},
	{Name: "RIVET_DISCORD_CLIENT_ID", Group: GroupAccounts, Kind: KindText, Managed: "/admin/settings",
		Description: "Discord OAuth client ID. Edited under Panel settings."},
	{Name: "RIVET_DISCORD_CLIENT_SECRET", Group: GroupAccounts, Kind: KindSecret, Managed: "/admin/settings",
		Description: "Discord OAuth client secret. Edited under Panel settings."},
	{Name: "RIVET_MAILGUN_API_KEY", Group: GroupAccounts, Kind: KindSecret, Managed: "/admin/settings",
		Description: "Mailgun API key used to send email. Edited under Panel settings."},
	{Name: "RIVET_MAILGUN_DOMAIN", Group: GroupAccounts, Kind: KindText, Managed: "/admin/settings",
		Description: "Mailgun sending domain. Edited under Panel settings."},
	{Name: "RIVET_MAILGUN_REGION", Group: GroupAccounts, Kind: KindEnum, Options: []string{"us", "eu"}, Managed: "/admin/settings",
		Description: "Mailgun region of the account (us or eu). Edited under Panel settings."},
	{Name: "RIVET_MAIL_FROM", Group: GroupAccounts, Kind: KindText, Managed: "/admin/settings",
		Description: "Sender of panel email, like RivetPanel <noreply@example.com>. Edited under Panel settings."},
}

var varIndex = func() map[string]VarSpec {
	m := make(map[string]VarSpec, len(Vars))
	for _, v := range Vars {
		m[v.Name] = v
	}
	return m
}()

// Spec returns the description of a variable.
func Spec(name string) (VarSpec, bool) { v, ok := varIndex[name]; return v, ok }

// Overlay layers overrides over a base lookup. An override wins even when it
// is empty, which switches off a feature the base would enable. Names that are
// not editable are never overridden.
func Overlay(base Lookup, overrides map[string]string) Lookup {
	return func(name string) (string, bool) {
		if v, ok := overrides[name]; ok {
			if spec, known := Spec(name); known && spec.Editable() {
				return strings.TrimSpace(v), true
			}
		}
		return base(name)
	}
}

// CheckValue reports a problem with one proposed value, in words an
// administrator can act on. It checks syntax only; Validate checks the whole.
func CheckValue(spec VarSpec, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil // blank means "unset": the built-in default applies
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s must be a single line", spec.Name)
	}
	if len(value) > 1024 {
		return fmt.Errorf("%s is too long", spec.Name)
	}
	switch spec.Kind {
	case KindInt, KindBytes:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Errorf("%s must be a whole number", spec.Name)
		}
	case KindDuration:
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("%s must be a duration such as 30s, 10m or 24h", spec.Name)
		}
	case KindBool:
		if value != "0" && value != "1" {
			return fmt.Errorf("%s must be 1 (on) or 0 (off)", spec.Name)
		}
	case KindEnum:
		for _, o := range spec.Options {
			if o == value {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s", spec.Name, strings.Join(spec.Options, ", "))
	}
	return nil
}
