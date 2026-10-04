package api

import (
	"encoding/json"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestInvitationLinks(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner@x.io", domain.RoleUser)
	guest := e.user("guest@x.io", domain.RoleUser)
	other := e.user("other@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id

	owner.mustStatus(400, "POST", base+"/invites", map[string]any{"permissions": domain.PermViewConsole, "expires_in_days": 30})
	guest.mustStatus(404, "POST", base+"/invites", map[string]any{"permissions": 1, "expires_in_days": 1})
	var inv struct {
		Token, Path string
	}
	json.Unmarshal(owner.mustStatus(201, "POST", base+"/invites", map[string]any{"permissions": domain.PermViewConsole | domain.PermPower, "expires_in_days": 7}), &inv)
	if len(inv.Token) < 20 || inv.Path != "/invite#"+inv.Token {
		t.Fatalf("invite: %+v", inv)
	}
	var list struct{ Invites []inviteDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/invites", nil), &list)
	if len(list.Invites) != 1 {
		t.Fatalf("list: %+v", list)
	}

	// Preview does not consume; the owner cannot accept their own bot.
	var pv inviteDTO
	json.Unmarshal(guest.mustStatus(200, "POST", "/api/v1/invites/preview", map[string]string{"token": inv.Token}), &pv)
	if pv.BotName != "b" || pv.Permissions != domain.PermViewConsole|domain.PermPower || pv.CreatedBy != "owner@x.io" {
		t.Fatalf("preview: %+v", pv)
	}
	owner.mustStatus(400, "POST", "/api/v1/invites/accept", map[string]string{"token": inv.Token})

	guest.mustStatus(404, "GET", base, nil)
	guest.mustStatus(200, "POST", "/api/v1/invites/accept", map[string]string{"token": inv.Token})
	var got botDTO
	json.Unmarshal(guest.mustStatus(200, "GET", base, nil), &got)
	if !got.Shared || got.Permissions != domain.PermViewConsole|domain.PermPower {
		t.Fatalf("grant: %+v", got)
	}
	// One use only.
	other.mustStatus(400, "POST", "/api/v1/invites/accept", map[string]string{"token": inv.Token})
	other.mustStatus(404, "GET", base, nil)
	other.mustStatus(400, "POST", "/api/v1/invites/accept", map[string]string{"token": "bpi_forged"})

	// Revoked links stop working.
	json.Unmarshal(owner.mustStatus(201, "POST", base+"/invites", map[string]any{"permissions": domain.PermEditFiles, "expires_in_days": 1}), &inv)
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/invites", nil), &list)
	owner.mustStatus(204, "DELETE", base+"/invites/"+list.Invites[0].ID, nil)
	other.mustStatus(400, "POST", "/api/v1/invites/accept", map[string]string{"token": inv.Token})
}
