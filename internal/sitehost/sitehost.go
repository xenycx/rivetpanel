// Package sitehost serves hosted static sites on their own listener. It never
// serves the panel: site files are arbitrary user HTML and scripts, so they
// must not share the panel's origin (cookies, CSRF tokens, service workers).
// Hosts are mapped to an immutable release directory opened through os.Root,
// so paths cannot leave the release.
package sitehost

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Router resolves hosts to releases (service.SiteService).
type Router interface {
	Resolve(host string) (domain.SiteRoute, bool)
	ReleaseRoot(siteID, releaseID string) (*os.Root, error)
	PublicPage(ctx context.Context, siteID string) (domain.PublicSitePage, error)
	TLSAllowed(host string) bool
}

// Handler serves sites.
type Handler struct {
	Sites Router
	Log   *slog.Logger
}

// TLSAskPath answers on-demand TLS permission checks (Caddy's "ask").
const TLSAskPath = "/.well-known/rivetpanel/tls-allowed"

// NewServer returns an HTTP server with conservative timeouts.
func NewServer(h *Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Minute, // large files over slow links
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if r.URL.Path == TLSAskPath {
		h.tlsAsk(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		page(w, http.StatusMethodNotAllowed, "Method not allowed", "Hosted sites are static: only GET and HEAD requests are served.")
		return
	}
	route, ok := h.Sites.Resolve(r.Host)
	switch {
	case !ok:
		page(w, http.StatusNotFound, "No site here", "No site is published at this address.")
		return
	case route.Disabled:
		page(w, http.StatusServiceUnavailable, "Site unavailable", "This site has been suspended by the administrator of this server.")
		return
	case route.Mode == "page":
		h.publicPage(w, r, route)
		return
	case route.Release == "":
		page(w, http.StatusServiceUnavailable, "Nothing published yet", "This site exists but nothing has been published to it yet.")
		return
	}
	root, err := h.Sites.ReleaseRoot(route.SiteID, route.Release)
	if err != nil {
		h.warn("open release", err)
		page(w, http.StatusServiceUnavailable, "Site unavailable", "This site cannot be served right now.")
		return
	}
	defer root.Close()
	h.serve(w, r, root, route)
}

type publicWidget struct {
	Key, Kind, Title, Group   string
	Span, MinHeight, Position int
	Data                      json.RawMessage
	UpdatedAtMS               int64
}

// publicPage renders the generated bot page from a redacted model. It is
// deliberately served on the sites origin: author HTML, CSS and scripts never
// execute with panel cookies or inside the authenticated panel origin.
func (h *Handler) publicPage(w http.ResponseWriter, r *http.Request, route domain.SiteRoute) {
	if path.Clean("/"+r.URL.Path) != "/" && path.Clean("/"+r.URL.Path) != "/index.html" {
		page(w, http.StatusNotFound, "Page not found", "There is no page at this address.")
		return
	}
	p, err := h.Sites.PublicPage(r.Context(), route.SiteID)
	if err != nil {
		h.warn("load public page", err)
		page(w, http.StatusServiceUnavailable, "Page unavailable", "This page cannot be served right now.")
		return
	}
	widgets := make([]publicWidget, 0, len(p.Widgets))
	for _, x := range p.Widgets {
		widgets = append(widgets, publicWidget{Key: x.Key, Kind: x.Kind, Title: x.Title, Group: x.Group, Span: x.Span,
			MinHeight: x.MinHeight, Position: x.Position, Data: json.RawMessage(x.PayloadJSON), UpdatedAtMS: x.UpdatedAtMS})
	}
	data, err := json.Marshal(widgets)
	if err != nil {
		h.warn("encode public widgets", err)
		data = []byte("[]")
	}
	title := p.Title
	if title == "" {
		title = p.BotName
	}
	identity := p.DiscordUsername
	if identity == "" {
		identity = p.BotName
	}
	avatar := ""
	if p.DiscordAvatarURL != "" {
		avatar = `<img class="avatar" src="` + html.EscapeString(p.DiscordAvatarURL) + `" alt="">`
	} else {
		initial := "B"
		if identity != "" {
			initial = strings.ToUpper(string([]rune(identity)[0]))
		}
		avatar = `<span class="avatar fallback">` + html.EscapeString(initial) + `</span>`
	}
	themeScript := ""
	if p.Theme == "system" {
		themeScript = ` data-theme="system"`
	} else if p.Theme == "daylight" {
		themeScript = ` data-theme="daylight"`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'self' https: data: blob:; img-src 'self' https: data:; style-src 'self' 'unsafe-inline' https:; script-src 'self' 'unsafe-inline' https:; object-src 'none'; base-uri 'self'; form-action 'self' https:")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"` + themeScript + `><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title><meta name="description" content="` + html.EscapeString(p.Description) + `"><style>` + publicPageCSS +
		`:root{--accent:` + html.EscapeString(p.Accent) + `}` + p.CSS + `</style></head><body><div class="aurora" aria-hidden="true"></div><main>` +
		`<header class="hero"><div class="identity">` + avatar + `<span><strong>` + html.EscapeString(identity) + `</strong><small>Discord bot</small></span></div>` +
		`<h1>` + html.EscapeString(title) + `</h1><p class="lede">` + html.EscapeString(p.Description) + `</p></header>` +
		`<section class="author-content">` + p.HTML + `</section><section id="widgets" class="widget-section" hidden><div class="section-head"><h2>Live from the bot</h2><p>Updated by the bot through RivetPanel</p></div><nav id="groups" aria-label="Widget groups"></nav><div id="grid" class="grid"></div></section>` +
		`<footer>Powered by <span>RivetPanel</span></footer></main><script>const W=` + string(data) + `;` + publicPageJS + `</script></body></html>`))
}

const publicPageCSS = `
*{box-sizing:border-box}html{color-scheme:dark;background:#090c14;color:#edf2ff;font:16px/1.55 Inter,ui-sans-serif,system-ui,sans-serif}html[data-theme=daylight]{color-scheme:light;background:#f4f7fb;color:#142033}html[data-theme=system]{color-scheme:light dark}body{margin:0;min-height:100vh;background:radial-gradient(ellipse 70% 45% at 50% -5%,color-mix(in srgb,var(--accent) 24%,transparent),transparent),linear-gradient(180deg,transparent 28rem,color-mix(in srgb,var(--accent) 4%,transparent));color:inherit}.aurora{position:fixed;inset:0 0 auto;height:18rem;pointer-events:none;border-top:3px solid var(--accent);opacity:.9}main{width:min(70rem,calc(100% - 2rem));margin:auto}.hero{min-height:31rem;display:flex;flex-direction:column;justify-content:center;align-items:flex-start;padding:6rem 0 4rem;max-width:54rem}.identity{display:flex;align-items:center;gap:.75rem;margin-bottom:2rem}.identity strong,.identity small{display:block}.identity small{opacity:.55;font-size:.78rem}.avatar{width:2.8rem;height:2.8rem;border-radius:35%;object-fit:cover;background:color-mix(in srgb,var(--accent) 22%,transparent)}.fallback{display:grid;place-items:center;font-weight:750;color:white;background:var(--accent)}h1{font-size:clamp(3.4rem,9vw,7.5rem);line-height:.88;letter-spacing:-.075em;margin:0;max-width:11ch;text-wrap:balance}.lede{font-size:clamp(1.05rem,2vw,1.35rem);max-width:42rem;opacity:.68;margin:2rem 0 0}.author-content{max-width:54rem;margin:0 0 5rem}.author-content:empty{display:none}.author-content h2{font-size:clamp(1.8rem,4vw,3.2rem);letter-spacing:-.04em}.author-content a{color:var(--accent)}.widget-section{padding:1rem 0 6rem}.section-head{display:flex;justify-content:space-between;gap:1rem;align-items:end;border-bottom:1px solid color-mix(in srgb,currentColor 15%,transparent);padding-bottom:1rem}.section-head h2{font-size:1.5rem;margin:0}.section-head p{margin:0;opacity:.5;font-size:.85rem}#groups{display:flex;gap:.4rem;overflow:auto;padding:1.25rem 0}#groups button{appearance:none;border:1px solid color-mix(in srgb,currentColor 16%,transparent);background:transparent;color:inherit;border-radius:999px;padding:.5rem .85rem;font:inherit;cursor:pointer}#groups button[aria-selected=true]{background:var(--accent);border-color:var(--accent);color:white}.grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:.8rem}.widget{grid-column:span var(--span,1);min-height:max(8rem,var(--min,0px));padding:1.2rem;border:1px solid color-mix(in srgb,currentColor 13%,transparent);background:color-mix(in srgb,currentColor 4%,transparent);border-radius:1rem;overflow:hidden}.widget h3{font-size:.78rem;font-weight:600;opacity:.55;margin:0 0 1rem}.metric{font-size:2.2rem;font-weight:720;letter-spacing:-.04em}.metric small{font-size:.9rem;opacity:.55;margin-left:.35rem}.detail{font-size:.82rem;opacity:.55;margin-top:.35rem}.status{display:flex;align-items:center;gap:.55rem;font-weight:650}.status:before{content:'';width:.65rem;height:.65rem;border-radius:50%;background:#8b95a7}.status.good:before{background:#2ed49b}.status.warn:before{background:#ffb84d}.status.bad:before{background:#ff655f}.progress{height:.65rem;background:color-mix(in srgb,currentColor 10%,transparent);border-radius:1rem;overflow:hidden}.progress i{display:block;height:100%;background:var(--accent);border-radius:inherit}.kv{display:grid;gap:.5rem}.kv div{display:flex;justify-content:space-between;gap:1rem;border-bottom:1px solid color-mix(in srgb,currentColor 9%,transparent);padding-bottom:.45rem}.kv span{opacity:.55}.code,.log{font:12px/1.55 ui-monospace,SFMono-Regular,monospace;white-space:pre-wrap;overflow:auto;margin:0;background:color-mix(in srgb,#000 26%,transparent);padding:.8rem;border-radius:.55rem}.chart{height:8rem;display:flex;align-items:end;gap:3px}.chart i{flex:1;min-width:2px;background:color-mix(in srgb,var(--accent) 70%,white);border-radius:3px 3px 0 0}.widget img{max-width:100%;height:auto;border-radius:.6rem}.widget a{color:var(--accent)}footer{padding:2rem 0 3rem;border-top:1px solid color-mix(in srgb,currentColor 12%,transparent);font-size:.8rem;opacity:.45}footer span{font-weight:700}@media(max-width:760px){.hero{min-height:27rem;padding-top:4rem}.grid{grid-template-columns:1fr}.widget{grid-column:1!important}.section-head{align-items:start;flex-direction:column}.section-head p{margin-top:-.6rem}}@media(prefers-color-scheme:light){html[data-theme=system] body{background-color:#f4f7fb;color:#142033}}`

const publicPageJS = `
const sec=document.querySelector('#widgets'),grid=document.querySelector('#grid'),nav=document.querySelector('#groups');
const esc=v=>String(v??''); const el=(tag,cls,text)=>{const n=document.createElement(tag);if(cls)n.className=cls;if(text!==undefined)n.textContent=esc(text);return n};
const groups=[...new Set(W.map(w=>w.Group||'Overview'))]; let active=groups[0];
function render(w){const c=el('article','widget kind-'+w.Kind);c.dataset.widget=w.Key;c.style.setProperty('--span',Math.max(1,Math.min(3,w.Span||1)));c.style.setProperty('--min',Math.max(0,Math.min(800,w.MinHeight||0))+'px');c.append(el('h3','',w.Title));const d=w.Data||{};let n;
switch(w.Kind){case'metric':n=el('div','metric',d.value);if(d.unit)n.append(el('small','',d.unit));if(d.detail)c.append(n,el('div','detail',d.detail));else c.append(n);break;case'status':c.append(el('div','status '+(d.state||'neutral'),d.text));break;case'progress':case'gauge':{const v=Number(d.value)||0,m=Number(d.max)||100;c.append(el('div','metric',v+(d.unit||'')));n=el('div','progress');const i=el('i');i.style.width=Math.max(0,Math.min(100,v/m*100))+'%';n.append(i);c.append(n);break}case'kv':n=el('div','kv');(d.items||[]).forEach(x=>{const r=el('div');r.append(el('span','',x.key),el('strong','',x.value));n.append(r)});c.append(n);break;case'code':n=el('pre','code',d.code);c.append(n);break;case'log':n=el('pre','log',d.text);c.append(n);break;case'image':n=el('img');n.src=d.url;n.alt=d.alt||'';c.append(n);break;case'link':n=el('a','',d.label||d.url);n.href=d.url;n.rel='noopener';c.append(n);break;case'chart':case'line':case'area':case'sparkline':case'heatmap':case'donut':{const pts=d.points||d.cells||d.values||[],max=Math.max(1,...pts.map(x=>Number(x.value)||0));n=el('div','chart');pts.forEach(x=>{const b=el('i');b.style.height=Math.max(4,(Number(x.value)||0)/max*100)+'%';b.title=(x.label?x.label+': ':'')+x.value;n.append(b)});c.append(n);break}case'table':{n=el('div','kv');(d.rows||[]).slice(0,8).forEach(row=>{const r=el('div');r.append(...row.map(x=>el('span','',typeof x==='object'?x.label:x)));n.append(r)});c.append(n);break}default:c.append(el('div','',d.text??JSON.stringify(d)))}return c}
function show(g){active=g;grid.replaceChildren(...W.filter(w=>(w.Group||'Overview')===g).map(render));[...nav.children].forEach(b=>b.setAttribute('aria-selected',String(b.textContent===g)))}
if(W.length){sec.hidden=false;groups.forEach(g=>{const b=el('button','',g);b.onclick=()=>show(g);nav.append(b)});show(active)}
`

// tlsAsk lets a TLS-terminating proxy on this host ask whether to issue a
// certificate for ?domain=. Only loopback peers may ask.
func (h *Handler) tlsAsk(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		http.NotFound(w, r)
		return
	}
	if h.Sites.TLSAllowed(r.URL.Query().Get("domain")) {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// hidden reports whether a path has a dot-segment other than .well-known:
// .git, .env and similar files are never served even when uploaded.
func hidden(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") && seg != ".well-known" {
			return true
		}
	}
	return false
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, root *os.Root, route domain.SiteRoute) {
	clean := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(clean, "/")
	if hidden(rel) {
		h.notFound(w, r, root, route)
		return
	}
	var candidates []string
	switch {
	case rel == "":
		candidates = []string{"index.html"}
	case strings.HasSuffix(r.URL.Path, "/"):
		candidates = []string{rel + "/index.html"}
	default:
		candidates = []string{rel}
		if route.CleanURLs && path.Ext(rel) == "" {
			candidates = append(candidates, rel+".html")
		}
	}
	for _, name := range candidates {
		st, err := root.Stat(name)
		if err != nil {
			continue
		}
		if st.IsDir() {
			// /docs → /docs/ so relative links inside the page resolve.
			if _, err := root.Stat(path.Join(name, "index.html")); err == nil {
				target := clean + "/"
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
			continue
		}
		if h.file(w, r, root, name, http.StatusOK, route.Release) {
			return
		}
	}
	if route.SPA && (path.Ext(rel) == "" || strings.HasSuffix(rel, ".html")) {
		if h.file(w, r, root, "index.html", http.StatusOK, route.Release) {
			return
		}
	}
	h.notFound(w, r, root, route)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request, root *os.Root, route domain.SiteRoute) {
	if h.file(w, r, root, "404.html", http.StatusNotFound, route.Release) {
		return
	}
	page(w, http.StatusNotFound, "Page not found", "There is no page at this address.")
}

// file serves one regular file; false means it does not exist (or is not a
// regular file). Conditional and range requests are handled for 200s.
func (h *Handler) file(w http.ResponseWriter, r *http.Request, root *os.Root, name string, status int, release string) bool {
	f, err := root.Open(name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			h.warn("open file", err)
		}
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ct := mime.TypeByExtension(ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// HTML revalidates on every visit so a new release shows immediately;
	// other assets are cached briefly (build tools fingerprint their names).
	if ext == ".html" || ext == ".htm" || ext == "" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = copyN(w, f, st.Size())
		}
		return true
	}
	w.Header().Set("ETag", `"`+release[:8]+"-"+strconv.FormatInt(st.Size(), 36)+"-"+strconv.FormatInt(st.ModTime().UnixNano(), 36)+`"`)
	http.ServeContent(w, r, name, st.ModTime(), f)
	return true
}

func copyN(w http.ResponseWriter, f *os.File, n int64) (int64, error) {
	buf := make([]byte, 32<<10)
	var total int64
	for total < n {
		m, err := f.Read(buf)
		if m > 0 {
			if _, werr := w.Write(buf[:m]); werr != nil {
				return total, werr
			}
			total += int64(m)
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (h *Handler) warn(msg string, err error) {
	if h.Log != nil {
		h.Log.Warn("sitehost: "+msg, "err", err)
	}
}

// page writes a small self-contained status page.
func page(w http.ResponseWriter, status int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title><style>` +
		`:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;font:16px/1.5 system-ui,sans-serif;` +
		`background:#f4f1ee;color:#1d1a18}@media(prefers-color-scheme:dark){body{background:#0b0908;color:#f3ece7}}` +
		`main{max-width:32rem;padding:2rem}p.code{font:600 .8rem ui-monospace,monospace;letter-spacing:.14em;opacity:.6;margin:0}` +
		`h1{margin:.25rem 0 .5rem;font-size:1.6rem}p{margin:0;opacity:.8}</style></head><body><main>` +
		`<p class="code">` + strconv.Itoa(status) + `</p><h1>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(text) + `</p>` +
		`</main></body></html>`))
}
