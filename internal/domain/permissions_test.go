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
