package sitehost

import (
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// TraefikPath serves Traefik dynamic configuration (its HTTP provider) with
// one router per verified custom domain, so a reverse proxy that routes some
// names of a domain elsewhere (a catch-all for *.example.com, say) sends each
// verified custom domain to the sites listener as soon as it is verified.
//
// Query parameters (all optional) adapt the routers to the proxy's own
// configuration:
//
//	service     the Traefik service of the sites listener (default sites@file)
//	entrypoint  the TLS entry point (default websecure)
//	web         a plain-HTTP entry point to route as well (default none)
//	middleware  middleware for the plain-HTTP routers, e.g. to-https@file
//	certresolver  certificate resolver for the TLS routers (default none)
//	priority    router priority (default 60)
//
// Only loopback peers and trusted proxies (RIVET_SITES_TRUSTED_PROXIES) may
// read it; anyone else gets 404.
const TraefikPath = "/.well-known/rivetpanel/traefik"

var traefikName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@_.-]{0,63}$`)

type traefikRouter struct {
	EntryPoints []string          `json:"entryPoints"`
	Rule        string            `json:"rule"`
	Priority    int               `json:"priority"`
	Service     string            `json:"service"`
	Middlewares []string          `json:"middlewares,omitempty"`
	TLS         *traefikRouterTLS `json:"tls,omitempty"`
}

type traefikRouterTLS struct {
	CertResolver string `json:"certResolver,omitempty"`
}

func (h *Handler) traefik(w http.ResponseWriter, r *http.Request) {
	if !h.trustedPeer(r) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	param := func(key, def string) (string, bool) {
		v := strings.TrimSpace(q.Get(key))
		if v == "" {
			return def, true
		}
		return v, traefikName.MatchString(v)
	}
	service, ok1 := param("service", "sites@file")
	secure, ok2 := param("entrypoint", "websecure")
	web, ok3 := param("web", "")
	mw, ok4 := param("middleware", "")
	resolver, ok5 := param("certresolver", "")
	priority := 60
	if v := q.Get("priority"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			ok1 = false
		}
		priority = n
	}
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		http.Error(w, "invalid parameter: use letters, digits, @ _ . - (priority 1-10000)", http.StatusBadRequest)
		return
	}
	routers := map[string]traefikRouter{}
	for _, host := range h.Sites.CustomHosts() {
		name := "rivetpanel-" + strings.ReplaceAll(host, ".", "-")
		rule := "Host(`" + host + "`)"
		routers[name] = traefikRouter{EntryPoints: []string{secure}, Rule: rule, Priority: priority, Service: service,
			TLS: &traefikRouterTLS{CertResolver: resolver}}
		if web != "" {
			rt := traefikRouter{EntryPoints: []string{web}, Rule: rule, Priority: priority, Service: service}
			if mw != "" {
				rt.Middlewares = []string{mw}
			}
			routers[name+"-web"] = rt
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"http": map[string]any{"routers": routers}})
}

// trustedPeer reports whether the request comes from this host or from a
// trusted proxy.
func (h *Handler) trustedPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	if a.Unmap().IsLoopback() {
		return true
	}
	return h.Proxy != nil && h.Proxy.trusted(a)
}
