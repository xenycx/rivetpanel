package noderoute_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/templates"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

// TestRemoteTemplateSeed creates template bots through the real BotService
// on a node served by a real hub and agent: every template file arrives in
// the node's workspace through one committed patch, and the panel's own
// workspace directory is never created.
func TestRemoteTemplateSeed(t *testing.T) {
	r, nodeID, files, db := remoteNodeDB(t)
	ctx := t.Context()
	if err := db.EnsureLocalNode(ctx); err != nil {
		t.Fatal(err)
	}
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	panelDir := t.TempDir()
	panel, err := filesystem.NewManager(panelDir)
	if err != nil {
		t.Fatal(err)
	}
	defer panel.Close()
	as := &service.AuthService{Store: db, Hasher: auth.NewHasher(2), TTL: 3600e9}
	admin, err := as.CreateUser(ctx, "admin@x.io", "correct-horse-battery", domain.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	user, err := as.CreateUser(ctx, "user@x.io", "correct-horse-battery", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	bots := &service.BotService{Store: db, Catalog: cat, Workspaces: panel, Files: panel, LocalNode: domain.LocalNodeID,
		RemoteNode: r.Remote, NodeFiles: r, Purger: r,
		Limits: service.Limits{MinMemoryBytes: 32 << 20, MaxMemoryBytes: 1 << 30, MinNanoCPUs: 50_000_000, MaxNanoCPUs: 2e9}}

	tid := "discordts" // has a nested file (src/index.mts)
	if _, err := bots.Create(ctx, user, service.CreateBotInput{Name: "nope", TemplateID: &tid, NodeID: nodeID}); err == nil {
		t.Fatal("a non-administrator chose the node")
	}
	b, err := bots.Create(ctx, admin, service.CreateBotInput{Name: "remote-ts", TemplateID: &tid, NodeID: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	if b.NodeID != nodeID || b.SourceType != "template" {
		t.Fatalf("bot = %+v", b)
	}
	want, err := templates.Files(tid)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range want {
		if got, ok := nodeFile(t, files, b.ID, f.Path); !ok || got != string(f.Data) {
			t.Fatalf("node %s = %q (exists %v)", f.Path, got, ok)
		}
	}
	if _, err := os.Stat(filepath.Join(panelDir, b.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the panel created a local workspace for a remote bot: %v", err)
	}
	// The node's transaction is closed: a later patch is not refused as busy.
	tx, _, err := r.BeginPatch(ctx, nodeID, b.ID, []filesystem.PatchFile{{Path: "extra.txt", After: []byte("x")}}, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("seed transaction left open: %v", err)
	}
	if err := r.CompleteTransaction(ctx, nodeID, b.ID, tx, false); err != nil {
		t.Fatal(err)
	}
	// Workspace creation is idempotent and the node refuses invalid ids.
	if err := r.CreateWorkspace(ctx, nodeID, b.ID); err != nil {
		t.Fatalf("repeat create: %v", err)
	}
	if err := r.CreateWorkspace(ctx, nodeID, ".."); err == nil {
		t.Fatal("an invalid workspace id was accepted")
	}
	// The node answers 400 (a readable refusal), not an internal error.
	var invalid *domain.ValidationError
	if err := r.CreateWorkspace(ctx, nodeID, "not-a-uuid"); !errors.As(err, &invalid) {
		t.Fatalf("invalid id: %v", err)
	}
	if err := r.CreateWorkspace(ctx, domain.LocalNodeID, "x"); err == nil {
		t.Fatal("the local node was sent to an agent")
	}
}

// TestRemoteStreamedWrite streams a file of a declared size to the node (the
// Modrinth install path): the node reports the size it wrote, and a body
// shorter than declared or larger than the node accepts leaves nothing.
func TestRemoteStreamedWrite(t *testing.T) {
	r, nodeID, files := remoteNode(t)
	ctx := t.Context()
	bot := "0b5e6c1a-8f0e-4a8a-9d55-2f1f3c1e0a01"
	if err := r.CreateWorkspace(ctx, nodeID, bot); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("jar"), 100_000) // 300 KB
	rev, err := r.WriteFileFrom(ctx, nodeID, bot, "plugins/a.jar", bytes.NewReader(data), int64(len(data)), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := filesystem.RevisionSize(rev); !ok || n != int64(len(data)) {
		t.Fatalf("revision %q does not report %d bytes", rev, len(data))
	}
	if got, ok := nodeFile(t, files, bot, "plugins/a.jar"); !ok || got != string(data) {
		t.Fatal("streamed file differs on the node")
	}
	// The declared length is more than the source delivers: refused.
	if _, err := r.WriteFileFrom(ctx, nodeID, bot, "plugins/short.jar", bytes.NewReader(data[:10]), int64(len(data)), "", false); err == nil {
		t.Fatal("a truncated body was accepted")
	}
	if _, ok := nodeFile(t, files, bot, "plugins/short.jar"); ok {
		t.Fatal("a truncated body left a file")
	}
	// Over the node's upload limit (4 MiB in this harness): 413, nothing written.
	big := make([]byte, 5<<20)
	if _, err := r.WriteFileFrom(ctx, nodeID, bot, "plugins/big.jar", bytes.NewReader(big), int64(len(big)), "", false); !errors.Is(err, filesystem.ErrTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
	if _, ok := nodeFile(t, files, bot, "plugins/big.jar"); ok {
		t.Fatal("an oversized body left a file")
	}
	if _, err := r.WriteFileFrom(ctx, nodeID, bot, "../escape.jar", bytes.NewReader(data[:3]), 3, "", false); err == nil {
		t.Fatal("escaping path accepted")
	}
}
