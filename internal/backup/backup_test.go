package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

const botA = "11111111-2222-4333-8444-555555555555"
const botB = "aaaaaaaa-2222-4333-8444-555555555555"

type world struct {
	dir, dbPath, dataRoot, keyDir string
	db                            *sqlite.DB
	keys                          *secrets.Keyring
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{dir: dir, dbPath: filepath.Join(dir, "live", "rivetpanel.db"), dataRoot: filepath.Join(dir, "live", "bots"), keyDir: filepath.Join(dir, "live", "keys")}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, w.dbPath, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w.db = db
	db.Migrate(ctx, migrations.FS)
	db.EnsureLocalNode(ctx)
	keys, err := secrets.LoadDir(w.keyDir, "k1", true)
	if err != nil {
		t.Fatal(err)
	}
	w.keys = keys
	db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u','u@x.io','h',1,1)`)
	for _, id := range []string{botA, botB} {
		db.CreateBot(ctx, domain.Bot{ID: id, OwnerID: "u", NodeID: domain.LocalNodeID, Name: "n" + id[:2], Runtime: "nodejs", ImageRef: "i",
			Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1})
		sealed, _ := keys.Seal(id, "TOKEN", []byte("secret-"+id[:2]))
		db.UpsertEnv(ctx, id, []domain.EnvVar{{BotID: id, Name: "TOKEN", Ciphertext: sealed.Ciphertext, Nonce: sealed.Nonce, KeyID: sealed.KeyID}}, 5)
		os.MkdirAll(filepath.Join(w.dataRoot, id, "src"), 0o750)
		os.WriteFile(filepath.Join(w.dataRoot, id, "src", "index.js"), []byte("code-"+id[:2]), 0o640)
		os.WriteFile(filepath.Join(w.dataRoot, id, "run.sh"), []byte("#!/bin/sh"), 0o750)
	}
	os.MkdirAll(filepath.Join(w.dataRoot, botA, "node_modules", ".bin"), 0o750)
	os.Symlink("../pkg/cli.js", filepath.Join(w.dataRoot, botA, "node_modules", ".bin", "tool"))
	os.Mkdir(filepath.Join(w.dataRoot, "not-a-bot"), 0o750) // ignored
	os.WriteFile(filepath.Join(w.dataRoot, "not-a-bot", "x"), []byte("x"), 0o600)
	return w
}

func (w *world) src() Source { return Source{DB: w.db, DataRoot: w.dataRoot, KeyDir: w.keyDir} }

func TestBackupRestoreRoundTrip(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	bdir := filepath.Join(w.dir, "backup")
	m, err := Create(ctx, w.src(), bdir, false, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if m.Bots != 2 || m.IncludesKeys || len(m.KeyIDs) != 1 || m.SHA256[dbName] == "" {
		t.Fatalf("%+v", m)
	}
	if _, err := os.Stat(filepath.Join(bdir, keysDirName)); err == nil {
		t.Fatal("keys were included without being asked")
	}
	if fi, _ := os.Stat(filepath.Join(bdir, dbName)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup db mode %v", fi.Mode())
	}

	// Restore into a completely fresh location, restoring keys from a separate copy.
	fresh := filepath.Join(w.dir, "fresh")
	o := RestoreOptions{DBPath: filepath.Join(fresh, "rivetpanel.db"), DataRoot: filepath.Join(fresh, "bots"), KeyDir: filepath.Join(fresh, "keys")}
	if _, err := Restore(ctx, bdir, o); err != nil {
		t.Fatal(err)
	}
	// files, modes and relative symlinks survived; the unrelated dir did not
	if b, _ := os.ReadFile(filepath.Join(o.DataRoot, botA, "src", "index.js")); string(b) != "code-11" {
		t.Fatal("file content")
	}
	if fi, _ := os.Stat(filepath.Join(o.DataRoot, botA, "run.sh")); fi.Mode().Perm() != 0o750 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if l, err := os.Readlink(filepath.Join(o.DataRoot, botA, "node_modules", ".bin", "tool")); err != nil || l != "../pkg/cli.js" {
		t.Fatalf("symlink %q %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(o.DataRoot, "not-a-bot")); err == nil {
		t.Fatal("non-bot directory restored")
	}
	// database restored and readable
	db2, err := sqlite.Open(ctx, o.DBPath, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	db2.Migrate(ctx, migrations.FS)
	bots, _ := db2.ListBots(ctx, "")
	if len(bots) != 2 {
		t.Fatalf("bots = %d", len(bots))
	}
	// Without keys, the secrets are unrecoverable: verification says so precisely.
	os.MkdirAll(o.KeyDir, 0o700)
	other, _ := secrets.NewKeyring("zz", map[string][]byte{"zz": bytes.Repeat([]byte{9}, 32)})
	rep, _ := VerifyEnv(ctx, db2, other)
	if rep.Healthy() || len(rep.MissingKeys) != 1 || rep.MissingKeys[0] != "k1" || rep.Total != 2 {
		t.Fatalf("%+v", rep)
	}
	// With the original key material, every value decrypts.
	rep, _ = VerifyEnv(ctx, db2, w.keys)
	if !rep.Healthy() || rep.OK != 2 {
		t.Fatalf("%+v", rep)
	}
}

func TestBackupIncludesUncheckpointedWALData(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	// Recent writes live only in the -wal file; a raw copy of the .db would miss them.
	w.db.ExecContext(ctx, `UPDATE bots SET name = 'only-in-wal' WHERE id = ?`, botA)
	if fi, err := os.Stat(w.dbPath + "-wal"); err != nil || fi.Size() == 0 {
		t.Skip("no WAL content to test against")
	}
	bdir := filepath.Join(w.dir, "b")
	if _, err := Create(ctx, w.src(), bdir, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	snap, _ := sqlite.Open(ctx, filepath.Join(bdir, dbName), 1)
	defer snap.Close()
	b, err := snap.GetBot(ctx, botA)
	if err != nil || b.Name != "only-in-wal" {
		t.Fatalf("snapshot missed WAL data: %v %q", err, b.Name)
	}
}

func TestKeysAreSeparateAndOptional(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	bdir := filepath.Join(w.dir, "b")
	m, err := Create(ctx, w.src(), bdir, true, time.Now())
	if err != nil || !m.IncludesKeys {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(bdir, keysDirName, "k1.key"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	fresh := filepath.Join(w.dir, "fresh")
	o := RestoreOptions{DBPath: filepath.Join(fresh, "db"), DataRoot: filepath.Join(fresh, "bots"), KeyDir: filepath.Join(fresh, "keys"), RestoreKeys: true}
	if _, err := Restore(ctx, bdir, o); err != nil {
		t.Fatal(err)
	}
	k, err := secrets.LoadDir(o.KeyDir, "k1", false)
	if err != nil {
		t.Fatal(err)
	}
	db2, _ := sqlite.Open(ctx, o.DBPath, 1)
	defer db2.Close()
	if rep, _ := VerifyEnv(ctx, db2, k); !rep.Healthy() {
		t.Fatalf("%+v", rep)
	}
	// asking to restore keys from a backup that has none is an error
	b2 := filepath.Join(w.dir, "b2")
	Create(ctx, w.src(), b2, false, time.Now())
	if _, err := Restore(ctx, b2, RestoreOptions{DBPath: filepath.Join(w.dir, "x", "db"), DataRoot: filepath.Join(w.dir, "x", "bots"), KeyDir: filepath.Join(w.dir, "x", "k"), RestoreKeys: true}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRestoreRefusesToOverwriteAndDetectsCorruption(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	bdir := filepath.Join(w.dir, "b")
	Create(ctx, w.src(), bdir, false, time.Now())
	if _, err := Create(ctx, w.src(), bdir, false, time.Now()); err == nil {
		t.Fatal("backup into a non-empty directory must fail")
	}
	// live location: existing DB and bots must not be clobbered without --force
	live := RestoreOptions{DBPath: w.dbPath, DataRoot: w.dataRoot, KeyDir: w.keyDir}
	if _, err := Restore(ctx, bdir, live); err == nil {
		t.Fatal("overwrote a live database")
	}
	fresh := filepath.Join(w.dir, "f")
	os.MkdirAll(filepath.Join(fresh, "bots", botA), 0o750)
	if _, err := Restore(ctx, bdir, RestoreOptions{DBPath: filepath.Join(fresh, "db"), DataRoot: filepath.Join(fresh, "bots"), KeyDir: filepath.Join(fresh, "k")}); err == nil {
		t.Fatal("restored over a non-empty data root")
	}
	if _, err := os.Stat(filepath.Join(fresh, "db")); err == nil {
		t.Fatal("database written although restore was refused")
	}
	// tampering is detected before anything is written
	f, _ := os.OpenFile(filepath.Join(bdir, botsName), os.O_WRONLY|os.O_APPEND, 0)
	f.Write([]byte("junk"))
	f.Close()
	fresh2 := filepath.Join(w.dir, "f2")
	if _, err := Restore(ctx, bdir, RestoreOptions{DBPath: filepath.Join(fresh2, "db"), DataRoot: filepath.Join(fresh2, "bots"), KeyDir: filepath.Join(fresh2, "k")}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corruption not detected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fresh2, "db")); err == nil {
		t.Fatal("restore wrote data from a corrupt backup")
	}
}

func evilArchive(t *testing.T, dir string, entries ...tar.Header) {
	t.Helper()
	os.MkdirAll(dir, 0o700)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		if h.Typeflag == tar.TypeReg {
			h.Size = 1
		}
		tw.WriteHeader(&h)
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte("x"))
		}
	}
	tw.Close()
	gz.Close()
	os.WriteFile(filepath.Join(dir, botsName), buf.Bytes(), 0o600)
}

func TestExtractionRejectsMaliciousArchives(t *testing.T) {
	cases := map[string][]tar.Header{
		"traversal":     {{Name: botA + "/../../evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"traversal2":    {{Name: botA + "/a/../../../evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"absolute":      {{Name: "/etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644}},
		"not a bot id":  {{Name: "evil/file", Typeflag: tar.TypeReg, Mode: 0o644}},
		"outside a bot": {{Name: "file", Typeflag: tar.TypeReg, Mode: 0o644}},
		"device":        {{Name: botA + "/dev", Typeflag: tar.TypeChar, Mode: 0o644}},
		"hardlink":      {{Name: botA + "/h", Typeflag: tar.TypeLink, Linkname: "/etc/passwd", Mode: 0o644}},
		"backslash":     {{Name: botA + "/a\\..\\b", Typeflag: tar.TypeReg, Mode: 0o644}},
	}
	for name, hs := range cases {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			evilArchive(t, d, hs...)
			root := filepath.Join(d, "root", "bots")
			err := extractBots(context.Background(), filepath.Join(d, botsName), root, false)
			if err == nil {
				t.Fatal("malicious archive accepted")
			}
			for _, p := range []string{filepath.Join(d, "evil"), filepath.Join(d, "root", "evil"), filepath.Join(root, "evil")} {
				if _, e := os.Stat(p); e == nil {
					t.Fatalf("file escaped to %s", p)
				}
			}
		})
	}
}

func TestExtractionCannotWriteThroughSymlinkToOutside(t *testing.T) {
	d := t.TempDir()
	outside := filepath.Join(d, "outside")
	os.Mkdir(outside, 0o750)
	evilArchive(t, d,
		tar.Header{Name: botA + "/", Typeflag: tar.TypeDir, Mode: 0o750},
		tar.Header{Name: botA + "/link", Typeflag: tar.TypeSymlink, Linkname: outside, Mode: 0o777},
		tar.Header{Name: botA + "/link/pwn.txt", Typeflag: tar.TypeReg, Mode: 0o644},
	)
	err := extractBots(context.Background(), filepath.Join(d, botsName), filepath.Join(d, "bots"), false)
	if err == nil {
		t.Fatal("wrote through a symlink")
	}
	if _, e := os.Stat(filepath.Join(outside, "pwn.txt")); e == nil {
		t.Fatal("file written outside the bot directory")
	}
}

func TestSetuidBitsAreDropped(t *testing.T) {
	d := t.TempDir()
	evilArchive(t, d, tar.Header{Name: botA + "/su", Typeflag: tar.TypeReg, Mode: 0o4755})
	root := filepath.Join(d, "bots")
	if err := extractBots(context.Background(), filepath.Join(d, botsName), root, false); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(root, botA, "su"))
	if fi.Mode()&os.ModeSetuid != 0 {
		t.Fatal("setuid preserved")
	}
}

var _ = io.EOF

func TestAgentCAIsBackedUpAndRestoredWithKeys(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	caDir := filepath.Join(w.keyDir, agentCADir)
	ca, err := agentcert.LoadOrCreate(caDir)
	if err != nil {
		t.Fatal(err)
	}
	// Without --include-keys the CA stays out of the backup.
	plain := filepath.Join(w.dir, "plain")
	if m, err := Create(ctx, w.src(), plain, false, time.Now()); err != nil || m.IncludesAgentCA {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(plain, keysDirName, agentCADir)); err == nil {
		t.Fatal("agent CA copied without --include-keys")
	}

	bdir := filepath.Join(w.dir, "b")
	m, err := Create(ctx, w.src(), bdir, true, time.Now())
	if err != nil || !m.IncludesAgentCA {
		t.Fatalf("%+v %v", m, err)
	}
	if fi, err := os.Stat(filepath.Join(bdir, keysDirName, agentCADir, "ca.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup CA key: %v %v", fi, err)
	}
	fresh := filepath.Join(w.dir, "fresh")
	o := RestoreOptions{DBPath: filepath.Join(fresh, "db"), DataRoot: filepath.Join(fresh, "bots"), KeyDir: filepath.Join(fresh, "keys"), RestoreKeys: true}
	if _, err := Restore(ctx, bdir, o); err != nil {
		t.Fatal(err)
	}
	restored, err := agentcert.LoadOrCreate(filepath.Join(o.KeyDir, agentCADir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.CertificatePEM(), ca.CertificatePEM()) {
		t.Fatal("restored agent CA has a different identity")
	}

	// A second restore must not silently replace the CA.
	o2 := RestoreOptions{DBPath: filepath.Join(w.dir, "x", "db"), DataRoot: filepath.Join(w.dir, "x", "bots"), KeyDir: o.KeyDir, RestoreKeys: true}
	if _, err := Restore(ctx, bdir, o2); err == nil || !strings.Contains(err.Error(), "agent CA") {
		t.Fatalf("existing agent CA overwritten: %v", err)
	}
	if _, err := os.Stat(o2.DBPath); err == nil {
		t.Fatal("database written although restore was refused")
	}

	// Tampering with the CA is detected.
	os.WriteFile(filepath.Join(bdir, keysDirName, agentCADir, "ca.crt"), []byte("forged"), 0o644)
	if _, err := Verify(bdir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered CA not detected: %v", err)
	}
}

func TestPartialAgentCAIsRefused(t *testing.T) {
	w := newWorld(t)
	os.MkdirAll(filepath.Join(w.keyDir, agentCADir), 0o700)
	os.WriteFile(filepath.Join(w.keyDir, agentCADir, "ca.crt"), []byte("x"), 0o644)
	if _, err := Create(context.Background(), w.src(), filepath.Join(w.dir, "b"), true, time.Now()); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("partial agent CA backed up: %v", err)
	}
}
