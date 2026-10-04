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
