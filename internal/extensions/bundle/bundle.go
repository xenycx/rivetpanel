package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Limits on an uploaded bundle.
const (
	MaxCompressed   = 16 << 20
	MaxUncompressed = 32 << 20
	MaxEntries      = 256
	maxRatio        = 100 // uncompressed/compressed, per entry over 64 KiB
)

// File names inside a bundle.
const (
	ManifestFile  = "manifest.json"
	WasmFile      = "plugin.wasm"
	IconFile      = "icon.svg"
	SignatureFile = "SIGNATURE"
)

// signingContext separates extension signatures from any other use of a key.
const signingContext = "rivetpanel-extension-v1\n"

var (
	ErrUnsigned     = errors.New("bundle is not signed")
	ErrBadSignature = errors.New("bundle signature does not match its contents")
	nameRe          = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

// Bundle is a parsed, structurally valid extension bundle. A Bundle is not
// trusted until Verify succeeds against a known publisher key.
type Bundle struct {
	Manifest Manifest
	Files    map[string][]byte // every file except SIGNATURE
	Sig      *Signature
	SHA256   string // of the raw .rpx bytes
	Raw      []byte
}

// Signature is the SIGNATURE file.
type Signature struct {
	KeyID string `json:"key_id"`
	Sig   string `json:"sig"` // base64 Ed25519 over signingContext + ContentList
}

// Read parses a .rpx archive, enforcing every structural limit. It does not
// check the signature.
func Read(raw []byte) (*Bundle, error) {
	if len(raw) > MaxCompressed {
		return nil, fmt.Errorf("bundle is larger than %d MiB", MaxCompressed>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("bundle is not a zip archive: %w", err)
	}
	if len(zr.File) > MaxEntries {
		return nil, fmt.Errorf("bundle has more than %d entries", MaxEntries)
	}
	b := &Bundle{Files: map[string][]byte{}, Raw: raw}
	sum := sha256.Sum256(raw)
	b.SHA256 = hex.EncodeToString(sum[:])
	folded := map[string]bool{}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasSuffix(name, "/") && f.Mode().IsDir() {
			continue // directory entries carry nothing
		}
		if err := checkName(name); err != nil {
			return nil, err
		}
		if !f.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: only regular files are allowed (no links or devices)", name)
		}
		key := strings.ToLower(name)
		if folded[key] {
			return nil, fmt.Errorf("%s: duplicate (or case-colliding) file name", name)
		}
		folded[key] = true
		if f.UncompressedSize64 > MaxUncompressed {
			return nil, fmt.Errorf("%s is too large", name)
		}
		if f.UncompressedSize64 > 64<<10 && f.CompressedSize64 > 0 && f.UncompressedSize64/f.CompressedSize64 > maxRatio {
			return nil, fmt.Errorf("%s: compression ratio is suspiciously high", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		// The declared size can lie; count what actually comes out.
		data, err := io.ReadAll(io.LimitReader(rc, MaxUncompressed-total+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		total += int64(len(data))
		if total > MaxUncompressed {
			return nil, fmt.Errorf("bundle expands beyond %d MiB", MaxUncompressed>>20)
		}
		if uint64(len(data)) != f.UncompressedSize64 {
			return nil, fmt.Errorf("%s: size does not match its header", name)
		}
		if name == SignatureFile {
			var s Signature
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&s); err != nil || s.KeyID == "" || s.Sig == "" {
				return nil, fmt.Errorf("SIGNATURE is malformed")
			}
			b.Sig = &s
			continue
		}
		b.Files[name] = data
	}
	mb, ok := b.Files[ManifestFile]
	if !ok {
		return nil, fmt.Errorf("bundle has no %s", ManifestFile)
	}
	if b.Manifest, err = ParseManifest(mb); err != nil {
		return nil, err
	}
	if err := b.checkContents(); err != nil {
		return nil, err
	}
	return b, nil
}

func checkName(name string) error {
	if len(name) > 200 || !nameRe.MatchString(name) || path.Clean(name) != name || strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
		return fmt.Errorf("%q: file names must be plain relative paths (no .., absolute paths, hidden files or unusual characters)", name)
	}
	return nil
}

// checkContents allows exactly the files a bundle may hold.
func (b *Bundle) checkContents() error {
	wasm, ok := b.Files[WasmFile]
	if !ok {
		return fmt.Errorf("bundle has no %s", WasmFile)
	}
	if len(wasm) < 8 || !bytes.Equal(wasm[:4], []byte("\x00asm")) {
		return fmt.Errorf("%s is not a WebAssembly module", WasmFile)
	}
	pages := map[string]bool{}
	for _, p := range b.Manifest.UI {
		pages[p.File] = true
		if _, ok := b.Files[p.File]; !ok {
			return fmt.Errorf("ui page %q: %s is missing", p.ID, p.File)
		}
	}
	for name, data := range b.Files {
		switch {
		case name == ManifestFile, name == WasmFile:
		case name == IconFile:
			if len(data) > 64<<10 || !bytes.Contains(data[:min(len(data), 1024)], []byte("<svg")) {
				return fmt.Errorf("icon.svg must be an SVG under 64 KiB")
			}
		case pages[name]:
			if len(data) > 2<<20 {
				return fmt.Errorf("%s is larger than 2 MiB", name)
			}
		default:
			return fmt.Errorf("unexpected file %q (a bundle holds manifest.json, plugin.wasm, icon.svg, SIGNATURE and the declared ui pages)", name)
		}
	}
	return nil
}

// ContentList is the canonical text a signature covers: one line per file
// (SIGNATURE excluded), sorted by name: "name\tsha256\tsize\n".
func ContentList(files map[string][]byte) []byte {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	buf.WriteString(signingContext)
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(&buf, "%s\t%s\t%d\n", n, hex.EncodeToString(sum[:]), len(files[n]))
	}
	return buf.Bytes()
}

// KeyID is the short fingerprint of a publisher key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Verify checks the signature with the key the bundle names, looked up with
// keyFor. It fails when the bundle is unsigned, the key is unknown, or any
// file was added, removed or changed after signing.
func (b *Bundle) Verify(keyFor func(keyID string) (ed25519.PublicKey, bool)) (string, error) {
	if b.Sig == nil {
		return "", ErrUnsigned
	}
	pub, ok := keyFor(b.Sig.KeyID)
	if !ok {
		return "", fmt.Errorf("bundle is signed by unknown publisher key %s", b.Sig.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(b.Sig.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize || KeyID(pub) != b.Sig.KeyID {
		return "", ErrBadSignature
	}
	if !ed25519.Verify(pub, ContentList(b.Files), sig) {
		return "", ErrBadSignature
	}
	return b.Sig.KeyID, nil
}

// Pack writes files (and a signature, when priv is set) as a .rpx archive
// with a deterministic order and timestamps.
func Pack(files map[string][]byte, priv ed25519.PrivateKey) ([]byte, error) {
	all := map[string][]byte{}
	for n, d := range files {
		if err := checkName(n); err != nil {
			return nil, err
		}
		all[n] = d
	}
	if priv != nil {
		pub := priv.Public().(ed25519.PublicKey)
		s, _ := json.Marshal(Signature{KeyID: KeyID(pub), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, ContentList(files)))})
		all[SignatureFile] = s
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(all[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ParsePublicKey reads a key written by `rivet-ext keygen` ("ed25519:<base64>").
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(s), "ed25519:")
	if !ok {
		return nil, fmt.Errorf("public key must look like ed25519:<base64>")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is not a 32-byte Ed25519 key")
	}
	return ed25519.PublicKey(b), nil
}

// FormatPublicKey is the inverse of ParsePublicKey.
func FormatPublicKey(pub ed25519.PublicKey) string {
	return "ed25519:" + base64.StdEncoding.EncodeToString(pub)
}
