package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

func TestVerifyCoversOAuthAndGitHubSecrets(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	db.EnsureLocalNode(ctx)
	kr, _ := secrets.NewKeyring("k1", map[string][]byte{"k1": make([]byte, 32)})
	db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u1','u1@x','',1,1)`)
	db.CreateBot(ctx, domain.Bot{ID: "b1", OwnerID: "u1", NodeID: domain.LocalNodeID, Name: "b", Runtime: "nodejs", ImageRef: "i",
		Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1})

	tok, _ := kr.Seal(secrets.OAuthNS("u1"), secrets.OAuthTokenName("github"), []byte("gho_x"))
	hook, _ := kr.Seal(secrets.OAuthNS("u1"), secrets.OAuthWebhookName("discord"), []byte("https://discord.com/api/webhooks/1/x"))
	sec, _ := kr.Seal(secrets.GitHubNS("b1"), secrets.GitHubSecretName, []byte("s"))
	acct := domain.OAuthAccount{Provider: "github", ProviderUserID: "1", UserID: "u1", Username: "u", CreatedAtMS: 1, UpdatedAtMS: 1,
		TokenCipher: tok.Ciphertext, TokenNonce: tok.Nonce, TokenKeyID: &tok.KeyID}
	if err := db.UpsertOAuthAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	dis := domain.OAuthAccount{Provider: "discord", ProviderUserID: "2", UserID: "u1", Username: "d", CreatedAtMS: 1, UpdatedAtMS: 1}
	db.UpsertOAuthAccount(ctx, dis)
	db.SetOAuthWebhook(ctx, "u1", "discord", hook.Ciphertext, hook.Nonce, &hook.KeyID, 2)
	db.UpsertGitHubRepo(ctx, domain.GitHubRepo{BotID: "b1", TokenUserID: "u1", FullName: "o/r", Branch: "main",
		SecretCipher: sec.Ciphertext, SecretNonce: sec.Nonce, SecretKeyID: sec.KeyID, CreatedAtMS: 1, UpdatedAtMS: 1})

	rep, err := VerifyEnv(ctx, db, kr)
	if err != nil || rep.Total != 3 || !rep.Healthy() {
		t.Fatalf("%+v %v", rep, err)
	}
	// Wrong key material and missing keys are both reported.
	other, _ := secrets.NewKeyring("k1", map[string][]byte{"k1": append(make([]byte, 31), 1)})
	if rep, _ := VerifyEnv(ctx, db, other); rep.Healthy() || rep.Failed != 3 {
		t.Fatalf("wrong key: %+v", rep)
	}
	none, _ := secrets.NewKeyring("k2", map[string][]byte{"k2": make([]byte, 32)})
	if rep, _ := VerifyEnv(ctx, db, none); rep.Healthy() || len(rep.MissingKeys) != 1 || rep.MissingKeys[0] != "k1" {
		t.Fatalf("missing key: %+v", rep)
	}
}

// Reseal moves every sealed value, in every table, to the active key; after it
// the old key is no longer needed by the database. A second pass changes nothing.
func TestResealMovesEverythingToTheActiveKey(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	db.EnsureLocalNode(ctx)
	k1 := make([]byte, 32)
	k2 := append(make([]byte, 31), 7)
	old, _ := secrets.NewKeyring("k1", map[string][]byte{"k1": k1})
	db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u1','u1@x','',1,1)`)
	db.CreateBot(ctx, domain.Bot{ID: "b1", OwnerID: "u1", NodeID: domain.LocalNodeID, Name: "b", Runtime: "nodejs", ImageRef: "i",
		Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1})
	env, _ := old.Seal("b1", "DISCORD_TOKEN", []byte("tok"))
	db.UpsertEnv(ctx, "b1", []domain.EnvVar{{BotID: "b1", Name: "DISCORD_TOKEN", Ciphertext: env.Ciphertext, Nonce: env.Nonce, KeyID: env.KeyID}}, 1)
	tok, _ := old.Seal(secrets.OAuthNS("u1"), secrets.OAuthTokenName("github"), []byte("gho_x"))
	db.UpsertOAuthAccount(ctx, domain.OAuthAccount{Provider: "github", ProviderUserID: "1", UserID: "u1", Username: "u", CreatedAtMS: 1, UpdatedAtMS: 1,
		TokenCipher: tok.Ciphertext, TokenNonce: tok.Nonce, TokenKeyID: &tok.KeyID})
	mfa, _ := old.Seal(secrets.MFANS("u1"), "totp", []byte("0123456789abcdefghij"))
	db.StartMFA(ctx, domain.MFA{UserID: "u1", Cipher: mfa.Ciphertext, Nonce: mfa.Nonce, KeyID: mfa.KeyID, CreatedAtMS: 1})

	both, _ := secrets.NewKeyring("k2", map[string][]byte{"k1": k1, "k2": k2})
	fn := func(ns, name, keyID string, ct, nonce []byte) ([]byte, []byte, string, bool, error) {
		out, changed, err := both.Reseal(ns, name, secrets.Sealed{Ciphertext: ct, Nonce: nonce, KeyID: keyID})
		return out.Ciphertext, out.Nonce, out.KeyID, changed, err
	}
	st, err := db.Reseal(ctx, fn)
	if err != nil || st.Seen != 3 || st.Changed != 3 || st.Failed != 0 {
		t.Fatalf("reseal: %+v %v", st, err)
	}
	onlyNew, _ := secrets.NewKeyring("k2", map[string][]byte{"k2": k2})
	if rep, err := VerifyEnv(ctx, db, onlyNew); err != nil || rep.Total != 3 || !rep.Healthy() {
		t.Fatalf("after reseal the new key alone must open everything: %+v %v", rep, err)
	}
	if st, _ := db.Reseal(ctx, fn); st.Changed != 0 {
		t.Fatalf("second pass changed %d", st.Changed)
	}
}
