package api

import (
	"io"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

func TestAcceptsEncoding(t *testing.T) {
	cases := []struct {
		header, enc string
		want        bool
	}{
		{"gzip, deflate, br, zstd", "br", true},
		{"gzip, deflate, br, zstd", "gzip", true},
		{"gzip", "br", false},
		{"", "gzip", false},
		{"br;q=0, gzip", "br", false},
		{"br;q=0.5", "br", true},
		{"*", "br", true},
		{"*;q=0", "gzip", false},
		{"br;q=0, *", "br", false},
		{"BR", "br", true},
	}
	for _, c := range cases {
		if got := acceptsEncoding(c.header, c.enc); got != c.want {
			t.Errorf("acceptsEncoding(%q, %q) = %v, want %v", c.header, c.enc, got, c.want)
		}
	}
}

func TestStaticServesPrecompressedVariants(t *testing.T) {
	fsys := fstest.MapFS{
		"_app/immutable/a.js":    {Data: []byte("raw-js")},
		"_app/immutable/a.js.br": {Data: []byte("br-js")},
		"_app/immutable/a.js.gz": {Data: []byte("gz-js")},
		"_app/immutable/b.png":   {Data: []byte("png")},
		"200.html":               {Data: []byte("<!doctype html><title>x</title>")},
		"200.html.gz":            {Data: []byte("gz-html")},
	}
	app := fiber.New()
	app.Use(staticHandler(fsys))

	get := func(path, accept string) (body, encoding, vary, ctype string) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b), res.Header.Get("Content-Encoding"), res.Header.Get("Vary"), res.Header.Get("Content-Type")
	}

	if body, enc, vary, ct := get("/_app/immutable/a.js", "gzip, br"); body != "br-js" || enc != "br" || vary != "Accept-Encoding" || ct != "text/javascript; charset=utf-8" {
		t.Errorf("brotli: body %q enc %q vary %q type %q", body, enc, vary, ct)
	}
	if body, enc, _, _ := get("/_app/immutable/a.js", "gzip"); body != "gz-js" || enc != "gzip" {
		t.Errorf("gzip: body %q enc %q", body, enc)
	}
	if body, enc, vary, _ := get("/_app/immutable/a.js", ""); body != "raw-js" || enc != "" || vary != "Accept-Encoding" {
		t.Errorf("identity: body %q enc %q vary %q", body, enc, vary)
	}
	if body, enc, vary, _ := get("/_app/immutable/b.png", "br, gzip"); body != "png" || enc != "" || vary != "" {
		t.Errorf("no variant: body %q enc %q vary %q", body, enc, vary)
	}
	// Extension pages are routes even though their dotted ids look like files.
	for _, p := range []string{"/admin/extensions/dev.example.tool", "/x/dev.example.tool/setup"} {
		if body, _, _, ct := get(p, ""); body != "<!doctype html><title>x</title>" || ct != "text/html; charset=utf-8" {
			t.Errorf("%s: body %q type %q", p, body, ct)
		}
	}
	if _, _, _, ct := get("/missing.js", ""); ct == "text/html; charset=utf-8" {
		t.Error("a missing asset got the app shell")
	}
	// A client-side route gets the compressed SPA shell.
	if body, enc, _, ct := get("/dashboard", "br, gzip"); body != "gz-html" || enc != "gzip" || ct != "text/html; charset=utf-8" {
		t.Errorf("shell: body %q enc %q type %q", body, enc, ct)
	}
}
