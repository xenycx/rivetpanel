package sqlite

import (
	"context"
	"slices"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Uninstalling an extension removes its permission from every role, so the
// roles stay valid and a later extension installed under the same id starts
// with nobody holding it; other extensions' permissions are kept.
func TestDeleteExtensionStripsItsPermission(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	const gone, kept = "ext:dev.example.gone", "ext:dev.example.kept"
	if err := db.CreateRole(ctx, domain.Role{ID: "r", Name: "Support", Permissions: []string{domain.PermBotsConsole, gone, kept}, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRole(ctx, domain.Role{ID: domain.SystemRoleUser, Name: "User", Permissions: []string{domain.PermBotsFiles, gone}, UpdatedAtMS: 2}); err != nil {
		t.Fatal(err)
	}
	r, err := db.GetRole(ctx, "r")
	if err != nil || !slices.Contains(r.Permissions, gone) {
		t.Fatalf("stored role lost the extension permission: %v %v", r.Permissions, err)
	}
	if err := db.PutExtension(ctx, domain.Extension{ID: "dev.example.gone", Source: domain.ExtSourceUpload, ActiveVersion: "1.0.0", State: domain.ExtEnabled, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteExtension(ctx, "dev.example.gone"); err != nil {
		t.Fatal(err)
	}
	r, _ = db.GetRole(ctx, "r")
	if !slices.Equal(r.Permissions, []string{domain.PermBotsConsole, kept}) {
		t.Fatalf("custom role after uninstall = %v", r.Permissions)
	}
	u, _ := db.GetRole(ctx, domain.SystemRoleUser)
	if !slices.Equal(u.Permissions, []string{domain.PermBotsFiles}) {
		t.Fatalf("user role after uninstall = %v", u.Permissions)
	}
	if a, _ := db.GetRole(ctx, domain.SystemRoleAdmin); !slices.Equal(a.Permissions, domain.AllPermissions()) {
		t.Fatalf("administrator role changed: %v", a.Permissions)
	}
}
