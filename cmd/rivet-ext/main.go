// Command rivet-ext creates publisher keys and packs, signs and verifies
// RivetPanel extension bundles (.rpx).
//
//	rivet-ext keygen -out publisher.key        (prints the public key)
//	rivet-ext pack -dir build/my-ext -out my-ext.rpx [-key publisher.key]
//	rivet-ext sign -in my-ext.rpx -key publisher.key -out signed.rpx
//	rivet-ext verify -in my-ext.rpx -pub ed25519:...
//	rivet-ext inspect -in my-ext.rpx
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/xenycx/rivetpanel/internal/extensions/bundle"
)

const privPrefix = "ed25519-private:"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "pack":
		err = pack(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "inspect":
		err = inspect(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rivet-ext:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rivet-ext keygen|pack|sign|verify|inspect [flags] (see -h on each)")
	os.Exit(2)
}

func keygen(args []string) error {
	fl := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fl.String("out", "", "file to write the private key to (mode 0600; must not exist)")
	fl.Parse(args)
	if *out == "" {
		return fmt.Errorf("-out is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, privPrefix+base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("public key: %s\nkey id:     %s\n", bundle.FormatPublicKey(pub), bundle.KeyID(pub))
	return nil
}

// loadKey reads a private key from a file, or from the environment variable
// named by "env:NAME" (for CI secrets).
func loadKey(ref string) (ed25519.PrivateKey, error) {
	var text string
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		text = os.Getenv(name)
		if text == "" {
			return nil, fmt.Errorf("$%s is empty", name)
		}
	} else {
		b, err := os.ReadFile(ref)
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	raw, ok := strings.CutPrefix(strings.TrimSpace(text), privPrefix)
	if !ok {
		return nil, fmt.Errorf("private key must start with %s", privPrefix)
	}
	seed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("private key is not a 32-byte Ed25519 seed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func pack(args []string) error {
	fl := flag.NewFlagSet("pack", flag.ExitOnError)
	dir := fl.String("dir", "", "directory holding manifest.json, plugin.wasm and optional icon.svg and ui/*.html")
	out := fl.String("out", "", "bundle to write")
	key := fl.String("key", "", "private key file or env:NAME (omit for an unsigned development bundle)")
	fl.Parse(args)
	if *dir == "" || *out == "" {
		return fmt.Errorf("-dir and -out are required")
	}
	files := map[string][]byte{}
	err := filepath.WalkDir(*dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		rel, _ := filepath.Rel(*dir, p)
		rel = filepath.ToSlash(rel)
		if rel == bundle.SignatureFile {
			return nil
		}
		b, err := os.ReadFile(p)
		files[rel] = b
		return err
	})
	if err != nil {
		return err
	}
	var priv ed25519.PrivateKey
	if *key != "" {
		if priv, err = loadKey(*key); err != nil {
			return err
		}
	}
	raw, err := bundle.Pack(files, priv)
	if err != nil {
		return err
	}
	b, err := bundle.Read(raw) // the same checks the panel applies
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	signed := "unsigned"
	if priv != nil {
		signed = "signed by " + bundle.KeyID(priv.Public().(ed25519.PublicKey))
	}
	fmt.Printf("%s %s %s (%d bytes, %s), sha256 %s\n", *out, b.Manifest.ID, b.Manifest.Version, len(raw), signed, b.SHA256)
	return nil
}

func sign(args []string) error {
	fl := flag.NewFlagSet("sign", flag.ExitOnError)
	in := fl.String("in", "", "bundle to sign")
	out := fl.String("out", "", "signed bundle to write")
	key := fl.String("key", "", "private key file or env:NAME")
	fl.Parse(args)
	if *in == "" || *out == "" || *key == "" {
		return fmt.Errorf("-in, -out and -key are required")
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	b, err := bundle.Read(raw)
	if err != nil {
		return err
	}
	priv, err := loadKey(*key)
	if err != nil {
		return err
	}
	signed, err := bundle.Pack(b.Files, priv)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, signed, 0o644)
}

func verify(args []string) error {
	fl := flag.NewFlagSet("verify", flag.ExitOnError)
	in := fl.String("in", "", "bundle to verify")
	pubText := fl.String("pub", "", "publisher public key (ed25519:...)")
	fl.Parse(args)
	if *in == "" || *pubText == "" {
		return fmt.Errorf("-in and -pub are required")
	}
	pub, err := bundle.ParsePublicKey(*pubText)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	b, err := bundle.Read(raw)
	if err != nil {
		return err
	}
	id, err := b.Verify(func(k string) (ed25519.PublicKey, bool) { return pub, k == bundle.KeyID(pub) })
	if err != nil {
		return err
	}
	fmt.Printf("ok: %s %s signed by %s\n", b.Manifest.ID, b.Manifest.Version, id)
	return nil
}

func inspect(args []string) error {
	fl := flag.NewFlagSet("inspect", flag.ExitOnError)
	in := fl.String("in", "", "bundle to describe")
	fl.Parse(args)
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	b, err := bundle.Read(raw)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	key := ""
	if b.Sig != nil {
		key = b.Sig.KeyID
	}
	return enc.Encode(map[string]any{"sha256": b.SHA256, "signed_by": key, "manifest": b.Manifest})
}
