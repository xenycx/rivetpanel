package mail

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T, status int, reply string, seen *http.Request, form *map[string]string) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		if form != nil && r.Method == http.MethodPost {
			r.ParseMultipartForm(1 << 20)
			m := map[string]string{}
			for k, v := range r.MultipartForm.Value {
				m[k] = v[0]
			}
			*form = m
		}
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}
}

var cfg = Config{APIKey: "key-secret", Domain: "mg.example.com", Region: RegionEU, From: "RivetPanel <noreply@mg.example.com>"}

func TestSendBuildsMailgunRequest(t *testing.T) {
	var req http.Request
	var form map[string]string
	c := fake(t, 200, `{"id":"<abc@mg.example.com>","message":"Queued. Thank you."}`, &req, &form)
	id, err := c.Send(context.Background(), cfg, Message{To: "Ann <ann@example.com>", Subject: "Hi\r\nBcc: x@y.z", Text: "body", HTML: "<p>body</p>", Tag: "test", TestMode: true})
	if err != nil || id != "<abc@mg.example.com>" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if req.URL.Path != "/v3/mg.example.com/messages" {
		t.Fatalf("path %s", req.URL.Path)
	}
	if u, p, ok := req.BasicAuth(); !ok || u != "api" || p != "key-secret" {
		t.Fatalf("auth %v %v %v", u, p, ok)
	}
	if form["to"] != "ann@example.com" || form["from"] != cfg.From || form["o:testmode"] != "yes" || form["o:tag"] != "test" {
		t.Fatalf("form %v", form)
	}
	if strings.ContainsAny(form["subject"], "\r\n") {
		t.Fatalf("subject kept a line break: %q", form["subject"])
	}
}

func TestSendRejectsBadInputWithoutCalling(t *testing.T) {
	c := fake(t, 500, "", nil, nil)
	for _, m := range []Message{
		{To: "a@b.c\r\nBcc: x@y.z", Subject: "s", Text: "t"},
		{To: "not-an-address", Subject: "s", Text: "t"},
		{To: "a@b.co", Subject: "", Text: "t"},
		{To: "a@b.co", Subject: "s", Text: ""},
	} {
		if _, err := c.Send(context.Background(), cfg, m); err == nil {
			t.Fatalf("accepted %+v", m)
		} else if _, ok := err.(*Error); ok {
			t.Fatalf("reached Mailgun for %+v", m)
		}
	}
	if _, err := c.Send(context.Background(), Config{}, Message{To: "a@b.co", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("sent without configuration")
	}
}

func TestErrorsAreExplainedAndNeverLeakTheKey(t *testing.T) {
	for status, want := range map[int]string{401: "rejected the API key", 404: "does not know that domain", 429: "rate limiting", 400: "HTTP 400"} {
		c := fake(t, status, `{"message":"boom"}`, nil, nil)
		_, err := c.Send(context.Background(), cfg, Message{To: "a@b.co", Subject: "s", Text: "t"})
		me, ok := err.(*Error)
		if !ok || me.Status != status || !strings.Contains(me.Message, want) || strings.Contains(me.Message, cfg.APIKey) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}

func TestCheckReportsDNSState(t *testing.T) {
	c := fake(t, 200, `{"domain":{"name":"mg.example.com","state":"active","type":"custom"},"sending_dns_records":[{"valid":"valid"},{"valid":"unknown"}]}`, nil, nil)
	info, err := c.Check(context.Background(), cfg)
	if err != nil || info.State != "active" || info.Verified {
		t.Fatalf("%+v %v", info, err)
	}
	c = fake(t, 200, `{"domain":{"name":"mg.example.com","state":"active","type":"custom"},"sending_dns_records":[{"valid":"valid"}]}`, nil, nil)
	if info, _ := c.Check(context.Background(), cfg); !info.Verified {
		t.Fatalf("%+v", info)
	}
}

func TestRegionHost(t *testing.T) {
	if (Config{Region: "eu"}).BaseURL() != "https://api.eu.mailgun.net" || (Config{}).BaseURL() != "https://api.mailgun.net" {
		t.Fatal("wrong region host")
	}
}

func TestTemplatesEscapeHTML(t *testing.T) {
	b := Alert("⚠️ <b>bot</b> needs attention", "<script>alert(1)</script>")
	if strings.Contains(b.HTML, "<script>") || strings.Contains(b.HTML, "<b>bot") {
		t.Fatalf("unescaped HTML: %s", b.HTML)
	}
	if strings.HasPrefix(b.Subject, "⚠") {
		t.Fatalf("subject kept the emoji: %q", b.Subject)
	}
	if r := PasswordReset("https://p.example/reset#bpr_x", 60); !strings.Contains(r.Text, "https://p.example/reset#bpr_x") || !strings.Contains(r.HTML, "60 minutes") {
		t.Fatal("reset body is missing the link or expiry")
	}
}

func TestValidators(t *testing.T) {
	if _, err := ValidateDomain("https://mg.example.com"); err == nil {
		t.Fatal("accepted a URL as a domain")
	}
	if d, err := ValidateDomain(" MG.Example.com "); err != nil || d != "mg.example.com" {
		t.Fatal(d, err)
	}
	if _, err := ValidateFrom("RivetPanel <noreply@example.com>"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFrom("x\r\nBcc: a@b.co"); err == nil {
		t.Fatal("accepted a header injection")
	}
}

func TestSendBatchHidesOtherRecipients(t *testing.T) {
	var form map[string]string
	c := fake(t, 200, `{"id":"<b@mg.example.com>","message":"Queued. Thank you."}`, nil, &form)
	if _, err := c.SendBatch(context.Background(), cfg, []string{"a@example.com", "Bob <b@example.com>"}, Message{Subject: "News", Text: "hi", HTML: "<p>hi</p>"}); err != nil {
		t.Fatal(err)
	}
	if form["to"] != "a@example.com,b@example.com" || !strings.Contains(form["recipient-variables"], `"a@example.com":{}`) || !strings.Contains(form["recipient-variables"], `"b@example.com":{}`) {
		t.Fatalf("batch form: %v", form)
	}
	if _, err := c.SendBatch(context.Background(), cfg, nil, Message{Subject: "s", Text: "t"}); err == nil {
		t.Fatal("empty batch accepted")
	}
	if _, err := c.SendBatch(context.Background(), cfg, make([]string, MaxBatch+1), Message{Subject: "s", Text: "t"}); err == nil {
		t.Fatal("oversized batch accepted")
	}
	if _, err := c.SendBatch(context.Background(), cfg, []string{"a@b.co\r\nBcc: x@y.z"}, Message{Subject: "s", Text: "t"}); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestSanitizeHTML(t *testing.T) {
	in := `<h1 onclick="x()">Hi</h1><script>alert(1)</script><p>ok <a href="javascript:alert(1)">link</a> <a href='https://example.com'>safe</a></p>` +
		`<iframe src="https://evil"></iframe><form action="/x"><input name=a></form><scr<script>ipt>alert(2)</scr</script>ipt><img src=x onerror=alert(3)>`
	out := SanitizeHTML(in)
	for _, bad := range []string{"<script", "onclick", "onerror", "javascript:", "<iframe", "<form", "<input"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Fatalf("kept %q: %s", bad, out)
		}
	}
	for _, keep := range []string{"<h1", "Hi", `href='https://example.com'`, "safe"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("lost %q: %s", keep, out)
		}
	}
}

func TestAnnouncementHasTextAlternative(t *testing.T) {
	b := Announcement("Policy update", "<h2>Terms</h2><p>We changed <b>things</b> &amp; stuff.</p><style>p{color:red}</style>", "You get this because you have an account.")
	if !strings.Contains(b.Text, "Terms") || !strings.Contains(b.Text, "things & stuff.") || strings.Contains(b.Text, "color:red") || strings.Contains(b.Text, "<") {
		t.Fatalf("text: %q", b.Text)
	}
	if !strings.Contains(b.Text, "You get this because") || !strings.Contains(b.HTML, "<h2>Terms</h2>") {
		t.Fatal("footer or body missing")
	}
	if got := Announcement("s", "<script>x</script>hello", "").HTML; strings.Contains(got, "<script") {
		t.Fatal("announcement skipped the sanitizer")
	}
}
