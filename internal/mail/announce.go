package mail

import (
	"html"
	"strings"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// MaxAnnouncementHTML bounds the HTML an administrator can send, before the
// branded wrapper is added.
const MaxAnnouncementHTML = 80 << 10

var (
	// Elements that have no place in an email and that some clients act on.
	dangerousBlock = lazyre.New(`(?is)<\s*(script|iframe|object|embed|form|template|noscript)\b[^>]*>.*?<\s*/\s*(script|iframe|object|embed|form|template|noscript)\s*>`)
	dangerousTag   = lazyre.New(`(?is)<\s*/?\s*(script|iframe|object|embed|form|input|button|textarea|select|link|meta|base|frame|frameset|applet)\b[^>]*>`)
	eventAttr      = lazyre.New(`(?is)\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	scriptURL      = lazyre.New(`(?is)(href|src|action|formaction|xlink:href)\s*=\s*("|')?\s*(javascript|vbscript|data)\s*:`)
	tagRe          = lazyre.New(`(?s)<[^>]*>`)
	breakRe        = lazyre.New(`(?i)<\s*(br\s*/?|/p|/div|/h[1-6]|/li|/tr)\s*>`)
	styleBlock     = lazyre.New(`(?is)<\s*(style|head|title)\b[^>]*>.*?<\s*/\s*(style|head|title)\s*>`)
	blankLines     = lazyre.New(`\n{3,}`)
)

// SanitizeHTML removes scripts, embedded frames, forms, event-handler
// attributes and script-bearing URLs from administrator-written HTML. Email
// clients already refuse to run scripts, so this is hygiene for the few that
// do not and for the preview, not a security boundary: only administrators can
// send announcements.
func SanitizeHTML(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	for i := 0; i < 3; i++ { // nested tricks such as <scr<script>ipt>
		before := s
		s = dangerousBlock.ReplaceAllString(s, "")
		s = dangerousTag.ReplaceAllString(s, "")
		s = eventAttr.ReplaceAllString(s, "")
		s = scriptURL.ReplaceAllString(s, `$1=$2#blocked:`)
		if s == before {
			break
		}
	}
	return strings.TrimSpace(s)
}

// PlainText derives a readable text alternative from HTML.
func PlainText(h string) string {
	h = styleBlock.ReplaceAllString(h, "")
	h = breakRe.ReplaceAllString(h, "\n")
	h = tagRe.ReplaceAllString(h, "")
	h = html.UnescapeString(h)
	lines := strings.Split(h, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// Announcement wraps administrator-written HTML in the standard layout. The
// body is sanitized here as well, so no caller can skip it. footer says why
// the person receives it.
func Announcement(subject, bodyHTML, footer string) Body {
	clean := SanitizeHTML(bodyHTML)
	text := PlainText(clean)
	if footer != "" {
		text += "\n\n--\n" + footer
	}
	var h strings.Builder
	h.WriteString(`<!doctype html><html><body style="margin:0;background:#f4f5f7;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1c2330">`)
	h.WriteString(`<div style="max-width:600px;margin:0 auto;padding:24px 16px"><div style="background:#fff;border:1px solid #e3e6ec;border-radius:10px;padding:24px;font-size:14px;line-height:1.55">`)
	h.WriteString(clean)
	h.WriteString(`</div>`)
	if footer != "" {
		h.WriteString(`<p style="text-align:center;font-size:12px;color:#6a7384;line-height:1.5;margin:16px 0 0">` + html.EscapeString(footer) + `</p>`)
	}
	h.WriteString(`<p style="text-align:center;font-size:12px;color:#9aa1af">RivetPanel</p></div></body></html>`)
	return Body{Subject: subject, Text: text, HTML: h.String()}
}
