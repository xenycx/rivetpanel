package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/service"
)

// moveRoutes stands in for noderoute.Router's node-addressed calls: add-on
// data on the remote node, and purging one node's copy (the real wire is
// covered by internal/noderoute's hub+agent tests).
type moveRoutes struct {
	mu          sync.Mutex
	local       addons.OwnedData
	remote      addons.OwnedData
	localFiles  *filesystem.Manager
	remoteFiles *filesystem.Manager
	purged      []string
}

func (m *moveRoutes) ArchiveAddonData(_ context.Context, nodeID, botID string, dst io.Writer) error {
	_, err := m.remote.Archive(botID, dst)
	return err
}

func (m *moveRoutes) ReplaceAddonData(_ context.Context, nodeID, botID string, src io.Reader) error {
	_, err := m.remote.Replace(botID, src)
	return err
}

func (m *moveRoutes) PurgeOn(_ context.Context, nodeID, botID string) error {
	m.mu.Lock()
	m.purged = append(m.purged, nodeID)
	m.mu.Unlock()
	if nodeID == remoteDeployNode {
		_ = m.remoteFiles.Remove(botID)
		return m.remote.RemoveBot(botID)
	}
	_ = m.localFiles.Remove(botID)
	return m.local.RemoveBot(botID)
}

func (m *moveRoutes) purges() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.purged...)
}

func TestMoveBotBetweenNodes(t *testing.T) {
	e, bs, _ := backupEnv(t)
	e.bots.Coord = &service.Coordinator{}
	uid, gid := os.Getuid(), os.Getgid()
	local := addons.OwnedData{DataRoot: addons.DataRoot{Dir: filepath.Join(t.TempDir(), "addons")}, UID: uid, GID: gid}
	remote := addons.OwnedData{DataRoot: addons.DataRoot{Dir: filepath.Join(t.TempDir(), "addons")}, UID: uid, GID: gid}
	e.bots.AddonData = local.DataRoot
	e.bots.RemoteAddons = &fakeNodeAddons{online: true}
	ops := &service.Operations{Store: e.db, Bots: e.bots}
	bs.Ops = ops

	admin := e.user("mover@x.io", domain.RoleAdmin)
	user := e.user("plain@x.io", domain.RoleUser)
	var b botDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "Mover", "runtime": "nodejs",
		"addons": []map[string]any{{"kind": "postgres"}}}), &b)
	base := "/api/v1/bots/" + b.ID
	w, err := bs.Files.Open(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	w.Write("index.js", strings.NewReader("console.log('moved')"), 1<<20)
	w.Close()
	pg, _ := local.Path(b.ID, "postgres")
	os.MkdirAll(pg, 0o700)
	os.WriteFile(filepath.Join(pg, "PG_VERSION"), []byte("16"), 0o600)

	// A second, remote node joins.
	nf := newFakeNodeFiles(t)
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'old-vps', 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode }
	e.bots.NodeFiles = nf
	bs.Remote = &remoteArchives{files: nf.files, tx: map[string]remoteArchiveTx{}}
	routes := &moveRoutes{local: local, remote: remote, localFiles: bs.Files, remoteFiles: nf.files}
	moves := &service.NodeMoveService{Backups: bs, Nodes: routes, LocalAddons: local}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Files: bs.Files,
		Backups: bs, Moves: moves, Ops: ops, SecureCookies: true})

	// Refusals: no node permission, the same node, an unknown node.
	user.mustStatus(404, "POST", base+"/node", map[string]any{"node_id": remoteDeployNode})
	admin.mustStatus(400, "POST", base+"/node", map[string]any{"node_id": domain.LocalNodeID})
	admin.mustStatus(400, "POST", base+"/node", map[string]any{"node_id": "33333333-3333-4333-8333-333333333333"})

	move := func(to string) {
		t.Helper()
		var r struct {
			OperationID string `json:"operation_id"`
		}
		json.Unmarshal(admin.mustStatus(202, "POST", base+"/node", map[string]any{"node_id": to}), &r)
		for i := 0; i < 300; i++ {
			op, err := e.db.GetOperation(context.Background(), r.OperationID)
			if err == nil && op.Status != domain.OpRunning && op.Status != domain.OpQueued {
				if op.Status != domain.OpSucceeded {
					t.Fatalf("move %s: %v %v", op.Status, deref(op.ResultCode), deref(op.Message))
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("move did not finish")
	}

	move(remoteDeployNode)
	var got botDTO
	json.Unmarshal(admin.mustStatus(200, "GET", base, nil), &got)
	if got.NodeID != remoteDeployNode || got.Generation <= b.Generation {
		t.Fatalf("bot not on the remote node: %+v", got)
	}
	if s, ok := nf.read(t, b.ID, "index.js"); !ok || s != "console.log('moved')" {
		t.Fatalf("files not on the remote node: %q", s)
	}
	rp, _ := remote.Path(b.ID, "postgres")
	if v, err := os.ReadFile(filepath.Join(rp, "PG_VERSION")); err != nil || string(v) != "16" {
		t.Fatalf("database data not on the remote node: %v", err)
	}
	if _, err := os.Stat(pg); err == nil {
		t.Fatal("database data left on the old node")
	}
	if p := routes.purges(); len(p) != 1 || p[0] != domain.LocalNodeID {
		t.Fatalf("purges %v", p)
	}

	// And back: the remote copy goes, the files come home.
	move(domain.LocalNodeID)
	json.Unmarshal(admin.mustStatus(200, "GET", base, nil), &got)
	if got.NodeID != domain.LocalNodeID {
		t.Fatalf("bot not back on the local node: %+v", got)
	}
	if panelFile(t, bs.Files, b.ID, "index.js") != "console.log('moved')" {
		t.Fatal("files not back on the local node")
	}
	if v, err := os.ReadFile(filepath.Join(pg, "PG_VERSION")); err != nil || string(v) != "16" {
		t.Fatalf("database data not back: %v", err)
	}
	if p := routes.purges(); len(p) != 2 || p[1] != remoteDeployNode {
		t.Fatalf("purges %v", p)
	}
}
