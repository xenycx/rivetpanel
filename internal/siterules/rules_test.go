package siterules

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseRedirects(t *testing.T) {
	rs, err := ParseRedirects([]byte("# comment\n/old /new\n/blog/:slug /posts/:slug 302\n/app/* /index.html 200\n/go https://example.com/x 308!\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 4 || rs[0].Status != 301 || rs[1].Status != 302 || rs[2].Status != 200 || !rs[3].Force {
		t.Fatalf("got %+v", rs)
	}
	bad := map[string]string{
		"/a":                     "expected",
		"/a /b 418":              "status",
		"/a/*/b /c":              "last segment",
		"a /b":                   "start with /",
		"/a //evil.com":          "protocol-relative",
		"/a https://x.test 200":  "proxying",
		"/a javascript:alert(1)": "target must",
		"/a?x=1 /b":              "query",
	}
	for in, want := range bad {
		_, err := ParseRedirects([]byte("\n" + in + "\n"))
		var le *LineError
		if !errors.As(err, &le) || le.Line != 2 || !strings.Contains(le.Error(), want) {
			t.Errorf("%q: got %v, want line 2 error containing %q", in, err, want)
		}
	}
}

func TestParseHeaders(t *testing.T) {
	hs, err := ParseHeaders([]byte("/*\n  X-Frame-Options: DENY\n/assets/*\n  cache-control: public, max-age=60\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 2 || hs[1].Headers[0].Name != "Cache-Control" {
		t.Fatalf("got %+v", hs)
	}
	for _, in := range []string{"/x\n  Set-Cookie: a=b\n", "  X-A: b\n", "/x\n/y\n  X-A: b\n", "/x\n", "/x\n  bad header\n"} {
		if _, err := ParseHeaders([]byte(in)); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func TestMatch(t *testing.T) {
	rs, err := Load(mustJSON(t, Rules{
		Redirects: []Redirect{
			{From: "/blog/:slug", To: "/posts/:slug?ref=:slug", Status: 301},
			{From: "/docs/*", To: "/manual/:splat", Status: 302},
			{From: "/*", To: "/index.html", Status: 200},
			{From: "/force", To: "/elsewhere", Status: 301, Force: true},
		},
		Headers: []HeaderRule{
			{Path: "/*", Headers: []Header{{Name: "X-A", Value: "1"}}},
			{Path: "/assets/*", Headers: []Header{{Name: "X-A", Value: "2"}}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path   string
		exists bool
		want   string
	}{
		{"/blog/hello", false, "/posts/hello?ref=hello"},
		{"/docs/a/b", false, "/manual/a/b"},
		{"/docs", false, "/manual/"},
		{"/anything", false, "/index.html"},
		{"/", false, "/index.html"},
		{"/force", true, "/elsewhere"},
	}
	for _, c := range cases {
		m, ok := rs.Redirect(c.path, c.exists)
		if !ok || m.Target != c.want {
			t.Errorf("%s: got %q %v, want %q", c.path, m.Target, ok, c.want)
		}
	}
	if _, ok := rs.Redirect("/blog/hello", true); ok {
		t.Error("an existing file must shadow unforced rules")
	}
	h := http.Header{}
	rs.Apply(h, "/assets/app.js")
	if h.Get("X-A") != "2" {
		t.Errorf("later header rules must win, got %q", h.Get("X-A"))
	}
}

func TestPublish(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("body{color:red}\n", 400)
	write(t, dir, "index.html", "<p>hi</p>")
	write(t, dir, "app.css", big)
	write(t, dir, "pre.js", big)
	write(t, dir, "pre.js.br", "x")
	write(t, dir, "_redirects", "/a /b 302\n")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := Publish(root); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, GzipDir, "app.css.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); string(got) != big {
		t.Error("gzip copy does not round-trip")
	}
	f.Close()
	for _, p := range []string{"index.html.gz", "pre.js.gz"} {
		if _, err := os.Stat(filepath.Join(dir, GzipDir, p)); err == nil {
			t.Errorf("%s: small files and files with a sibling are not compressed", p)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, CompiledFile))
	if err != nil || !strings.Contains(string(data), `"/a"`) {
		t.Fatalf("compiled rules: %s %v", data, err)
	}
	write(t, dir, "_headers", "/x\n  Set-Cookie: a\n")
	var le *LineError
	if err := Publish(root); !errors.As(err, &le) || le.File != HeadersFile {
		t.Fatalf("invalid _headers: got %v", err)
	}
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, r Rules) []byte {
	t.Helper()
	dir := t.TempDir()
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	var lines []string
	for _, x := range r.Redirects {
		s := x.From + " " + x.To + " " + strconv.Itoa(x.Status)
		if x.Force {
			s += "!"
		}
		lines = append(lines, s)
	}
	write(t, dir, RedirectsFile, strings.Join(lines, "\n"))
	var hs []string
	for _, h := range r.Headers {
		hs = append(hs, h.Path)
		for _, x := range h.Headers {
			hs = append(hs, "  "+x.Name+": "+x.Value)
		}
	}
	write(t, dir, HeadersFile, strings.Join(hs, "\n"))
	if err := Publish(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, CompiledFile))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
