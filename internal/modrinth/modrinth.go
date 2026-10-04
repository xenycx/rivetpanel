// Package modrinth searches Modrinth for Minecraft mods and plugins and
// downloads verified files from its CDN.
package modrinth

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MaxFileBytes bounds one downloaded mod or plugin.
const MaxFileBytes = 256 << 20

// Client talks to the Modrinth API.
type Client struct {
	HTTP *http.Client
	Base string // default https://api.modrinth.com
	// AllowedHosts are the only hosts files are downloaded from.
	AllowedHosts []string
}

// Hit is one search result.
type Hit struct {
	ProjectID   string   `json:"project_id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Downloads   int64    `json:"downloads"`
	IconURL     string   `json:"icon_url"`
	Categories  []string `json:"categories"`
	ProjectType string   `json:"project_type"`
}

// Version is one release of a project.
type Version struct {
	ID            string       `json:"id"`
	ProjectID     string       `json:"project_id"`
	Name          string       `json:"name"`
	VersionNumber string       `json:"version_number"`
	GameVersions  []string     `json:"game_versions"`
	Loaders       []string     `json:"loaders"`
	VersionType   string       `json:"version_type"`
	DatePublished string       `json:"date_published"`
	Files         []File       `json:"files"`
	Dependencies  []Dependency `json:"dependencies"`
}

// File is one downloadable file of a version.
type File struct {
	URL      string            `json:"url"`
	Filename string            `json:"filename"`
	Primary  bool              `json:"primary"`
	Size     int64             `json:"size"`
	Hashes   map[string]string `json:"hashes"`
}

// Dependency links a version to another project.
type Dependency struct {
	ProjectID      string `json:"project_id"`
	VersionID      string `json:"version_id"`
	DependencyType string `json:"dependency_type"` // required | optional | incompatible | embedded
}

var (
	idRe       = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	tokenRe    = regexp.MustCompile(`^[a-z0-9_.-]{1,32}$`)
	filenameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._+()\[\]-]{0,150}\.(jar|zip)$`)
)

// ValidID reports whether s looks like a Modrinth project or version ID/slug.
func ValidID(s string) bool { return idRe.MatchString(s) || tokenRe.MatchString(s) }

// ValidFilename reports whether a file name is safe to write into a server.
func ValidFilename(s string) bool { return filenameRe.MatchString(s) && !strings.Contains(s, "..") }

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return "https://api.modrinth.com"
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	u := c.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "xenycx/rivetpanel (self-hosted panel)")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("contact Modrinth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Modrinth answered %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst)
}

// ErrNotFound means Modrinth does not know the project or version.
var ErrNotFound = errors.New("not found on Modrinth")

func facets(groups ...[]string) string {
	var out [][]string
	for _, g := range groups {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func prefixed(prefix string, vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if tokenRe.MatchString(v) {
			out = append(out, prefix+v)
		}
	}
	return out
}

// Search finds server-compatible projects for the loaders and game version
// ("" = any version).
func (c *Client) Search(ctx context.Context, query string, loaders []string, gameVersion string, offset int) ([]Hit, int, error) {
	if len(query) > 100 {
		query = query[:100]
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("limit", "20")
	q.Set("offset", fmt.Sprint(max(0, min(offset, 1000))))
	q.Set("index", "relevance")
	var versions []string
	if gameVersion != "" && tokenRe.MatchString(gameVersion) {
		versions = []string{"versions:" + gameVersion}
	}
	q.Set("facets", facets(prefixed("categories:", loaders), versions, []string{"server_side:required", "server_side:optional"}))
	var r struct {
		Hits      []Hit `json:"hits"`
		TotalHits int   `json:"total_hits"`
	}
	if err := c.get(ctx, "/v2/search", q, &r); err != nil {
		return nil, 0, err
	}
	return r.Hits, r.TotalHits, nil
}

// Versions lists a project's versions for the loaders and game version.
func (c *Client) Versions(ctx context.Context, project string, loaders []string, gameVersion string) ([]Version, error) {
	if !ValidID(project) {
		return nil, ErrNotFound
	}
	q := url.Values{}
	if l := prefixed("", loaders); len(l) > 0 {
		b, _ := json.Marshal(l)
		q.Set("loaders", string(b))
	}
	if gameVersion != "" && tokenRe.MatchString(gameVersion) {
		b, _ := json.Marshal([]string{gameVersion})
		q.Set("game_versions", string(b))
	}
	var out []Version
	if err := c.get(ctx, "/v2/project/"+url.PathEscape(project)+"/version", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Version loads one version.
func (c *Client) Version(ctx context.Context, id string) (Version, error) {
	var v Version
	if !idRe.MatchString(id) {
		return v, ErrNotFound
	}
	err := c.get(ctx, "/v2/version/"+url.PathEscape(id), nil, &v)
	return v, err
}

// PrimaryFile returns the version's primary (or only) jar.
func (v Version) PrimaryFile() (File, bool) {
	var first *File
	for i := range v.Files {
		f := &v.Files[i]
		if !strings.HasSuffix(strings.ToLower(f.Filename), ".jar") {
			continue
		}
		if f.Primary {
			return *f, true
		}
		if first == nil {
			first = f
		}
	}
	if first != nil {
		return *first, true
	}
	return File{}, false
}

// Fetch streams a file into w, checking host, size and SHA-512.
func (c *Client) Fetch(ctx context.Context, f File, w io.Writer) error {
	u, err := url.Parse(f.URL)
	if err != nil || u.Scheme != "https" {
		return errors.New("the file address is not https")
	}
	hosts := c.AllowedHosts
	if len(hosts) == 0 {
		hosts = []string{"cdn.modrinth.com"}
	}
	allowed := false
	for _, h := range hosts {
		allowed = allowed || u.Host == h
	}
	if !allowed {
		return fmt.Errorf("files are only downloaded from %s", strings.Join(hosts, ", "))
	}
	want := f.Hashes["sha512"]
	if len(want) != 128 {
		return errors.New("the file has no SHA-512 checksum")
	}
	if f.Size > MaxFileBytes {
		return errors.New("the file is larger than 256 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "xenycx/rivetpanel (self-hosted panel)")
	cl := *c.http()
	cl.Timeout = 10 * time.Minute
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download answered %d", resp.StatusCode)
	}
	h := sha512.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, MaxFileBytes+1))
	if err != nil {
		return err
	}
	if n > MaxFileBytes || (f.Size > 0 && n != f.Size) {
		return errors.New("the download has an unexpected size")
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return errors.New("the download does not match its published checksum")
	}
	return nil
}
