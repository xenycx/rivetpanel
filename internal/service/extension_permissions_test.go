package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/extensions/bundle"
)

// extPermStore serves installed extensions from memory; every other store
// method panics (the embedded interface is nil), so the tests notice if the
// permission checks start reaching further.
type extPermStore struct {
	ExtensionStore
	exts      []domain.Extension
	manifests map[string]bundle.Manifest
}

func (f *extPermStore) ListExtensions(context.Context) ([]domain.Extension, error) {
	return f.exts, nil
}

func (f *extPermStore) GetExtension(_ context.Context, id string) (domain.Extension, error) {
	for _, e := range f.exts {
		if e.ID == id {
			return e, nil
		}
	}
	return domain.Extension{}, domain.ErrNotFound
}

func (f *extPermStore) GetExtensionVersion(_ context.Context, id, version string) (domain.ExtensionVersion, error) {
	m, ok := f.manifests[id]
	if !ok || m.Version != version {
		return domain.ExtensionVersion{}, domain.ErrNotFound
	}
	b, _ := json.Marshal(m)
	return domain.ExtensionVersion{ExtensionID: id, Version: version, ManifestJSON: string(b)}, nil
}

const permExtID = "dev.example.tool"

// permTestManifest has a page for people holding the extension's permission
// (with a route), an administrator page behind extensions.manage, a page a
// bot tab opens and a page nothing links to.
func permTestManifest() bundle.Manifest {
	return bundle.Manifest{
		ManifestVersion: 1, ID: permExtID, Name: "Tool", Version: "1.0.0", Publisher: "Example",
		Compat:  bundle.Compat{ABI: bundle.ABI},
		Runtime: bundle.Runtime{MemoryMB: 16, TimeoutMS: 2000},
		Routes: []bundle.Route{
			{ID: "list", Method: "GET", Path: "/list", Auth: "user", Perm: bundle.PermExtension},
			{ID: "own", Method: "GET", Path: "/own", Auth: "user", Perm: domain.ExtensionPermission(permExtID)},
			{ID: "setup", Method: "POST", Path: "/setup", Auth: "user", Perm: domain.PermExtensionsManage},
			{ID: "open", Method: "GET", Path: "/open", Auth: "user"},
		},
		Contributes: bundle.Contributes{
			Nav: []bundle.NavItem{
				{Label: "Tool", Page: "main", Perm: bundle.PermExtension},
				{Label: "Tool setup", Page: "setup", Perm: domain.PermExtensionsManage},
			},
			BotTabs: []bundle.BotTab{{ID: "t", Label: "Tab", Page: "tab"}},
		},
		UI: []bundle.UIPage{{ID: "main", File: "ui/main.html"}, {ID: "setup", File: "ui/setup.html"}, {ID: "tab", File: "ui/tab.html"}, {ID: "free", File: "ui/free.html"}},
	}
}

func newPermTestService(t *testing.T) *ExtensionService {
	t.Helper()
	m := permTestManifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	ext := domain.Extension{ID: permExtID, ActiveVersion: m.Version, State: domain.ExtEnabled}
	files := map[string][]byte{}
	for _, p := range m.UI {
		files[p.File] = []byte("<p>" + p.ID + "</p>")
	}
	s := &ExtensionService{Store: &extPermStore{exts: []domain.Extension{ext}, manifests: map[string]bundle.Manifest{permExtID: m}}}
	// The page files come from the loaded copy, as for a running extension.
	s.loaded = map[string]*loadedExt{permExtID: {ext: ext, man: m, files: files}}
	return s
}

func permUsers() (admin, member, plain, delegated, other domain.User) {
	role := func(perms ...string) domain.User {
		return domain.User{ID: "u", Role: domain.RoleUser, RoleID: "r", PermissionsLoaded: true, CustomPermissions: perms}
	}
	return domain.User{ID: "a", Role: domain.RoleAdmin},
		role(domain.PermBotsConsole, domain.ExtensionPermission(permExtID)),
		role(domain.PermBotsConsole),
		role(domain.PermExtensionsManage),
		role("ext:dev.example.other")
}

func TestExtensionPermissionContributions(t *testing.T) {
	s := newPermTestService(t)
	admin, member, plain, delegated, other := permUsers()
	nav := func(u domain.User) []string {
		cs, err := s.Contributions(context.Background(), u)
		if err != nil || len(cs) != 1 {
			t.Fatalf("contributions: %v %v", cs, err)
		}
		var out []string
		for _, n := range cs[0].Nav {
			out = append(out, n.Page+"="+n.Perm)
		}
		return out
	}
	want := map[string][]string{
		"admin":     {"main=ext:" + permExtID, "setup=extensions.manage"},
		"member":    {"main=ext:" + permExtID},
		"plain":     nil,
		"delegated": {"setup=extensions.manage"},
		"other":     nil,
	}
	for name, u := range map[string]domain.User{"admin": admin, "member": member, "plain": plain, "delegated": delegated, "other": other} {
		if got := nav(u); !slices.Equal(got, want[name]) {
			t.Errorf("%s: nav = %v, want %v", name, got, want[name])
		}
	}
}

func TestExtensionPermissionUIPage(t *testing.T) {
	s := newPermTestService(t)
	admin, member, plain, delegated, other := permUsers()
	cases := []struct {
		name string
		u    domain.User
		page string
		ok   bool
	}{
		{"admin main", admin, "main", true},
		{"admin setup", admin, "setup", true},
		{"member main", member, "main", true},
		{"member setup", member, "setup", false},
		{"plain main", plain, "main", false},
		{"other extension's permission", other, "main", false},
		{"delegated main", delegated, "main", false},
		{"delegated setup", delegated, "setup", true},
		{"bot tab page", plain, "tab", true},
		{"unlinked page", plain, "free", true},
	}
	for _, c := range cases {
		html, _, err := s.UIPage(context.Background(), c.u, permExtID, c.page)
		if c.ok && (err != nil || string(html) != "<p>"+c.page+"</p>") {
			t.Errorf("%s: %q %v", c.name, html, err)
		}
		if !c.ok && !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: served (%v)", c.name, err)
		}
	}
	if _, _, err := s.UIPage(context.Background(), admin, permExtID, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown page: %v", err)
	}
}

func TestExtensionPermissionRoutes(t *testing.T) {
	s := newPermTestService(t)
	// Safe mode stops right after authorization: an allowed request fails
	// with "not loaded" instead of running code, a refused one is forbidden.
	s.SafeMode = true
	admin, member, plain, delegated, other := permUsers()
	call := func(u domain.User, method, path string) error {
		_, err := s.HandleRoute(context.Background(), &u, permExtID, ExtensionRouteRequest{Auth: "user", Method: method, Path: path})
		return err
	}
	cases := []struct {
		name         string
		u            domain.User
		method, path string
		ok           bool
	}{
		{"admin list", admin, "GET", "/list", true},
		{"member list", member, "GET", "/list", true},
		{"member own ext id", member, "GET", "/own", true},
		{"member setup", member, "POST", "/setup", false},
		{"plain list", plain, "GET", "/list", false},
		{"plain own ext id", plain, "GET", "/own", false},
		{"other extension's permission", other, "GET", "/list", false},
		{"delegated list", delegated, "GET", "/list", false},
		{"delegated setup", delegated, "POST", "/setup", true},
		{"open route", plain, "GET", "/open", true},
	}
	for _, c := range cases {
		err := call(c.u, c.method, c.path)
		if forbidden := errors.Is(err, domain.ErrForbidden); forbidden == c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	var pe *domain.PermissionError
	if err := call(plain, "GET", "/list"); !errors.As(err, &pe) || pe.Perm != "ext:"+permExtID {
		t.Fatalf("refusal should name the extension permission: %v", err)
	}
	// An API client must carry the extension permission too.
	client := member
	client.Client = &domain.ClientScope{Permissions: []string{domain.PermBotsConsole}}
	if err := call(client, "GET", "/list"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("API client without the permission: %v", err)
	}
}

func TestExtensionPermissionCatalog(t *testing.T) {
	s := newPermTestService(t)
	got, err := s.PermissionCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PermissionInfo{{Name: "ext:" + permExtID, Group: domain.PermGroupExtensions, Label: "Use Tool", Description: "See and use the pages of the Tool extension."}}
	if !slices.Equal(got, want) {
		t.Fatalf("catalog = %+v", got)
	}
}

// Role validation accepts well-formed extension permissions (installed or
// not) and refuses malformed ones; a delegated role manager can only grant
// the extension permissions they hold.
func TestRoleExtensionPermissions(t *testing.T) {
	if _, err := validateRole(RoleInput{Name: "Support", Permissions: []string{domain.PermBotsConsole, "ext:" + permExtID, "ext:dev.example.not-installed"}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"ext:Tool", "ext:", "ext:dev.example.tool/x"} {
		if _, err := validateRole(RoleInput{Name: "Support", Permissions: []string{bad}}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	admin, member, _, _, _ := permUsers()
	manager := member
	manager.CustomPermissions = append(slices.Clone(member.CustomPermissions), domain.PermRolesManage)
	if err := grantable(admin, []string{"ext:" + permExtID, "ext:dev.example.other"}); err != nil {
		t.Fatalf("administrator: %v", err)
	}
	if err := grantable(manager, []string{"ext:" + permExtID}); err != nil {
		t.Fatalf("held: %v", err)
	}
	if err := grantable(manager, []string{"ext:dev.example.other"}); err == nil {
		t.Fatal("a role manager granted an extension permission they lack")
	}
}
