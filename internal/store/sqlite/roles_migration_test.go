package sqlite

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
)

// 0044 is additive: accounts that existed before it keep their role and
// count as verified; the two system roles are seeded.
func TestRolesMigrationKeepsAccounts(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	before := fstest.MapFS{}
	all, _ := fs.Glob(migrations.FS, "*.sql")
	for _, f := range all {
		if !strings.HasPrefix(f, "0044_") {
			b, _ := fs.ReadFile(migrations.FS, f)
			before[f] = &fstest.MapFile{Data: b}
		}
	}
	if err := db.Migrate(ctx, before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id,email,password_hash,role,created_at_ms,updated_at_ms) VALUES ('a','a@x.io','h','admin',1,1)`,
		`INSERT INTO users (id,email,password_hash,role,created_at_ms,updated_at_ms) VALUES ('u','u@x.io','h','user',1,1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "u"} {
		u, err := db.GetUserByID(ctx, id)
		if err != nil || !u.EmailVerified || u.RoleID != "" {
			t.Fatalf("%s after migration: %+v %v", id, u, err)
		}
	}
	roles, err := db.ListRoles(ctx)
	if err != nil || len(roles) != 2 || roles[0].Users != 1 || roles[1].Users != 1 {
		t.Fatalf("roles: %+v %v", roles, err)
	}
	// New accounts are inserted unverified and a custom role can be assigned.
	if err := db.CreateUser(ctx, domain.User{ID: "n", Email: "n@x.io", Role: domain.RoleUser, CreatedAtMS: 2, UpdatedAtMS: 2}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRole(ctx, domain.Role{ID: "r", Name: "Ops", Permissions: []string{domain.PermBotsPower, "gone.permission"}, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserCustomRole(ctx, "n", "r", 3); err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserCustomRole(ctx, "a", "r", 3); err != ErrLastAdmin {
		t.Fatalf("last admin: %v", err)
	}
	n, _ := db.GetUserByID(ctx, "n")
	if n.EmailVerified || n.RoleID != "r" || n.RoleName != "Ops" || len(n.CustomPermissions) != 1 {
		t.Fatalf("new account: %+v", n)
	}
	if err := db.DeleteRole(ctx, "r"); err != ErrRoleInUse {
		t.Fatalf("delete in use: %v", err)
	}
	if err := db.SetUserRole(ctx, "n", domain.RoleUser, 4); err != nil {
		t.Fatal(err)
	}
	if n, _ = db.GetUserByID(ctx, "n"); n.RoleID != "" {
		t.Fatal("built-in role must clear the custom role")
	}
	if err := db.DeleteRole(ctx, "r"); err != nil {
		t.Fatal(err)
	}
}
