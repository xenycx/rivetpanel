package domain_test

import (
	"io/fs"
	"regexp"
	"slices"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
)

// Migration 0057 writes the built-in User role's permissions as a literal
// list. It must stay equal to the factory default, in catalog order.
func TestMigration0057MatchesDefaultUserPermissions(t *testing.T) {
	b, err := fs.ReadFile(migrations.FS, "0057_user_role_permissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)json_array\((.*?)\)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no json_array literal in migration 0057")
	}
	var got []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllSubmatch(m[1], -1) {
		got = append(got, string(q[1]))
	}
	want := domain.DefaultUserPermissions()
	if !slices.Equal(got, want) {
		t.Fatalf("migration 0057 list = %v\nDefaultUserPermissions = %v", got, want)
	}
	if !slices.Contains(want, domain.PermWorkspacesRename) || slices.Contains(want, domain.PermWorkspacesManage) {
		t.Fatal("workspaces.rename must be a default user permission and workspaces.manage must not")
	}
}
