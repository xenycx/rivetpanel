package filesystem

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const botID = "11111111-2222-4333-8444-555555555555"

func newWS(t *testing.T) (*Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if err := m.Create(botID); err != nil {
		t.Fatal(err)
	}
	w, err := m.Open(botID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, filepath.Join(dir, botID)
}

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func targz(t *testing.T, hdrs []tar.Header, bodies []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := range hdrs {
		h := hdrs[i]
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(bodies[i]))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(bodies[i]))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestBackupRoundTrip(t *testing.T) {
	w, root := newWS(t)
	put(t, root, "index.js", "console.log(1)")
	put(t, root, "src/lib/util.js", "util")
	put(t, root, "node_modules/big/x.js", "rebuildable")
	put(t, root, "src/node_modules/y.js", "rebuildable-nested")
	put(t, root, ".tmp-abc", "junk")
	os.Symlink("src/lib/util.js", filepath.Join(root, "link"))
	os.Symlink("/etc/passwd", filepath.Join(root, "evil-link"))

	var buf bytes.Buffer
	n, size, err := w.WriteTarGz(&buf, map[string][]byte{"env.json": []byte(`{"v":1}`)}, DefaultBackupLimits)
	if err != nil || n == 0 || size != int64(len("console.log(1)")+len("util")) {
		t.Fatalf("n=%d size=%d err=%v", n, size, err)
	}
	// The archive holds sources and metadata but not rebuildable directories.
	var names []string
	tr := tar.NewReader(func() *gzip.Reader { g, _ := gzip.NewReader(bytes.NewReader(buf.Bytes())); return g }())
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	all := strings.Join(names, " ")
	if strings.Contains(all, "node_modules") || strings.Contains(all, ".tmp-") || !strings.Contains(all, ".rivetpanel/env.json") {
		t.Fatalf("archive contents: %s", all)
	}

	// Change the workspace, then restore.
	put(t, root, "index.js", "MODIFIED")
	put(t, root, "new-file.txt", "should be removed")
	os.RemoveAll(filepath.Join(root, "src"))
	meta, err := w.RestoreTarGz(bytes.NewReader(buf.Bytes()), DefaultBackupLimits)
	if err != nil {
		t.Fatal(err)
	}
	if string(meta["env.json"]) != `{"v":1}` {
		t.Fatalf("meta = %v", meta)
	}
	for rel, want := range map[string]string{"index.js": "console.log(1)", "src/lib/util.js": "util"} {
		if b, _ := os.ReadFile(filepath.Join(root, rel)); string(b) != want {
			t.Errorf("%s = %q", rel, b)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "new-file.txt")); err == nil {
		t.Error("file created after the backup survived the restore")
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules/big/x.js")); err != nil {
		t.Error("rebuildable directory must be left alone")
	}
	if b, err := os.ReadFile(filepath.Join(root, "link")); err != nil || string(b) != "util" {
		t.Errorf("relative symlink not restored: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "evil-link")); err == nil {
		// The absolute symlink was backed up as a link but must not come back.
		t.Error("absolute symlink restored")
	}
	if ents, _ := os.ReadDir(root); func() bool {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".restore-") {
				return true
			}
		}
		return false
	}() {
		t.Error("staging directory left behind")
	}
}

func TestBackupMetadataIsBoundedAndContained(t *testing.T) {
	w, _ := newWS(t)
	if _, _, err := w.WriteTarGz(io.Discard, map[string][]byte{"../outside": []byte("x")}, DefaultBackupLimits); err == nil {
		t.Fatal("unsafe metadata name accepted")
	}
	if _, _, err := w.WriteTarGz(io.Discard, map[string][]byte{"large": make([]byte, maxMetaBytes+1)}, DefaultBackupLimits); err == nil {
		t.Fatal("oversized metadata accepted")
	}
}

func TestRestoreRejectsMaliciousArchivesAndLeavesWorkspaceIntact(t *testing.T) {
	reg := func(name string) tar.Header { return tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644} }
	cases := map[string][]byte{
		"traversal":  targz(t, []tar.Header{reg("../escape.txt")}, []string{"x"}),
		"absolute":   targz(t, []tar.Header{reg("/etc/cron.d/x")}, []string{"x"}),
		"nested ..":  targz(t, []tar.Header{reg("a/../../escape")}, []string{"x"}),
		"backslash":  targz(t, []tar.Header{reg(`a\..\b`)}, []string{"x"}),
		"hardlink":   targz(t, []tar.Header{{Name: "h", Typeflag: tar.TypeLink, Linkname: "/etc/passwd"}}, []string{""}),
		"device":     targz(t, []tar.Header{{Name: "dev", Typeflag: tar.TypeChar}}, []string{""}),
		"fifo":       targz(t, []tar.Header{{Name: "f", Typeflag: tar.TypeFifo}}, []string{""}),
		"stage name": targz(t, []tar.Header{reg(".restore-abc/x")}, []string{"x"}),
		"not gzip":   []byte("plain text"),
	}
	for name, data := range cases {
		w, root := newWS(t)
		put(t, root, "keep.txt", "original")
		if _, err := w.RestoreTarGz(bytes.NewReader(data), DefaultBackupLimits); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if b, _ := os.ReadFile(filepath.Join(root, "keep.txt")); string(b) != "original" {
			t.Errorf("%s: a rejected archive modified the workspace", name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); err == nil {
			t.Errorf("%s: file escaped the workspace", name)
		}
	}

	// Symlinks that would escape are skipped, not followed, and setuid is dropped.
	w, root := newWS(t)
	data := targz(t, []tar.Header{
		{Name: "l1", Typeflag: tar.TypeSymlink, Linkname: "../../etc/passwd"},
		{Name: "l2", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "ok", Typeflag: tar.TypeReg, Mode: 0o4755},
	}, []string{"", "", "data"})
	if _, err := w.RestoreTarGz(bytes.NewReader(data), DefaultBackupLimits); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"l1", "l2"} {
		if _, err := os.Lstat(filepath.Join(root, l)); err == nil {
			t.Errorf("unsafe symlink %s restored", l)
		}
	}
	if st, _ := os.Stat(filepath.Join(root, "ok")); st.Mode()&os.ModeSetuid != 0 {
		t.Error("setuid bit survived")
	}

	// Limits.
	w, _ = newWS(t)
	big := targz(t, []tar.Header{{Name: "big", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{strings.Repeat("a", 2000)})
	if _, err := w.RestoreTarGz(bytes.NewReader(big), BackupLimits{MaxEntries: 10, MaxBytes: 1000, MaxFile: 1000}); err == nil {
		t.Error("over-limit archive accepted")
	}
	put(t, filepath.Join(t.TempDir()), "x", "")
	w2, root2 := newWS(t)
	put(t, root2, "f", strings.Repeat("a", 100))
	if _, _, err := w2.WriteTarGz(io.Discard, nil, BackupLimits{MaxEntries: 10, MaxBytes: 50, MaxFile: 1000}); err == nil {
		t.Error("oversize backup accepted")
	}
}

func repoTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var hdrs []tar.Header
	var bodies []string
	hdrs = append(hdrs, tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader}, tar.Header{Name: "o-r-abc123/", Typeflag: tar.TypeDir, Mode: 0o755})
	bodies = append(bodies, "", "")
	for n, c := range files {
		hdrs = append(hdrs, tar.Header{Name: "o-r-abc123/" + n, Typeflag: tar.TypeReg, Mode: 0o644})
		bodies = append(bodies, c)
	}
	return targz(t, hdrs, bodies)
}

func TestDeployTarGz(t *testing.T) {
	w, root := newWS(t)
	read := func(rel string) string { b, _ := os.ReadFile(filepath.Join(root, rel)); return string(b) }

	n, err := w.DeployTarGz(bytes.NewReader(repoTar(t, map[string]string{"index.js": "v1", "src/a.js": "a", "old.js": "old"})), "", DefaultBackupLimits)
	if err != nil || n != 3 || read("index.js") != "v1" || read("src/a.js") != "a" {
		t.Fatalf("first deploy: n=%d err=%v", n, err)
	}
	// Runtime data the user or bot created must survive later deploys.
	put(t, root, "data/db.sqlite", "precious")
	put(t, root, "src/user-added.js", "mine")
	put(t, root, "node_modules/x/i.js", "deps")

	if _, err := w.DeployTarGz(bytes.NewReader(repoTar(t, map[string]string{"index.js": "v2", "src/b.js": "b"})), "", DefaultBackupLimits); err != nil {
		t.Fatal(err)
	}
	if read("index.js") != "v2" || read("src/b.js") != "b" {
		t.Fatal("new files not applied")
	}
	if _, err := os.Stat(filepath.Join(root, "old.js")); err == nil {
		t.Error("file removed from the repository survived")
	}
	if _, err := os.Stat(filepath.Join(root, "src/a.js")); err == nil {
		t.Error("stale tracked file survived")
	}
	for rel, want := range map[string]string{"data/db.sqlite": "precious", "src/user-added.js": "mine", "node_modules/x/i.js": "deps"} {
		if read(rel) != want {
			t.Errorf("untracked file %s was touched", rel)
		}
	}
	for _, e := range func() []os.DirEntry { d, _ := os.ReadDir(root); return d }() {
		if strings.HasPrefix(e.Name(), ".deploy-") {
			t.Error("staging directory left behind")
		}
	}

	// Root directory: only that subtree, stripped.
	w2, root2 := newWS(t)
	_, err = w2.DeployTarGz(bytes.NewReader(repoTar(t, map[string]string{"bot/main.py": "m", "bot/lib/x.py": "x", "README.md": "r"})), "bot", DefaultBackupLimits)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root2, "main.py")); string(b) != "m" {
		t.Fatal("root dir not applied")
	}
	if _, err := os.Stat(filepath.Join(root2, "README.md")); err == nil {
		t.Fatal("files outside the root directory were deployed")
	}
	if _, err := w2.DeployTarGz(bytes.NewReader(repoTar(t, map[string]string{"a": "b"})), "missing", DefaultBackupLimits); err == nil {
		t.Fatal("missing root directory accepted")
	}
	if _, err := w2.DeployTarGz(bytes.NewReader(repoTar(t, map[string]string{"a": "b"})), "../x", DefaultBackupLimits); err != nil {
		// "../x" is normalized to "x" (not found), never an escape.
		if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "invalid") {
			t.Fatal(err)
		}
	}
}

func TestDeployRejectsHostileTarballs(t *testing.T) {
	reg := func(n string) tar.Header { return tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644} }
	cases := map[string][]byte{
		"traversal": targz(t, []tar.Header{reg("o-r-1/../../escape")}, []string{"x"}),
		"absolute":  targz(t, []tar.Header{reg("/etc/x")}, []string{"x"}),
		"backslash": targz(t, []tar.Header{reg(`o-r-1/a\..\b`)}, []string{"x"}),
		"not gzip":  []byte("nope"),
	}
	for name, data := range cases {
		w, root := newWS(t)
		put(t, root, "keep", "k")
		if _, err := w.DeployTarGz(bytes.NewReader(data), "", DefaultBackupLimits); err == nil {
			t.Errorf("%s accepted", name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
			t.Errorf("%s: escaped the workspace", name)
		}
		if b, _ := os.ReadFile(filepath.Join(root, "keep")); string(b) != "k" {
			t.Errorf("%s: workspace modified", name)
		}
	}
	// The manifest, and symlinks that leave the workspace, are not deployable.
	w, root := newWS(t)
	data := targz(t, []tar.Header{
		{Name: "o-r-1/.rivetpanel-deploy", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "o-r-1/l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "o-r-1/ok", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{`["../../etc/passwd"]`, "", "1"})
	if _, err := w.DeployTarGz(bytes.NewReader(data), "", DefaultBackupLimits); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "l")); err == nil {
		t.Error("escaping symlink deployed")
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rivetpanel-deploy")); strings.Contains(string(b), "etc/passwd") {
		t.Error("archive controlled the manifest")
	}
	// A forged old manifest cannot make a later deploy delete outside the workspace.
	os.WriteFile(filepath.Join(root, ".rivetpanel-deploy"), []byte(`["../outside.txt","/etc/hostname","ok"]`), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(root), "outside.txt"), []byte("safe"), 0o644)
	if _, err := w.DeployTarGz(bytes.NewReader(repoTar(t, map[string]string{"z": "1"})), "", DefaultBackupLimits); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(root), "outside.txt")); string(b) != "safe" {
		t.Fatal("a forged manifest deleted a file outside the workspace")
	}
}

// A workspace deployed before the RivetPanel rename has only the old
// manifest: the next deploy still removes the files the repository dropped,
// and the old manifest is replaced by the current one.
func TestDeployReadsAndRemovesLegacyManifest(t *testing.T) {
	w, root := newWS(t)
	put(t, root, "index.js", "v1")
	put(t, root, "old.js", "old")
	put(t, root, "data.db", "keep")
	put(t, root, LegacyDeployManifest, `["index.js","old.js"]`)
	if _, err := w.DeployTarGz(bytes.NewReader(repoTar(t, map[string]string{"index.js": "v2", LegacyDeployManifest: `["data.db"]`})), "", DefaultBackupLimits); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.js")); err == nil {
		t.Error("file listed in the legacy manifest survived")
	}
	if _, err := os.Stat(filepath.Join(root, LegacyDeployManifest)); err == nil {
		t.Error("legacy manifest left behind (or taken from the repository)")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "data.db")); string(b) != "keep" {
		t.Error("untracked file touched")
	}
	if b, err := os.ReadFile(filepath.Join(root, DeployManifest)); err != nil || !strings.Contains(string(b), "index.js") {
		t.Errorf("current manifest = %q, %v", b, err)
	}
}
