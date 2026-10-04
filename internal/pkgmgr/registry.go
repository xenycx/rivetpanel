package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/module"
)

// Result is a registry search hit.
type Result struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// Registry queries public package registries. Base URLs are fixed to the
// official hosts (overridable only in tests), so no user-supplied URL is ever
// fetched.
type Registry struct {
	HTTP                       *http.Client
	NPM, Crates, PyPI, GoProxy string
	mu                         sync.Mutex
	cache                      map[string]cached
}

type cached struct {
	at  time.Time
	val []Result
}

const (
	cacheTTL   = 5 * time.Minute
	cacheMax   = 500
	maxRespond = 2 << 20
)

func (r *Registry) hc() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 8 * time.Second}
}

func def2(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

var errNotFound = errors.New("package not found")

func (r *Registry) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "rivetpanel (package manager)") // crates.io requires one
	res, err := r.hc().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone {
		return errNotFound
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("registry returned status %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, maxRespond)).Decode(out)
}

// Search finds packages. npm and crates.io support free-text search; PyPI and
// the Go proxy have no search API, so they resolve an exact name.
func (r *Registry) Search(ctx context.Context, ecosystem, q string) ([]Result, error) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 100 || strings.ContainsAny(q, "\x00\r\n") {
		return nil, errors.New("enter a search term of up to 100 characters")
	}
	key := ecosystem + "\x00" + strings.ToLower(q)
	r.mu.Lock()
	if c, ok := r.cache[key]; ok && time.Since(c.at) < cacheTTL {
		r.mu.Unlock()
		return c.val, nil
	}
	r.mu.Unlock()

	var res []Result
	var err error
	switch ecosystem {
	case "npm":
		var o struct {
			Objects []struct {
				Package struct{ Name, Version, Description string }
			}
		}
		if err = r.get(ctx, def2(r.NPM, "https://registry.npmjs.org")+"/-/v1/search?size=10&text="+url.QueryEscape(q), &o); err == nil {
			for _, x := range o.Objects {
				res = append(res, Result{x.Package.Name, x.Package.Version, x.Package.Description})
			}
		}
	case "cargo":
		var o struct {
			Crates []struct {
				Name        string
				MaxVersion  string `json:"max_version"`
				Description string
			}
		}
		if err = r.get(ctx, def2(r.Crates, "https://crates.io")+"/api/v1/crates?per_page=10&q="+url.QueryEscape(q), &o); err == nil {
			for _, x := range o.Crates {
				res = append(res, Result{x.Name, x.MaxVersion, x.Description})
			}
		}
	case "pip":
		if !pipName.MatchString(q) {
			return nil, errors.New("PyPI has no search API: enter the exact package name")
		}
		var o struct {
			Info struct{ Name, Version, Summary string }
		}
		if err = r.get(ctx, def2(r.PyPI, "https://pypi.org")+"/pypi/"+url.PathEscape(q)+"/json", &o); err == nil {
			res = []Result{{o.Info.Name, o.Info.Version, o.Info.Summary}}
		}
	case "gomod":
		if !goPath.MatchString(q) {
			return nil, errors.New("the Go proxy has no search API: enter the exact module path")
		}
		esc, e := module.EscapePath(q)
		if e != nil {
			return nil, errors.New("invalid module path")
		}
		var o struct{ Version string }
		if err = r.get(ctx, def2(r.GoProxy, "https://proxy.golang.org")+"/"+esc+"/@latest", &o); err == nil {
			res = []Result{{q, o.Version, ""}}
		}
	default:
		return nil, ErrUnsupported
	}
	if errors.Is(err, errNotFound) {
		return []Result{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry lookup failed: %w", err)
	}
	if res == nil {
		res = []Result{}
	}
	r.mu.Lock()
	if r.cache == nil || len(r.cache) >= cacheMax {
		r.cache = map[string]cached{}
	}
	r.cache[key] = cached{time.Now(), res}
	r.mu.Unlock()
	return res, nil
}

// Latest returns the newest version of one exact package.
func (r *Registry) Latest(ctx context.Context, ecosystem, name string) (string, error) {
	var res []Result
	var err error
	switch ecosystem {
	case "npm":
		if !npmName.MatchString(name) {
			return "", errors.New("invalid package name")
		}
		var o struct{ Version string }
		esc := strings.ReplaceAll(name, "/", "%2F")
		if err = r.get(ctx, def2(r.NPM, "https://registry.npmjs.org")+"/"+esc+"/latest", &o); err == nil {
			return o.Version, nil
		}
	case "cargo":
		if !cargoName.MatchString(name) {
			return "", errors.New("invalid package name")
		}
		var o struct {
			Crate struct {
				MaxStable string `json:"max_stable_version"`
				Max       string `json:"max_version"`
			}
		}
		if err = r.get(ctx, def2(r.Crates, "https://crates.io")+"/api/v1/crates/"+url.PathEscape(name), &o); err == nil {
			if o.Crate.MaxStable != "" {
				return o.Crate.MaxStable, nil
			}
			return o.Crate.Max, nil
		}
	default:
		res, err = r.Search(ctx, ecosystem, name)
		if err == nil && len(res) > 0 {
			return res[0].Version, nil
		}
		if err == nil {
			err = errNotFound
		}
	}
	if errors.Is(err, errNotFound) {
		return "", errors.New("package not found")
	}
	return "", err
}
