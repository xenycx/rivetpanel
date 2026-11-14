package service

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// Verification lookups ask the domain's own (authoritative) nameservers, not
// the server's caching resolver: a record the user has just added or edited
// is checked as it is now, not as a cache last saw it (a resolver may keep an
// old value for the record's whole TTL, often five minutes). When the
// nameservers cannot be found or do not answer, the system resolver is used.

// authoritativeTXT resolves TXT records through the nameservers of a zone.
type authoritativeTXT struct {
	servers []string // host:53
}

func (a authoritativeTXT) LookupTXT(ctx context.Context, name string) ([]string, error) {
	var last error
	for _, srv := range a.servers {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, network, srv)
		}}
		recs, err := r.LookupTXT(ctx, name)
		var dnsErr *net.DNSError
		if err == nil || (errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
			return recs, err
		}
		last = err
	}
	return nil, last
}

// fallbackTXT asks primary and, when it fails for a reason other than "no
// such record", secondary.
type fallbackTXT struct{ primary, secondary TXTResolver }

func (f fallbackTXT) LookupTXT(ctx context.Context, name string) ([]string, error) {
	recs, err := f.primary.LookupTXT(ctx, name)
	var dnsErr *net.DNSError
	if err == nil || (errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
		return recs, err
	}
	return f.secondary.LookupTXT(ctx, name)
}

// txtResolverFor returns the resolver for verification records under zone:
// a configured Resolver (tests) as is, else the zone's nameservers with the
// system resolver as fallback.
func (s *SiteService) txtResolverFor(ctx context.Context, zone string) TXTResolver {
	if s.Resolver != nil {
		return s.Resolver
	}
	nsCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ns, err := net.DefaultResolver.LookupNS(nsCtx, zone)
	if err != nil || len(ns) == 0 {
		return net.DefaultResolver
	}
	servers := make([]string, 0, len(ns))
	for _, n := range ns {
		if h := strings.TrimSuffix(n.Host, "."); h != "" {
			servers = append(servers, net.JoinHostPort(h, "53"))
		}
	}
	if len(servers) > 3 {
		servers = servers[:3]
	}
	return fallbackTXT{authoritativeTXT{servers}, net.DefaultResolver}
}

// mismatchMessage explains a TXT record that holds other values than want.
func mismatchMessage(at, want string, found []string) string {
	shown := make([]string, 0, 2)
	for _, v := range found {
		if v = strings.TrimSpace(v); v != "" && len(shown) < 2 {
			if len(v) > 80 {
				v = v[:80] + "…"
			}
			shown = append(shown, "“"+v+"”")
		}
	}
	if len(shown) == 0 {
		return "The TXT record at " + at + " is empty. Expected “" + want + "”."
	}
	return "The TXT record at " + at + " holds a different value. Found " + strings.Join(shown, " and ") + "; expected “" + want +
		"”. Copy the value again; if you just changed it, your DNS provider may still serve the old one for a few minutes."
}
