package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func seed(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	db.EnsureLocalNode(ctx)
	// u1 has a password, u2 is OAuth-only (empty hash), u3 is a plain user.
	for _, u := range [][2]string{{"u1", "hash"}, {"u2", ""}, {"u3", "hash"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES (?,?,?,1,1)`,
			u[0], u[0]+"@x", u[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"b1", "b2"} {
		err := db.CreateBot(ctx, domain.Bot{ID: id, OwnerID: "u1", NodeID: domain.LocalNodeID, Name: id, Runtime: "nodejs",
			ImageRef: "i", Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func oa(user, provider, pid string) domain.OAuthAccount {
	return domain.OAuthAccount{Provider: provider, ProviderUserID: pid, UserID: user, Username: pid, CreatedAtMS: 1, UpdatedAtMS: 1, NotifyEnabled: true}
}

func TestOAuthLinkAndLastMethodGuard(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()

	// OAuth-only user u2 with one provider: cannot unlink it.
	if err := db.UpsertOAuthAccount(ctx, oa("u2", "github", "g2")); err != nil {
		t.Fatal(err)
	}
	if err := db.UnlinkOAuthAccount(ctx, "u2", "github"); !errors.Is(err, domain.ErrLastAuthMethod) {
		t.Fatalf("err = %v", err)
	}
	if _, err := db.GetOAuthAccount(ctx, "github", "g2"); err != nil {
		t.Fatal("rejected unlink must roll back:", err)
	}
	// With a second provider, one may be unlinked but not the other.
	if err := db.UpsertOAuthAccount(ctx, oa("u2", "discord", "d2")); err != nil {
		t.Fatal(err)
	}
	if err := db.UnlinkOAuthAccount(ctx, "u2", "github"); err != nil {
		t.Fatal(err)
	}
	if err := db.UnlinkOAuthAccount(ctx, "u2", "discord"); !errors.Is(err, domain.ErrLastAuthMethod) {
		t.Fatalf("err = %v", err)
	}
	// A user with a password may unlink their only provider.
	db.UpsertOAuthAccount(ctx, oa("u1", "github", "g1"))
	if err := db.UnlinkOAuthAccount(ctx, "u1", "github"); err != nil {
		t.Fatal(err)
	}
	if err := db.UnlinkOAuthAccount(ctx, "u1", "github"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestOAuthIdentityCannotBeStolen(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()
	if err := db.UpsertOAuthAccount(ctx, oa("u1", "github", "g")); err != nil {
		t.Fatal(err)
	}
	// Same external identity, different panel user.
	if err := db.UpsertOAuthAccount(ctx, oa("u3", "github", "g")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	// Second github identity for the same user.
	if err := db.UpsertOAuthAccount(ctx, oa("u1", "github", "other")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	// Refresh by the owner works.
	a := oa("u1", "github", "g")
	a.Username = "renamed"
	if err := db.UpsertOAuthAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetOAuthAccount(ctx, "github", "g")
	if got.Username != "renamed" {
		t.Fatal(got.Username)
	}
	// Partial token columns violate the CHECK.
	bad := oa("u3", "discord", "d")
	bad.TokenCipher = make([]byte, 16)
	if err := db.UpsertOAuthAccount(ctx, bad); err == nil {
		t.Fatal("expected check violation for partial token")
	}
}

func TestSubUsers(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()
	su := domain.SubUser{BotID: "b1", UserID: "u3", Permissions: domain.PermViewConsole | domain.PermPower, CreatedAtMS: 1, UpdatedAtMS: 1}
	if err := db.SetSubUser(ctx, su); err != nil {
		t.Fatal(err)
	}
	su.Permissions = domain.PermFullAdmin
	if err := db.SetSubUser(ctx, su); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetSubUserPermissions(ctx, "b1", "u3")
	if err != nil || !domain.HasPerm(p, domain.PermManageEnv) {
		t.Fatal(p, err)
	}
	if _, err := db.GetSubUserPermissions(ctx, "b2", "u3"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if err := db.SetSubUser(ctx, domain.SubUser{BotID: "b1", UserID: "u1", Permissions: 1}); err == nil {
		t.Fatal("owner must not become a sub-user")
	}
	if err := db.SetSubUser(ctx, domain.SubUser{BotID: "b1", UserID: "u2", Permissions: 0}); err == nil {
		t.Fatal("empty permissions must be rejected")
	}
	if list, _ := db.ListSubUsers(ctx, "b1"); len(list) != 1 || list[0].Email != "u3@x" {
		t.Fatal(list)
	}
	// Deleting the bot cascades the grant.
	db.ExecContext(ctx, `DELETE FROM bots WHERE id = 'b1'`)
	if list, _ := db.ListSubUsers(ctx, "b1"); len(list) != 0 {
		t.Fatal("grants must cascade")
	}
	if !domain.HasPerm(domain.PermPower, domain.PermPower) || domain.HasPerm(domain.PermPower, domain.PermEditFiles) {
		t.Fatal("HasPerm")
	}
}

func TestAPIKeys(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()
	h := make([]byte, 32)
	h[0] = 1
	k := domain.APIKey{ID: "k1", UserID: "u1", Name: "sftp", Prefix: "bp_ab", TokenHash: h, Scope: "sftp", CreatedAtMS: 1}
	if err := db.InsertAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if u, err := db.GetUserByAPIKey(ctx, h, "sftp", 10); err != nil || u.ID != "u1" {
		t.Fatal(u, err)
	}
	exp := int64(5)
	k2 := domain.APIKey{ID: "k2", UserID: "u1", Name: "e", Prefix: "bp_cd", TokenHash: append([]byte{2}, h[1:]...), Scope: "sftp", CreatedAtMS: 1, ExpiresAtMS: &exp}
	db.InsertAPIKey(ctx, k2)
	if _, err := db.GetUserByAPIKey(ctx, k2.TokenHash, "sftp", 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("expired key must not authenticate", err)
	}
	db.SetUserDisabled(ctx, "u1", true, 20)
	if _, err := db.GetUserByAPIKey(ctx, h, "sftp", 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("disabled user's key must not authenticate", err)
	}
	if err := db.DeleteAPIKey(ctx, "u3", "k1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("other user must not delete key", err)
	}
}

func TestBotTelemetryAndKey(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()
	v := 42.0
	rows := []domain.BotTelemetryLog{
		{BotID: "b1", RecordedAtMS: 100, Kind: "stat", Name: "guilds", Value: &v},
		{BotID: "b1", RecordedAtMS: 200, Kind: "stat", Name: "guilds", Value: &v},
	}
	if err := db.InsertBotTelemetry(ctx, rows); err != nil {
		t.Fatal(err)
	}
	tooBig := `"` + string(bytesOf('a', 2100)) + `"`
	if err := db.InsertBotTelemetry(ctx, []domain.BotTelemetryLog{{BotID: "b1", RecordedAtMS: 1, Kind: "event", Name: "e", PayloadJSON: &tooBig}}); err == nil {
		t.Fatal("oversize payload must be rejected")
	}
	if got, _ := db.ListBotTelemetry(ctx, "b1", "stat", 0, 10); len(got) != 2 || got[0].RecordedAtMS != 100 {
		t.Fatal(got)
	}
	if n, _ := db.PruneBotTelemetry(ctx, 150, 10); n != 1 {
		t.Fatal(n)
	}

	h := make([]byte, 32)
	h[5] = 9
	if err := db.SetBotTelemetryKey(ctx, "b1", h, 3); err != nil {
		t.Fatal(err)
	}
	if id, err := db.GetBotIDByTelemetryKey(ctx, h); err != nil || id != "b1" {
		t.Fatal(id, err)
	}
	if err := db.SetBotTelemetryKey(ctx, "b2", h, 3); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("key hash must be unique", err)
	}
	db.SetBotTelemetryKey(ctx, "b1", nil, 4)
	if _, err := db.GetBotIDByTelemetryKey(ctx, h); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cleared key must not authenticate", err)
	}
}

func bytesOf(c byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return b
}

func TestBackupsPortsAndRepos(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()

	b := domain.Backup{ID: "bk1", BotID: "b1", Kind: "manual", Status: "creating", FileName: "bk1.tar.gz", IncludesEnv: true, CreatedAtMS: 1}
	if err := db.InsertBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	sha := "abc"
	if err := db.FinishBackup(ctx, "bk1", "ready", 10, &sha, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := db.GetBackup(ctx, "b2", "bk1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("backup must be scoped to its bot", got, err)
	}
	for _, name := range []string{"../evil.tar.gz", "a/b.tar.gz", ".hidden"} {
		b2 := b
		b2.ID, b2.FileName = "x"+name, name
		if err := db.InsertBackup(ctx, b2); err == nil {
			t.Fatalf("file_name %q must be rejected", name)
		}
	}

	p := domain.BotPort{ContainerPort: 8080, HostPort: 20080, Protocol: "tcp", HostIP: "127.0.0.1", CreatedAtMS: 1}
	if err := db.ReplaceBotPorts(ctx, "b1", []domain.BotPort{p}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceBotPorts(ctx, "b2", []domain.BotPort{p}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("host port collision", err)
	}
	if ports, _ := db.ListBotPorts(ctx, "b2"); len(ports) != 0 {
		t.Fatal("failed replace must roll back")
	}
	p.HostPort = 80
	if err := db.ReplaceBotPorts(ctx, "b1", []domain.BotPort{p}); err == nil {
		t.Fatal("privileged host port must be rejected")
	}
	if ports, _ := db.ListBotPorts(ctx, "b1"); len(ports) != 1 || ports[0].HostPort != 20080 {
		t.Fatal("failed replace must keep the previous set", ports)
	}

	r := domain.GitHubRepo{BotID: "b1", TokenUserID: "u1", FullName: "Org/Repo", Branch: "main", AutoDeploy: true,
		SecretCipher: make([]byte, 32), SecretNonce: make([]byte, 12), SecretKeyID: "k", CreatedAtMS: 1, UpdatedAtMS: 1}
	if err := db.UpsertGitHubRepo(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ListAutoDeployRepos(ctx, "org/repo"); len(got) != 1 {
		t.Fatal("full_name lookup must be case-insensitive", got)
	}
	r.FullName = "noslash"
	if err := db.UpsertGitHubRepo(ctx, r); err == nil {
		t.Fatal("full_name must be owner/repo")
	}
	sha2 := "deadbeef"
	db.RecordDeploy(ctx, "b1", &sha2, nil, 9)
	if got, _ := db.GetGitHubRepo(ctx, "b1"); got.LastSHA == nil || *got.LastSHA != sha2 {
		t.Fatal(got)
	}
}
