package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

type SearchConfig struct {
	BaseURL                         string
	Keys                            []string
	Results                         int
	Language, Categories, TimeRange string
	SafeSearch                      int
}
type SearchResult struct {
	Title, URL, Snippet, Published string
	RetrievedMS                    int64
}
type SearchResponse struct {
	Answer  string
	Results []SearchResult
}

// Research performs web search and public page fetches. Address checks run
// when each connection is dialed (after DNS resolution, for every redirect
// hop), so DNS rebinding cannot swap in an internal address after validation.
type Research struct {
	// HTTP replaces both clients entirely; tests only. It bypasses the
	// connect-time address policy.
	HTTP     *http.Client
	Resolver *net.Resolver
	next     atomic.Uint64
}

func (r *Research) resolver() *net.Resolver {
	if r != nil && r.Resolver != nil {
		return r.Resolver
	}
	return net.DefaultResolver
}

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",      // "this" network
		"100.64.0.0/10",  // carrier-grade NAT (also Tailscale and some cloud metadata)
		"192.0.0.0/24",   // IETF protocol assignments
		"198.18.0.0/15",  // benchmarking
		"240.0.0.0/4",    // reserved, broadcast
		"64:ff9b::/96",   // NAT64 can reach any IPv4 address
		"64:ff9b:1::/48", // local-use NAT64
		"2002::/16",      // 6to4 embeds IPv4 addresses
		"fec0::/10",      // deprecated site-local
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// metadataIP reports cloud instance-metadata endpoints, which no research
// connection (search included) may reach.
func metadataIP(ip net.IP) bool {
	return ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("100.100.100.200")) || ip.Equal(net.ParseIP("fd00:ec2::254"))
}

// PublicIP reports whether ip is a globally routable unicast address.
func PublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || metadataIP(ip) {
		return false
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var errBlockedAddress = errors.New("private, local, multicast, and metadata addresses are blocked")

// dialControl runs on the resolved address of every outgoing connection.
func dialControl(allowPrivate bool) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return errBlockedAddress
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return errBlockedAddress
		}
		if allowPrivate {
			if ip.IsUnspecified() || ip.IsMulticast() || metadataIP(ip) {
				return errBlockedAddress
			}
			return nil
		}
		if !PublicIP(ip) {
			return errBlockedAddress
		}
		return nil
	}
}

// client builds a client whose dialer enforces the address policy at connect
// time. Public page fetches are always direct: a proxy would resolve the name
// itself and hide the real destination. The administrator-trusted search
// origin may go through the environment's HTTP(S)_PROXY.
func (r *Research) client(allowPrivate, useProxy bool, redirect func(*http.Request, []*http.Request) error) *http.Client {
	if r != nil && r.HTTP != nil {
		c := *r.HTTP
		c.CheckRedirect = redirect
		return &c
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Resolver: r.resolver(), Control: dialControl(allowPrivate)}
	var proxy func(*http.Request) (*url.URL, error)
	if useProxy {
		proxy = http.ProxyFromEnvironment
	}
	t := &http.Transport{Proxy: proxy, DialContext: d.DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 10, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	return &http.Client{Timeout: 20 * time.Second, Transport: t, CheckRedirect: redirect}
}

func syntaxURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("only HTTP(S) URLs without credentials are allowed")
	}
	return u, nil
}

// ValidateURL is an early, friendly check that a URL resolves only to public
// addresses. It is not the security boundary: the dialer re-checks the
// address actually connected to.
func (r *Research) ValidateURL(ctx context.Context, raw string) (*url.URL, error) {
	u, e := syntaxURL(raw)
	if e != nil {
		return nil, errors.New("only public HTTP(S) URLs without credentials are allowed")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if !PublicIP(ip) {
			return nil, errBlockedAddress
		}
		return u, nil
	}
	ips, e := r.resolver().LookupIP(ctx, "ip", u.Hostname())
	if e != nil || len(ips) == 0 {
		return nil, errors.New("the host could not be resolved")
	}
	for _, ip := range ips {
		if !PublicIP(ip) {
			return nil, errBlockedAddress
		}
	}
	return u, nil
}

func (r *Research) Search(ctx context.Context, cfg SearchConfig, q string) (SearchResponse, error) {
	if strings.TrimSpace(q) == "" || len(q) > 500 {
		return SearchResponse{}, errors.New("search query is invalid")
	}
	// The administrator-configured search origin may be private (a
	// self-hosted SearxNG); result pages are still fetched public-only.
	base, e := syntaxURL(cfg.BaseURL)
	if e != nil {
		return SearchResponse{}, e
	}
	// Keys travel in X-API-Key, which Go would forward on a cross-origin
	// redirect, so the search client never leaves the configured origin.
	client := r.client(true, true, func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
			return errors.New("the search service redirected to another origin")
		}
		return nil
	})
	defer client.CloseIdleConnections()
	u := base.ResolveReference(&url.URL{Path: strings.TrimRight(base.Path, "/") + "/search"})
	v := u.Query()
	v.Set("q", q)
	v.Set("format", "json")
	if cfg.Language != "" {
		v.Set("language", cfg.Language)
	}
	if cfg.Categories != "" {
		v.Set("categories", cfg.Categories)
	}
	if cfg.TimeRange != "" {
		v.Set("time_range", cfg.TimeRange)
	}
	v.Set("safesearch", fmt.Sprint(cfg.SafeSearch))
	u.RawQuery = v.Encode()
	limit := cfg.Results
	if limit < 1 || limit > 10 {
		limit = 5
	}
	var last error
	attempts := len(cfg.Keys)
	if attempts == 0 {
		attempts = 1
	}
	start := int(r.next.Add(1) - 1)
	for i := 0; i < attempts; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		req.Header.Set("Accept", "application/json")
		if len(cfg.Keys) > 0 {
			req.Header.Set("X-API-Key", cfg.Keys[(start+i)%len(cfg.Keys)])
		}
		resp, e := client.Do(req)
		if e != nil {
			return SearchResponse{}, e
		}
		if resp.StatusCode == 429 {
			resp.Body.Close()
			last = errors.New("search service rate limited every configured key")
			continue
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			resp.Body.Close()
			last = fmt.Errorf("search service refused the request (%d); check the search API keys", resp.StatusCode)
			continue
		}
		if resp.StatusCode/100 != 2 {
			resp.Body.Close()
			return SearchResponse{}, fmt.Errorf("search service returned %d", resp.StatusCode)
		}
		var raw struct {
			Answers []string                                              `json:"answers"`
			Results []struct{ Title, URL, Content, PublishedDate string } `json:"results"`
		}
		e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&raw)
		resp.Body.Close()
		if e != nil {
			return SearchResponse{}, e
		}
		out := SearchResponse{}
		if len(raw.Answers) > 0 {
			out.Answer = raw.Answers[0]
		}
		now := time.Now().UnixMilli()
		for _, x := range raw.Results {
			if len(out.Results) >= limit {
				break
			}
			if _, e := r.ValidateURL(ctx, x.URL); e != nil {
				continue
			}
			out.Results = append(out.Results, SearchResult{Title: x.Title, URL: x.URL, Snippet: clip(x.Content, 1200), Published: x.PublishedDate, RetrievedMS: now})
		}
		return out, nil
	}
	return SearchResponse{}, last
}

// Fetch extracts visible text from a public page. The URL and every redirect
// are checked up front for a clear error; the dialer then re-checks the
// address of each actual connection, so redirects and DNS rebinding cannot
// reach internal hosts.
func (r *Research) Fetch(ctx context.Context, raw string) (title, text string, err error) {
	if _, err = r.ValidateURL(ctx, raw); err != nil {
		return
	}
	client := r.client(false, false, func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		_, e := r.ValidateURL(req.Context(), req.URL.String())
		return e
	})
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	req.Header.Set("Accept", "text/html,application/json,text/plain;q=0.9")
	req.Header.Set("User-Agent", "RivetPanel-Research/1.0")
	resp, e := client.Do(req)
	if e != nil {
		if errors.Is(e, errBlockedAddress) {
			return "", "", errBlockedAddress
		}
		return "", "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("page returned %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(ct, "text/") && !strings.Contains(ct, "json") && !strings.Contains(ct, "html") {
		return "", "", errors.New("binary pages are not supported")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if e != nil {
		return "", "", e
	}
	if len(b) > 2<<20 {
		return "", "", errors.New("page is too large")
	}
	if strings.Contains(ct, "html") {
		title, text = htmlText(b)
	} else {
		text = string(b)
	}
	return clip(title, 300), clip(text, 20000), nil
}

func htmlText(b []byte) (string, string) {
	n, e := html.Parse(strings.NewReader(string(b)))
	if e != nil {
		return "", string(b)
	}
	var title string
	var parts []string
	var walk func(*html.Node, bool)
	walk = func(x *html.Node, blocked bool) {
		if x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "script", "style", "nav", "form", "noscript", "svg":
				blocked = true
			case "title":
				if x.FirstChild != nil {
					title = x.FirstChild.Data
				}
			}
		}
		if x.Type == html.TextNode && !blocked {
			v := strings.Join(strings.Fields(x.Data), " ")
			if v != "" {
				parts = append(parts, v)
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c, blocked)
		}
	}
	walk(n, false)
	return title, strings.Join(parts, "\n")
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
