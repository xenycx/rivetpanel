package blueprint

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"runtime"
	"testing"
)

func classBytes(major int) []byte {
	b := make([]byte, 32)
	binary.BigEndian.PutUint32(b, 0xCAFEBABE)
	binary.BigEndian.PutUint16(b[6:], uint16(major))
	return b
}

// buildJar writes entries with a data descriptor (zip.Writer.Create), a
// stored entry and a deflated entry with sizes in the local header.
func buildJar(t *testing.T, described map[string][]byte, stored, deflated map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range described {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	for name, data := range stored {
		w, err := zw.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(data),
			CompressedSize64: uint64(len(data)), UncompressedSize64: uint64(len(data))})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	for name, data := range deflated {
		var c bytes.Buffer
		fw, _ := flate.NewWriter(&c, flate.DefaultCompression)
		fw.Write(data)
		fw.Close()
		w, err := zw.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(data),
			CompressedSize64: uint64(c.Len()), UncompressedSize64: uint64(len(data))})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(c.Bytes())
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sniffJava(data []byte, chunk int) int {
	s := NewJarJavaSniffer()
	for len(data) > 0 {
		n := min(chunk, len(data))
		s.Write(data[:n])
		data = data[n:]
	}
	return s.Java()
}

func TestJarJavaSniffer(t *testing.T) {
	// Velocity 4 is compiled for Java 25 (class file 69); a Java 21 runtime
	// fails with UnsupportedClassVersionError.
	jar := buildJar(t,
		map[string][]byte{"META-INF/MANIFEST.MF": []byte("Main-Class: x\n"), "com/velocitypowered/proxy/Velocity.class": classBytes(69)},
		map[string][]byte{"a/Stored.class": classBytes(61), "META-INF/versions/99/x/Y.class": classBytes(143)},
		map[string][]byte{"b/Deflated.class": classBytes(65), "readme.txt": bytes.Repeat([]byte("x"), 5000)})
	for _, chunk := range []int{1, 7, 4096, len(jar)} {
		if got := sniffJava(jar, chunk); got != 25 {
			t.Fatalf("chunk %d: java %d, want 25", chunk, got)
		}
	}
	if got := sniffJava(buildJar(t, nil, map[string][]byte{"A.class": classBytes(65)}, nil), 64); got != 21 {
		t.Fatalf("stored only: %d", got)
	}
	if got := sniffJava(buildJar(t, nil, nil, map[string][]byte{"A.class": classBytes(52)}), 64); got != 8 {
		t.Fatalf("deflated only: %d", got)
	}
	// Not a jar, empty, truncated: unknown, and writes never fail.
	for _, data := range [][]byte{[]byte("#!/bin/sh\necho hi\n"), nil} {
		if got := sniffJava(data, 3); got != 0 {
			t.Fatalf("%q: %d", data, got)
		}
	}
	if got := sniffJava(jar[:len(jar)/2], 5); got > 25 {
		t.Fatalf("truncated: %d", got)
	}
	// The sniffer keeps accepting writes after the scan stops.
	s := NewJarJavaSniffer()
	if n, err := s.Write(bytes.Repeat([]byte{0}, 1<<20)); err != nil || n != 1<<20 {
		t.Fatalf("%d %v", n, err)
	}
	if s.Java() != 0 || s.Java() != 0 {
		t.Fatal("garbage reported a version")
	}
}

// Scanning reuses one inflater: a jar with thousands of entries must not cost
// a fresh 40 KiB decompressor per entry (a real server jar churned through
// about 1 GB of heap that way).
func TestJarJavaSnifferReusesInflater(t *testing.T) {
	described := map[string][]byte{}
	deflated := map[string][]byte{}
	for i := range 1500 {
		described[fmt.Sprintf("d/C%d.class", i)] = classBytes(61)
		deflated[fmt.Sprintf("f/C%d.class", i)] = classBytes(61)
	}
	described["z/New.class"] = classBytes(69)
	jar := buildJar(t, described, nil, deflated)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if got := sniffJava(jar, 32<<10); got != 25 {
		t.Fatalf("java %d, want 25", got)
	}
	runtime.ReadMemStats(&after)
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 8<<20 {
		t.Fatalf("scanning 3000 entries allocated %d bytes", alloc)
	}
}

func TestClassFileJava(t *testing.T) {
	for major, want := range map[int]int{52: 8, 61: 17, 65: 21, 69: 25, 45: 1, 10: 0, 500: 0} {
		if got := ClassFileJava(major); got != want {
			t.Errorf("%d: %d want %d", major, got, want)
		}
	}
}

func TestPaperFillJavaMinimum(t *testing.T) {
	jar := []byte("PK fake")
	p := fakeProviders(t, jar)
	a, err := p.Resolve(context.Background(), Download{Provider: Provider{Name: "papermc", Project: "velocity"}, Version: "latest"}, nil)
	if err != nil || a.Version != "4.2.0" || a.JavaHint != 25 {
		t.Fatalf("velocity %+v %v", a, err)
	}
	var buf bytes.Buffer
	if _, err := p.Fetch(context.Background(), a, io.MultiWriter(&buf, NewJarJavaSniffer())); err != nil {
		t.Fatal(err)
	}
	// No published requirement: unknown rather than an error.
	a, err = p.Resolve(context.Background(), Download{Provider: Provider{Name: "papermc", Project: "paper"}, Version: "latest"}, nil)
	if err != nil || a.JavaHint != 0 {
		t.Fatalf("paper %+v %v", a, err)
	}
}
