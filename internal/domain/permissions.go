package domain

import "slices"

// Panel permissions. A user's role grants a set of these; they are checked by
// the API (route middleware) and, for bot access, by BotService itself, so a
// permission missing from a role is refused on the server, not only hidden in
// the interface. The built-in administrator role holds every permission.
const (
	// Resource permissions: what an account may do with its own (or shared)
	// bots, servers, sites and workspaces. The built-in user role has all of
	// them; a custom role may leave some out.
	PermBotsCreate       = "bots.create"
	PermBotsDelete       = "bots.delete"
	PermBotsConsole      = "bots.console"
	PermBotsPower        = "bots.power"
	PermBotsFiles        = "bots.files"
	PermBotsEnv          = "bots.env"
	PermBotsBackups      = "bots.backups"
	PermBotsDeploy       = "bots.deploy"
	PermBotsShare        = "bots.share"
	PermSitesCreate      = "sites.create"
	PermWorkspacesCreate = "workspaces.create"
	PermAPIKeys          = "api_keys.manage"
	PermAIUse            = "ai.use"
	PermTicketsCreate    = "tickets.create"

	// Administration permissions: parts of the administration area a custom
	// role may delegate. Built-in administrators always have them.
	PermUsersView        = "users.view"
	PermUsersManage      = "users.manage"
	PermRolesManage      = "roles.manage"
	PermNodesManage      = "nodes.manage"
	PermBlueprintsManage = "blueprints.manage"
	PermAllocations      = "allocations.manage"
	PermSettingsManage   = "settings.manage"
	PermMailAnnounce     = "mail.announce"
	PermAIManage         = "ai.manage"
	PermSitesManage      = "sites.manage"
	PermWorkspacesView   = "workspaces.view"
	PermSystemView       = "system.view"
	PermTicketsViewAll   = "tickets.view_all"
	PermTicketsManage    = "tickets.manage"
	PermKBManage         = "kb.manage"
	PermStatusManage     = "status.manage"
	PermAnalyticsView    = "analytics.view"
)

// PermissionInfo describes one permission for the roles editor.
type PermissionInfo struct {
	Name        string `json:"name"`
	Group       string `json:"group"` // resources | administration
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Permissions is the catalog, in display order.
var Permissions = []PermissionInfo{
	{PermBotsCreate, "resources", "Create bots and servers", "Create bots and game servers in workspaces the account may create in."},
	{PermBotsDelete, "resources", "Delete bots and servers", "Delete bots and servers the account owns or administers."},
	{PermBotsConsole, "resources", "View console", "Read console output and logs."},
	{PermBotsPower, "resources", "Power and console input", "Start, stop, restart and kill; send console input and commands."},
	{PermBotsFiles, "resources", "Files", "File manager, SFTP, packages and AI file changes."},
	{PermBotsEnv, "resources", "Environment variables", "Read and change environment variables."},
	{PermBotsBackups, "resources", "Backups", "Create, restore, verify and delete backups."},
	{PermBotsDeploy, "resources", "Deployments", "Link GitHub repositories, deploy, publish and push."},
	{PermBotsShare, "resources", "Sharing", "Share bots with other accounts, invitations and ownership transfer."},
	{PermSitesCreate, "resources", "Create sites", "Create static sites."},
	{PermWorkspacesCreate, "resources", "Create workspaces", "Create team workspaces."},
	{PermAPIKeys, "resources", "API keys and tokens", "Create SFTP API keys, automation tokens and API clients."},
	{PermAIUse, "resources", "AI assistant", "Use the AI assistant."},
	{PermTicketsCreate, "resources", "Support tickets", "Open support tickets and reply to the account's own tickets."},

	{PermUsersView, "administration", "View accounts", "List accounts and open an account's details."},
	{PermUsersManage, "administration", "Manage accounts", "Create, enable and disable accounts, assign roles they hold themselves, verify email addresses and send account invitations."},
	{PermRolesManage, "administration", "Manage roles", "Create, change and delete custom roles (only with permissions they hold themselves)."},
	{PermNodesManage, "administration", "Nodes", "Locations, node enrollment, certificates and node settings; placing bots on nodes."},
	{PermBlueprintsManage, "administration", "Server types", "Import, enable, disable and delete game server blueprints."},
	{PermAllocations, "administration", "Allocations", "Create, change and delete port allocations."},
	{PermSettingsManage, "administration", "Panel settings", "Panel address, sign-in providers, registration, email and the unverified-account policy."},
	{PermMailAnnounce, "administration", "Announcements", "Send announcements to accounts by email."},
	{PermAIManage, "administration", "AI providers", "AI provider profiles and web search settings."},
	{PermSitesManage, "administration", "Sites and domains", "Every site, site base domains and disabling sites."},
	{PermWorkspacesView, "administration", "View workspaces", "List every workspace and read its details."},
	{PermSystemView, "administration", "Host and diagnostics", "Host monitoring, panel logs and the diagnostics report."},
	{PermTicketsViewAll, "administration", "View all tickets", "Read every support ticket, including internal staff notes."},
	{PermTicketsManage, "administration", "Answer tickets", "Read and answer every support ticket, write internal notes, change status and priority, and assign tickets."},
	{PermKBManage, "administration", "Knowledgebase", "Write, publish and delete help articles and categories, and choose whether the help center is public."},
	{PermStatusManage, "administration", "Status page", "Configure the public status page, its components, incidents and scheduled maintenance."},
	{PermAnalyticsView, "administration", "Usage analytics", "Panel-wide usage analytics: accounts, bots and servers, resource use, deployments, backups and top consumers (nodes only with Nodes, tickets only with ticket access)."},
}

// ValidPermission reports whether p is in the catalog.
func ValidPermission(p string) bool {
	for _, x := range Permissions {
		if x.Name == p {
			return true
		}
	}
	return false
}

// DefaultUserPermissions is what the built-in user role grants: every
// resource permission and nothing in the administration area (the behaviour
// regular accounts had before roles existed).
func DefaultUserPermissions() []string {
	var out []string
	for _, x := range Permissions {
		if x.Group == "resources" {
			out = append(out, x.Name)
		}
	}
	return out
}

// AllPermissions is every permission in the catalog.
func AllPermissions() []string {
	out := make([]string, len(Permissions))
	for i, x := range Permissions {
		out[i] = x.Name
	}
	return out
}

// Role is a named set of permissions. System roles ("admin" and "user") are
// seeded, map to users.role and cannot be changed or deleted.
type Role struct {
	ID          string
	Name        string
	Description string
	Permissions []string
	System      bool
	Users       int // accounts holding it (listing only)
	CreatedAtMS int64
	UpdatedAtMS int64
}

// System role ids (equal to the users.role values).
const (
	SystemRoleAdmin = RoleAdmin
	SystemRoleUser  = RoleUser
)

// botPermBits maps the per-bot permission bits to the role permission that
// must also be held.
var botPermBits = []struct {
	bit  int
	perm string
}{
	{PermViewConsole, PermBotsConsole},
	{PermPower, PermBotsPower},
	{PermEditFiles, PermBotsFiles},
	{PermManageEnv, PermBotsEnv},
}

// MaskBotPerms removes from a per-bot permission mask the bits the user's
// role does not allow. PermFullAdmin is expanded first, so a role without
// file access cannot regain it through a full-access sub-user grant.
func (u User) MaskBotPerms(mask int) int {
	if u.IsAdmin() {
		return mask
	}
	if mask&PermFullAdmin != 0 {
		mask = PermAll
	}
	for _, b := range botPermBits {
		if !u.Can(b.perm) {
			mask &^= b.bit
		}
	}
	if mask != PermAll {
		mask &^= PermFullAdmin
	}
	return mask
}

// CanBotBit reports whether the role allows a single per-bot permission bit
// (any bit not mapped to a role permission is allowed).
func (u User) CanBotBit(bit int) bool {
	if u.IsAdmin() {
		return true
	}
	for _, b := range botPermBits {
		if bit&b.bit != 0 && !u.Can(b.perm) {
			return false
		}
	}
	return true
}

// Can reports whether the account holds permission p: administrators hold
// everything; otherwise the custom role (or the built-in user role) decides,
// minus anything the unverified-email policy withholds.
func (u User) Can(p string) bool {
	if u.Client != nil && !slices.Contains(u.Client.Permissions, p) {
		return false
	}
	if u.Role == RoleAdmin {
		return true
	}
	if slices.Contains(u.Withheld, p) {
		return false
	}
	return slices.Contains(u.RolePermissions(), p)
}

// RolePermissions is what the account's role grants, before the
// unverified-email policy.
func (u User) RolePermissions() []string {
	if u.Role == RoleAdmin {
		return AllPermissions()
	}
	if u.RoleID == "" {
		return DefaultUserPermissions()
	}
	return u.CustomPermissions
}

// EffectivePermissions is the sorted list of permissions Can grants.
func (u User) EffectivePermissions() []string {
	var out []string
	for _, p := range AllPermissions() {
		if u.Can(p) {
			out = append(out, p)
		}
	}
	return out
}

// HasAnyAdminPermission reports whether the account may open some part of
// the administration area.
func (u User) HasAnyAdminPermission() bool {
	for _, x := range Permissions {
		if x.Group == "administration" && u.Can(x.Name) {
			return true
		}
	}
	return false
}

// BotBitDenied is the permission error for a per-bot bit the role does not
// allow, or nil.
func (u User) BotBitDenied(bit int) error {
	if u.IsAdmin() {
		return nil
	}
	for _, b := range botPermBits {
		if bit&b.bit != 0 && !u.Can(b.perm) {
			return Denied(u, b.perm)
		}
	}
	return nil
}
