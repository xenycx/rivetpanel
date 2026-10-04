package filesystem

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

func putFile(t *testing.T, w *Workspace, name, body string) {
	t.Helper()
	if err := w.Write(name, strings.NewReader(body), 1<<20); err != nil {
		t.Fatal(err)
	}
}

func TestWriteZipRoundTrip(t *testing.T) {
	w, _ := setup(t)
	putFile(t, w, "world/level.dat", "LEVEL")
	putFile(t, w, "world/region/r.0.0.mca", strings.Repeat("x", 5000))
	putFile(t, w, "world/.tmp-abc", "in progress")
	if err := w.root.Symlink("/etc/passwd", "world/evil"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	n, err := w.WriteZip(&buf, []string{"world"}, DefaultZipLimits)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["world/level.dat"] || !names["world/region/r.0.0.mca"] || names["world/evil"] || names["world/.tmp-abc"] || n != len(zr.File) {
		t.Fatalf("entries %v (%d)", names, n)
	}
	// Extract it elsewhere through the in-workspace path.
	putFile(t, w, "backup.zip", buf.String())
	got, err := w.ExtractArchive("backup.zip", "restored", LargeExtractLimits)
	if err != nil || got != 2 {
		t.Fatalf("extract %d %v", got, err)
	}
	b, _ := w.Read("restored/world/level.dat", 100)
	if string(b) != "LEVEL" {
		t.Fatalf("restored %q", b)
	}
	if _, err := w.WriteZip(io.Discard, []string{"world"}, ZipLimits{MaxEntries: 2, MaxBytes: 1 << 20}); err == nil {
		t.Fatal("entry limit not enforced")
	}
}

func tarGz(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := strings.Repeat("y", int(h.Size))
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractTarGzSafety(t *testing.T) {
	w, _ := setup(t)
	ok := tarGz(t, tar.Header{Name: "mods/", Typeflag: tar.TypeDir, Mode: 0o755},
		tar.Header{Name: "mods/a.jar", Typeflag: tar.TypeReg, Mode: 0o644, Size: 10})
	putFile(t, w, "pack.tar.gz", string(ok))
	if n, err := w.ExtractArchive("pack.tar.gz", ".", LargeExtractLimits); err != nil || n != 1 {
		t.Fatalf("extract %d %v", n, err)
	}
	for name, archive := range map[string][]byte{
		"traversal": tarGz(t, tar.Header{Name: "../../escape", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}),
		"absolute":  tarGz(t, tar.Header{Name: "/etc/cron.d/x", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}),
		"symlink":   tarGz(t, tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}),
		"too big":   tarGz(t, tar.Header{Name: "big", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2000}),
	} {
		putFile(t, w, "bad.tar.gz", string(archive))
		lim := LargeExtractLimits
		if name == "too big" {
			lim = ExtractLimits{MaxEntries: 10, MaxTotalSize: 1000, MaxFileSize: 1000}
		}
		if _, err := w.ExtractArchive("bad.tar.gz", ".", lim); err == nil {
			t.Errorf("%s accepted", name)
		}
		if ok, _ := w.Exists("escape"); ok {
			t.Fatal("wrote outside the target")
		}
	}
	if ok, _ := w.Exists("link"); ok {
		t.Fatal("symlink created")
	}
}

func TestZipBombRatio(t *testing.T) {
	w, _ := setup(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("bomb.bin")
	f.Write(make([]byte, 8<<20)) // zeros compress far beyond 200x
	zw.Close()
	putFile(t, w, "bomb.zip", buf.String())
	if _, err := w.ExtractArchive("bomb.zip", ".", LargeExtractLimits); err == nil || !strings.Contains(err.Error(), "suspiciously") {
		t.Fatalf("zip bomb accepted: %v", err)
	}
	if _, err := w.ExtractArchive("notes.txt", ".", LargeExtractLimits); err == nil {
		t.Fatal("non-archive accepted")
	}
}
