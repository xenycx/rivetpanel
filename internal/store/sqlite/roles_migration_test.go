package sqlite

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
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
		// Later migrations (0057 onwards) change the roles table, so stop
		// before 0044 and let the second run apply it and everything after.
		if f < "0044_" {
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

// 0057 writes the factory list into the User role (which the store reads
// from then on) and adds workspaces.rename to custom roles exactly once.
func TestMigration0057SeedsUserRole(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, upTo(t, 56)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO roles (id, name, description, permissions_json, system, created_at_ms, updated_at_ms)
			VALUES ('ops', 'Ops', '', '["bots.console"]', 0, 1, 1)`,
		`INSERT INTO roles (id, name, description, permissions_json, system, created_at_ms, updated_at_ms)
			VALUES ('ren', 'Renamers', '', '["workspaces.rename"]', 0, 1, 1)`,
		`INSERT INTO users (id,email,password_hash,role,created_at_ms,updated_at_ms) VALUES ('u','u@x.io','h','user',1,1)`,
		`INSERT INTO users (id,email,password_hash,role,role_id,created_at_ms,updated_at_ms) VALUES ('o','o@x.io','h','user','ops',1,1)`,
		`INSERT INTO users (id,email,password_hash,role,created_at_ms,updated_at_ms) VALUES ('a','a@x.io','h','admin',1,1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	user, err := db.GetRole(ctx, domain.SystemRoleUser)
	if err != nil || !user.System || !user.Editable || !slices.Equal(user.Permissions, domain.DefaultUserPermissions()) {
		t.Fatalf("user role: %+v %v", user, err)
	}
	adm, err := db.GetRole(ctx, domain.SystemRoleAdmin)
	if err != nil || adm.Editable || !slices.Equal(adm.Permissions, domain.AllPermissions()) {
		t.Fatalf("admin role: %+v %v", adm, err)
	}
	ops, _ := db.GetRole(ctx, "ops")
	if !slices.Equal(ops.Permissions, []string{domain.PermBotsConsole, domain.PermWorkspacesRename}) {
		t.Fatalf("custom role: %v", ops.Permissions)
	}
	var raw string
	db.QueryRowContext(ctx, `SELECT permissions_json FROM roles WHERE id = 'ren'`).Scan(&raw)
	if raw != `["workspaces.rename"]` {
		t.Fatalf("rename inserted twice: %s", raw)
	}

	// Accounts read the stored list of their role; administrators are computed.
	u, err := db.GetUserByID(ctx, "u")
	if err != nil || u.RoleID != "" || !u.PermissionsLoaded || !slices.Equal(u.RolePermissions(), domain.DefaultUserPermissions()) {
		t.Fatalf("user account: %+v %v", u, err)
	}
	o, _ := db.GetUserByID(ctx, "o")
	if o.RoleID != "ops" || o.RoleName != "Ops" || !slices.Equal(o.RolePermissions(), ops.Permissions) {
		t.Fatalf("custom account: %+v", o)
	}
	a, _ := db.GetUserByID(ctx, "a")
	if !slices.Equal(a.RolePermissions(), domain.AllPermissions()) {
		t.Fatalf("admin account: %+v", a)
	}

	// Changing the User role changes what its accounts get; the
	// administrator role is never changed and neither system role deleted.
	if err := db.UpdateRole(ctx, domain.Role{ID: domain.SystemRoleUser, Name: "User", Permissions: []string{domain.PermBotsConsole}, UpdatedAtMS: 2}); err != nil {
		t.Fatal(err)
	}
	if u, _ = db.GetUserByID(ctx, "u"); !u.Can(domain.PermBotsConsole) || u.Can(domain.PermBotsCreate) {
		t.Fatalf("after change: %+v", u)
	}
	if err := db.UpdateRole(ctx, domain.Role{ID: domain.SystemRoleAdmin, Name: "Administrator", Permissions: nil, UpdatedAtMS: 2}); err != domain.ErrNotFound {
		t.Fatalf("admin update: %v", err)
	}
	if a, _ = db.GetUserByID(ctx, "a"); !a.Can(domain.PermBotsCreate) {
		t.Fatal("administrator changed")
	}
	for _, id := range []string{domain.SystemRoleUser, domain.SystemRoleAdmin} {
		if err := db.DeleteRole(ctx, id); err != domain.ErrNotFound {
			t.Fatalf("delete %s: %v", id, err)
		}
	}
}
