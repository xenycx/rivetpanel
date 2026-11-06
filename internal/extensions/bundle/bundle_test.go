package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
)

func testManifest() Manifest {
	return Manifest{
		ManifestVersion: 1, ID: "dev.example.tool", Name: "Tool", Version: "1.0.0", Publisher: "Example",
		Compat:       Compat{Panel: ">=0.9.0 <2.0.0", ABI: 1},
		Runtime:      Runtime{MemoryMB: 16, TimeoutMS: 2000},
		Capabilities: []Capability{{Name: CapKV, Reason: "remember things"}, {Name: CapHTTP, Reason: "call the API", Hosts: []string{"api.example.com"}}},
		Settings:     []Setting{{Key: "token", Label: "Token", Type: "secret", InjectHosts: []string{"api.example.com"}}},
		Routes:       []Route{{ID: "card", Method: "GET", Path: "/card", Auth: "bot", BotPerm: "view"}},
		Contributes:  Contributes{BotCards: []BotCard{{ID: "c", Title: "Card", Route: "card"}}, BotTabs: []BotTab{{ID: "t", Label: "Tab", Page: "main"}}},
		UI:           []UIPage{{ID: "main", File: "ui/main.html"}},
	}
}

func testFiles(t *testing.T, m Manifest) map[string][]byte {
	t.Helper()
	mb, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{
		ManifestFile:   mb,
		WasmFile:       []byte("\x00asm\x01\x00\x00\x00"),
		"ui/main.html": []byte("<!doctype html><p>hi</p>"),
		IconFile:       []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
	}
}

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, func(string) (ed25519.PublicKey, bool)) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv, func(id string) (ed25519.PublicKey, bool) { return pub, id == KeyID(pub) }
}

// rezip rewrites an archive, letting edit change the file map (SIGNATURE included).
func rezip(t *testing.T, raw []byte, edit func(map[string][]byte)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		files[f.Name] = b.Bytes()
	}
	edit(files)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, d := range files {
		w, _ := zw.Create(n)
		w.Write(d)
	}
	zw.Close()
	return buf.Bytes()
}

func TestPackReadVerify(t *testing.T) {
	_, priv, keyFor := keys(t)
	raw, err := Pack(testFiles(t, testManifest()), priv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.ID != "dev.example.tool" || len(b.Files) != 4 || b.SHA256 == "" {
		t.Fatalf("%+v", b.Manifest)
	}
	if _, err := b.Verify(keyFor); err != nil {
		t.Fatal(err)
	}
	// Packing is deterministic, so a rebuilt bundle has the same hash.
	again, _ := Pack(testFiles(t, testManifest()), priv)
	if !bytes.Equal(raw, again) {
		t.Fatal("pack is not deterministic")
	}
}

func TestTamperDetection(t *testing.T) {
	_, priv, keyFor := keys(t)
	raw, _ := Pack(testFiles(t, testManifest()), priv)
	cases := map[string]func(map[string][]byte){
		"byte changed": func(f map[string][]byte) { f[WasmFile] = append([]byte(nil), f[WasmFile]...); f[WasmFile][7] ^= 1 },
		"page changed": func(f map[string][]byte) { f["ui/main.html"] = []byte("<script>evil()</script>") },
		"manifest edited": func(f map[string][]byte) {
			f[ManifestFile] = bytes.Replace(f[ManifestFile], []byte(`"Tool"`), []byte(`"Tooo"`), 1)
		},
		"icon removed": func(f map[string][]byte) { delete(f, IconFile) },
		"file added": func(f map[string][]byte) {
			m := testManifest()
			m.UI = append(m.UI, UIPage{ID: "x", File: "ui/x.html"})
			f[ManifestFile], _ = json.Marshal(m)
			f["ui/x.html"] = []byte("x")
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := Read(rezip(t, raw, edit))
			if err != nil {
				t.Fatal(err) // structurally fine; only the signature may catch it
			}
			if _, err := b.Verify(keyFor); err != ErrBadSignature {
				t.Fatalf("verify = %v, want ErrBadSignature", err)
			}
		})
	}

	t.Run("unsigned", func(t *testing.T) {
		raw, _ := Pack(testFiles(t, testManifest()), nil)
		b, err := Read(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Verify(keyFor); err != ErrUnsigned {
			t.Fatal(err)
		}
	})
	t.Run("unknown key", func(t *testing.T) {
		_, other, _ := keys(t)
		raw, _ := Pack(testFiles(t, testManifest()), other)
		b, _ := Read(raw)
		if _, err := b.Verify(keyFor); err == nil || !strings.Contains(err.Error(), "unknown publisher key") {
			t.Fatal(err)
		}
	})
	t.Run("signature swapped to another key id", func(t *testing.T) {
		pub2, other, _ := keys(t)
		raw, _ := Pack(testFiles(t, testManifest()), other)
		b, _ := Read(raw)
		// A key store that (wrongly) answers for any id must still not verify.
		if _, err := b.Verify(func(string) (ed25519.PublicKey, bool) { return keysPub(t), true }); err != ErrBadSignature {
			t.Fatal(err, pub2)
		}
	})
}

func keysPub(t *testing.T) ed25519.PublicKey { p, _, _ := keys(t); return p }

func TestStructuralRejections(t *testing.T) {
	good := testFiles(t, testManifest())
	cases := map[string]func(map[string][]byte){
		"traversal":        func(f map[string][]byte) { f["../evil"] = []byte("x") },
		"absolute":         func(f map[string][]byte) { f["/etc/passwd"] = []byte("x") },
		"hidden":           func(f map[string][]byte) { f[".env"] = []byte("x") },
		"undeclared file":  func(f map[string][]byte) { f["extra.txt"] = []byte("x") },
		"case collision":   func(f map[string][]byte) { f["UI/main.html"] = []byte("x") },
		"not wasm":         func(f map[string][]byte) { f[WasmFile] = []byte("MZ\x90\x00not wasm") },
		"missing wasm":     func(f map[string][]byte) { delete(f, WasmFile) },
		"missing page":     func(f map[string][]byte) { delete(f, "ui/main.html") },
		"missing manifest": func(f map[string][]byte) { delete(f, ManifestFile) },
		"unknown field": func(f map[string][]byte) {
			f[ManifestFile] = bytes.Replace(f[ManifestFile], []byte(`{`), []byte(`{"surprise":1,`), 1)
		},
		"malformed signature": func(f map[string][]byte) { f[SignatureFile] = []byte("{}") },
	}
	raw, _ := Pack(good, nil)
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(rezip(t, raw, edit)); err == nil {
				t.Fatal("accepted")
			}
		})
	}

	t.Run("zip bomb", func(t *testing.T) {
		big := bytes.Repeat([]byte{0}, 8<<20)
		bomb := rezip(t, raw, func(f map[string][]byte) { f[WasmFile] = append([]byte("\x00asm\x01\x00\x00\x00"), big...) })
		if _, err := Read(bomb); err == nil || !strings.Contains(err.Error(), "ratio") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for n, d := range good {
			w, _ := zw.Create(n)
			w.Write(d)
		}
		h := &zip.FileHeader{Name: "icon2.svg"}
		h.SetMode(0o777 | 1<<27) // os.ModeSymlink
		w, _ := zw.CreateHeader(h)
		w.Write([]byte("/etc/passwd"))
		zw.Close()
		if _, err := Read(buf.Bytes()); err == nil {
			t.Fatal("symlink accepted")
		}
	})
	t.Run("too large", func(t *testing.T) {
		if _, err := Read(make([]byte, MaxCompressed+1)); err == nil {
			t.Fatal("accepted")
		}
	})
}

func TestManifestValidation(t *testing.T) {
	cases := map[string]func(*Manifest){
		"bad id":      func(m *Manifest) { m.ID = "Tool" },
		"bad version": func(m *Manifest) { m.Version = "v1" },
		"abi":         func(m *Manifest) { m.Compat.ABI = 2 },
		"bad range":   func(m *Manifest) { m.Compat.Panel = "~1.0" },
		"memory":      func(m *Manifest) { m.Runtime.MemoryMB = 512 },
		"unknown capability": func(m *Manifest) {
			m.Capabilities = append(m.Capabilities, Capability{Name: "docker.socket", Reason: "x"})
		},
		"no reason":       func(m *Manifest) { m.Capabilities[0].Reason = "" },
		"ip host":         func(m *Manifest) { m.Capabilities[1].Hosts = []string{"169.254.169.254"} },
		"localhost":       func(m *Manifest) { m.Capabilities[1].Hosts = []string{"localhost"} },
		"wildcard":        func(m *Manifest) { m.Capabilities[1].Hosts = []string{"*.example.com"} },
		"host with port":  func(m *Manifest) { m.Capabilities[1].Hosts = []string{"example.com:8080"} },
		"internal suffix": func(m *Manifest) { m.Capabilities[1].Hosts = []string{"db.internal"} },
		"public no verify": func(m *Manifest) {
			m.Routes = append(m.Routes, Route{ID: "p", Method: "POST", Path: "/p", Auth: "public"})
		},
		"bad bot perm": func(m *Manifest) { m.Routes[0].BotPerm = "root" },
		"unknown perm": func(m *Manifest) {
			m.Routes = append(m.Routes, Route{ID: "u", Method: "GET", Path: "/u", Auth: "user", Perm: "nope"})
		},
		"card needs bot GET": func(m *Manifest) { m.Contributes.BotCards[0].Route = "missing" },
		"tab needs page":     func(m *Manifest) { m.Contributes.BotTabs[0].Page = "missing" },
		"short job":          func(m *Manifest) { m.Jobs = []Job{{ID: "j", Every: "1m"}} },
		"unknown event":      func(m *Manifest) { m.Events = []string{"user.login"} },
		"discord needs consent": func(m *Manifest) {
			m.Capabilities = append(m.Capabilities, Capability{Name: CapDiscordUser, Reason: "x"})
		},
		"secret default": func(m *Manifest) { m.Settings[0].Default = "x" },
		"ui path":        func(m *Manifest) { m.UI[0].File = "../main.html" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			m := testManifest()
			edit(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	m := testManifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRange(t *testing.T) {
	r, err := ParseRange(">=0.9.0 <1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for v, want := range map[string]bool{"0.9.0": true, "0.9.1": true, "0.10.0-rc.1": true, "1.0.0": false, "0.8.9": false, "dev": true} {
		if r.Allows(v) != want {
			t.Errorf("%s: %v", v, !want)
		}
	}
	if CompareVersions("1.10.0", "1.9.0") <= 0 {
		t.Fatal("compare")
	}
}
