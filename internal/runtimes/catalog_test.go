package runtimes

import (
	"strings"
	"testing"
	"testing/fstest"

	rt "github.com/xenycx/rivetpanel/runtimes"
)

func TestEmbeddedCatalogLoadsAllSix(t *testing.T) {
	c, err := Load(rt.FS)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(c.List()); n != 6 {
		t.Fatalf("runtimes = %d", n)
	}
	node, _ := c.Get("nodejs")
	if !node.CommandAllowed("node") || node.CommandAllowed("sh") || node.CommandAllowed("/bin/sh") {
		t.Fatal("node command policy wrong")
	}
	g, _ := c.Get("go")
	if !g.CommandAllowed("./app") || !g.CommandAllowed("bin/app") || g.CommandAllowed("../app") || g.CommandAllowed("/bin/sh") {
		t.Fatal("go binary policy wrong")
	}
}

const base = `id: nodejs
display_name: N
image: node:24-alpine
builder_image: node:24-alpine
build_argv: [npm, ci]
default_argv: [node, x.js]
allowed_commands: [node]
defaults: {memory_bytes: 100, nano_cpus: 1, pids_limit: 10}
min_memory_bytes: 10
`

func load(s string) error {
	_, err := Load(fstest.MapFS{"a.yaml": {Data: []byte(s)}})
	return err
}

func TestValidation(t *testing.T) {
	if err := load(base); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"id":      strings.Replace(base, "id: nodejs", "id: php", 1),
		"image":   strings.Replace(base, "image: node:24-alpine", "image: 'node; rm -rf /'", 1),
		"digest":  base + "digest: sha256:zz\n",
		"unknown": base + "privileged: true\n",
		"argv0":   strings.Replace(base, "default_argv: [node, x.js]", "default_argv: [sh, x]", 1),
		"memmin":  strings.Replace(base, "min_memory_bytes: 10", "min_memory_bytes: 1000", 1),
		"pids":    strings.Replace(base, "pids_limit: 10", "pids_limit: 99999", 1),
	}
	for name, s := range bad {
		if load(s) == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestImageRefPinsDigest(t *testing.T) {
	r := Runtime{Image: "node:24-alpine", Digest: "sha256:" + strings.Repeat("a", 64)}
	if got := r.ImageRef(); got != "node:24-alpine@sha256:"+strings.Repeat("a", 64) {
		t.Fatal(got)
	}
}
