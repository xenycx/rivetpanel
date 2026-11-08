package domain

import (
	"slices"
	"testing"
)

func TestMaskBotPermsAndCan(t *testing.T) {
	admin := User{Role: RoleAdmin, Withheld: []string{PermBotsCreate}}
	if !admin.Can(PermBotsCreate) || admin.MaskBotPerms(PermFullAdmin) != PermFullAdmin {
		t.Fatal("administrators are never limited")
	}
	user := User{Role: RoleUser}
	if !user.Can(PermBotsFiles) || user.Can(PermUsersView) || user.MaskBotPerms(PermAll) != PermAll {
		t.Fatal("built-in user role changed")
	}
	custom := User{Role: RoleUser, RoleID: "r", CustomPermissions: []string{PermBotsConsole, PermBotsPower}}
	got := custom.MaskBotPerms(PermFullAdmin)
	if got != PermViewConsole|PermPower || HasPerm(got, PermEditFiles) {
		t.Fatalf("full access not masked: %b", got)
	}
	if custom.CanBotBit(PermEditFiles) || !custom.CanBotBit(PermPower) || custom.BotBitDenied(PermManageEnv) == nil {
		t.Fatal("bit checks")
	}
	// bots.configure keeps a bot's startup configuration on full access the
	// role trimmed, and only there.
	cfg := User{Role: RoleUser, RoleID: "r", CustomPermissions: []string{PermBotsConsole, PermBotsConfigure}}
	if got := cfg.MaskBotPerms(PermFullAdmin); got != PermViewConsole|PermConfigure || HasPerm(got, PermEditFiles) || HasPerm(got, PermFullAdmin) {
		t.Fatalf("configure on full access: %b", got)
	}
	if got := cfg.MaskBotPerms(PermViewConsole | PermEditFiles); HasPerm(got, PermConfigure) {
		t.Fatalf("configure without full access: %b", got)
	}
	if !HasPerm(user.MaskBotPerms(PermAll), PermConfigure) || HasPerm(custom.MaskBotPerms(PermFullAdmin), PermConfigure) {
		t.Fatal("full access implies configure; a role without it gets none")
	}
	unverified := User{Role: RoleUser, Withheld: []string{PermBotsCreate}}
	if unverified.Can(PermBotsCreate) || !unverified.Can(PermBotsFiles) {
		t.Fatal("policy not applied")
	}
	if e, ok := Denied(unverified, PermBotsCreate).(*PermissionError); !ok || !e.Unverified {
		t.Fatal("denial should name verification")
	}
	if slices.Contains(unverified.EffectivePermissions(), PermBotsCreate) {
		t.Fatal("effective list")
	}
}

// A User built in memory (never read from the store) keeps the factory
// defaults of the built-in User role; one read from the store gets exactly
// the stored list, even an empty one.
func TestRolePermissionsInMemoryDefaults(t *testing.T) {
	if got := (User{Role: RoleUser}).RolePermissions(); !slices.Equal(got, DefaultUserPermissions()) {
		t.Fatalf("in-memory user: %v", got)
	}
	loaded := User{Role: RoleUser, PermissionsLoaded: true, CustomPermissions: []string{PermBotsConsole}}
	if !loaded.Can(PermBotsConsole) || loaded.Can(PermBotsFiles) || loaded.Can(PermBotsCreate) {
		t.Fatalf("loaded user: %v", loaded.EffectivePermissions())
	}
	if empty := (User{Role: RoleUser, PermissionsLoaded: true}); len(empty.EffectivePermissions()) != 0 {
		t.Fatalf("empty stored list: %v", empty.EffectivePermissions())
	}
	if got := (User{Role: RoleAdmin, PermissionsLoaded: true, CustomPermissions: []string{}}).RolePermissions(); !slices.Equal(got, AllPermissions()) {
		t.Fatalf("administrator: %v", got)
	}
}

// Per-extension permissions: valid for well-formed ids whether or not the
// extension is installed, held by administrators, granted by roles, and never
// part of the User role's defaults or the administration area.
func TestExtensionPermissions(t *testing.T) {
	const p = "ext:dev.rivetpanel.db-browser"
	for _, ok := range []string{p, "ext:dev.example.tool", ExtensionPermission("a.b")} {
		if !ValidPermission(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"ext:", "ext:Tool", "ext:tool", "ext:dev..tool", "ext:dev.tool/x", "ext: dev.tool", "ext:" + string(make([]byte, 70)), "extension", "EXT:dev.tool"} {
		if ValidPermission(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if id, ok := ExtensionOfPermission(p); !ok || id != "dev.rivetpanel.db-browser" {
		t.Fatalf("ExtensionOfPermission = %q %v", id, ok)
	}
	if slices.Contains(DefaultUserPermissions(), p) || slices.Contains(AllPermissions(), p) {
		t.Fatal("extension permissions are not in the fixed catalog")
	}
	if !(User{Role: RoleAdmin}).Can(p) {
		t.Fatal("administrators hold every extension permission")
	}
	if (User{Role: RoleUser}).Can(p) {
		t.Fatal("the built-in User role defaults do not include extension permissions")
	}
	custom := User{Role: RoleUser, RoleID: "r", PermissionsLoaded: true, CustomPermissions: []string{PermBotsConsole, p}}
	if !custom.Can(p) || custom.Can("ext:dev.example.other") || custom.HasAnyAdminPermission() {
		t.Fatal("custom role")
	}
	if eff := custom.EffectivePermissions(); !slices.Equal(eff, []string{PermBotsConsole, p}) {
		t.Fatalf("effective = %v", eff)
	}
	if (User{Role: RoleUser, RoleID: "r", PermissionsLoaded: true, CustomPermissions: []string{p}, Withheld: []string{p}}).Can(p) {
		t.Fatal("the unverified-email policy can withhold an extension permission")
	}
}
