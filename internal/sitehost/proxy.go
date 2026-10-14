package sitehost

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// AppResolver finds the address of a site app's container.
type AppResolver interface {
	ContainerIP(ctx context.Context, containerID, network string) (string, error)
}

// Proxy settings of the sites listener for app sites.
type Proxy struct {
	Apps    AppResolver // nil: app sites answer 503
	Network string      // the apps network the containers are reached on
	// Trusted are the peers (the TLS proxy in front of the listener) whose
	// X-Forwarded-* headers and ClientIPHeader are believed.
	Trusted        []netip.Prefix
	ClientIPHeader string // e.g. CF-Connecting-IP; "" = X-Forwarded-For
	MaxBody        int64  // default request body limit (0 = 100 MiB)

	once sync.Once
	rp   *httputil.ReverseProxy
	ips  sync.Map // container ID -> IP (stable for a container's life)
}

const (
	defaultMaxBody = 100 << 20
	appReadTimeout = 10 * time.Minute
)

// dialRetryFor is how long connections refused by a (re)starting app are
// retried.
var dialRetryFor = 10 * time.Second

type proxyCtxKey struct{}

type proxyTarget struct {
	addr, host, proto, client string
	container                 string
	rc                        *http.ResponseController
}

func (p *Proxy) init() {
	p.once.Do(func() {
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
		transport := &http.Transport{
			// An app that is (re)starting refuses connections for a moment:
			// retry briefly instead of failing the visitor's request.
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				deadline := time.Now().Add(dialRetryFor)
				for {
					c, err := dialer.DialContext(ctx, network, addr)
					if err == nil || !errors.Is(err, syscall.ECONNREFUSED) || time.Now().After(deadline) {
						return c, err
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(250 * time.Millisecond):
					}
				}
			},
			MaxIdleConns: 256, MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second,
			ResponseHeaderTimeout: 5 * time.Minute, ExpectContinueTimeout: time.Second,
			DisableCompression: true, // pass the app's encoding through untouched
		}
		p.rp = &httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(pr *httputil.ProxyRequest) {
				t := pr.In.Context().Value(proxyCtxKey{}).(*proxyTarget)
				pr.SetURL(&url.URL{Scheme: "http", Host: t.addr})
				pr.Out.Host = t.host // the app sees the address the visitor used
				// Rewrite already dropped X-Forwarded-*; these are set from what
				// this listener verified, never copied from the visitor.
				for _, h := range []string{"Forwarded", "X-Real-Ip", "X-Forwarded-Port", "X-Forwarded-Server"} {
					pr.Out.Header.Del(h)
				}
				for name := range pr.Out.Header {
					if strings.HasPrefix(name, "X-Rivet-") {
						pr.Out.Header.Del(name)
					}
				}
				pr.Out.Header.Set("X-Forwarded-For", t.client)
				pr.Out.Header.Set("X-Real-IP", t.client)
				pr.Out.Header.Set("X-Forwarded-Host", t.host)
				pr.Out.Header.Set("X-Forwarded-Proto", t.proto)
			},
			ModifyResponse: func(res *http.Response) error {
				// Server-sent events stay open: lift the write deadline.
				if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
					if t, ok := res.Request.Context().Value(proxyCtxKey{}).(*proxyTarget); ok {
						_ = t.rc.SetWriteDeadline(time.Time{})
					}
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				t := r.Context().Value(proxyCtxKey{}).(*proxyTarget)
				p.ips.Delete(t.container) // look the address up again next time
				var mbe *http.MaxBytesError
				switch {
				case errors.As(err, &mbe):
					page(w, http.StatusRequestEntityTooLarge, "Request too large", "The request body is larger than this site accepts.")
				case errors.Is(err, context.Canceled):
					w.WriteHeader(499) // the visitor went away; nobody reads this
				default:
					page(w, http.StatusBadGateway, "App not responding", "This site's app did not answer. It may be starting or have crashed; try again shortly.")
				}
			},
		}
	})
}

// serveApp proxies a request to a site's running app.
func (h *Handler) serveApp(w http.ResponseWriter, r *http.Request, route domain.SiteRoute) {
	p := h.Proxy
	if p == nil || p.Apps == nil {
		page(w, http.StatusServiceUnavailable, "Site unavailable", "Apps cannot be served by this server right now.")
		return
	}
	switch {
	case route.AppState == "running" && route.ContainerID != "":
	case route.AppState == "building" || route.AppState == "starting":
		deploying(w, r)
		return
	default:
		page(w, http.StatusServiceUnavailable, "App is not running", "This site's app is stopped. Its owner can start it from the panel.")
		return
	}
	p.init()
	ip, err := p.ip(r.Context(), route.ContainerID)
	if err != nil {
		if h.Log != nil {
			h.Log.Warn("sitehost: find app address", "site", route.SiteID, "err", err)
		}
		deploying(w, r)
		return
	}
	limit := route.MaxBody
	if limit <= 0 {
		limit = p.MaxBody
	}
	if limit <= 0 {
		limit = defaultMaxBody
	}
	if r.ContentLength > limit {
		page(w, http.StatusRequestEntityTooLarge, "Request too large", "The request body is larger than this site accepts.")
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	rc := http.NewResponseController(w)
	if isUpgrade(r) {
		// WebSockets live as long as the visitor keeps them open.
		_ = rc.SetReadDeadline(time.Time{})
		_ = rc.SetWriteDeadline(time.Time{})
	} else {
		_ = rc.SetReadDeadline(time.Now().Add(appReadTimeout)) // uploads over slow links
	}
	port := route.AppPort
	if port <= 0 {
		port = domain.DefaultAppPort
	}
	t := &proxyTarget{addr: net.JoinHostPort(ip, strconv.Itoa(port)), host: r.Host, container: route.ContainerID, rc: rc}
	t.client, t.proto = p.forwarded(r)
	p.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), proxyCtxKey{}, t)))
}

func (p *Proxy) ip(ctx context.Context, container string) (string, error) {
	if v, ok := p.ips.Load(container); ok {
		return v.(string), nil
	}
	ip, err := p.Apps.ContainerIP(ctx, container, p.Network)
	if err != nil {
		return "", err
	}
	p.ips.Store(container, ip)
	return ip, nil
}

// Forget drops cached addresses (for tests and network changes).
func (p *Proxy) Forget() { p.ips.Clear() }

func isUpgrade(r *http.Request) bool {
	for _, v := range r.Header.Values("Connection") {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), "upgrade") {
				return r.Header.Get("Upgrade") != ""
			}
		}
	}
	return false
}

// forwarded returns the visitor's address and scheme. Headers are believed
// only from a trusted peer (the TLS proxy in front of this listener).
func (p *Proxy) forwarded(r *http.Request) (client, proto string) {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	proto = "http"
	if r.TLS != nil {
		proto = "https"
	}
	pa, err := netip.ParseAddr(peer)
	if err != nil || !p.trusted(pa) {
		return peer, proto
	}
	if v := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); v == "https" || v == "http" {
		proto = v
	}
	if p.ClientIPHeader != "" {
		if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(p.ClientIPHeader))); err == nil {
			return a.Unmap().String(), proto
		}
	}
	// The right-most address that is not one of our own proxies.
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			break
		}
		if !p.trusted(a) {
			return a.Unmap().String(), proto
		}
	}
	return peer, proto
}

func (p *Proxy) trusted(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsLoopback() {
		return true
	}
	for _, pf := range p.Trusted {
		if pf.Contains(a) {
			return true
		}
	}
	return false
}

// deploying answers while an app builds or starts: a page that reloads
// itself for browsers, and Retry-After for everything else.
func deploying(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "5")
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Refresh", "5")
	}
	page(w, http.StatusServiceUnavailable, "Deploying…", "This site's app is starting. This page reloads by itself.")
}
