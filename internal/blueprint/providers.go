package blueprint

import (
	"context"
	"crypto/md5"  //nolint:gosec // integrity check where the provider publishes only MD5; TLS provides authenticity
	"crypto/sha1" //nolint:gosec // Mojang and Maven publish SHA-1 checksums
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Artifact is a resolved download.
type Artifact struct {
	URL      string
	Name     string
	Version  string // resolved version ("latest" becomes a real one)
	Size     int64  // 0 = unknown
	SHA256   string // at most one checksum is set
	SHA1     string
	MD5      string
	JavaHint int // Java major version required, when the provider knows it
}

// Version is one entry of a version list.
type Version struct {
	ID     string `json:"id"`
	Stable bool   `json:"stable"`
}

// Endpoints are the provider base URLs (overridable in tests).
type Endpoints struct {
	Mojang, Paper, Purpur, Fabric, Forge, ForgeMaven, NeoForge string
}

// DefaultEndpoints are the public provider APIs.
var DefaultEndpoints = Endpoints{
	Mojang:     "https://piston-meta.mojang.com",
	Paper:      "https://fill.papermc.io",
	Purpur:     "https://api.purpurmc.org",
	Fabric:     "https://meta.fabricmc.net",
	Forge:      "https://files.minecraftforge.net",
	ForgeMaven: "https://maven.minecraftforge.net",
	NeoForge:   "https://maven.neoforged.net",
}

// MaxDownloadBytes bounds a single server download.
const MaxDownloadBytes = 1 << 30

// Providers resolves versions and downloads from first-party sources. Version
// lists are cached for ten minutes.
type Providers struct {
	HTTP      *http.Client
	Endpoints Endpoints

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at   time.Time
	data any
}

type provider interface {
	versions(ctx context.Context, p *Providers, project string) ([]Version, error)
	resolve(ctx context.Context, p *Providers, project, version, build string) (Artifact, error)
	// gameVersion maps a provider version to a Minecraft version, for Java selection.
	gameVersion(project, version string) string
}

var providers = map[string]provider{
	"minecraft-vanilla": vanilla{},
	"papermc":           paperMC{},
	"purpur":            purpur{},
	"fabric":            fabric{},
	"forge":             forge{},
	"neoforge":          neoForge{},
}

// ProviderNames lists the supported providers.
func ProviderNames() []string {
	out := make([]string, 0, len(providers))
	for n := range providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (p *Providers) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (p *Providers) ep() Endpoints {
	if p.Endpoints.Mojang == "" {
		return DefaultEndpoints
	}
	return p.Endpoints
}

func (p *Providers) getJSON(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "RivetPanel (+https://github.com/xenycx/rivetpanel)")
	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("contact %s: %w", hostOf(u), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrUnknownVersion
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", hostOf(u), resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst)
}

func (p *Providers) getText(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %d", hostOf(u), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.TrimSpace(string(b)), err
}

func hostOf(u string) string {
	if pu, err := url.Parse(u); err == nil {
		return pu.Host
	}
	return "provider"
}

func (p *Providers) memo(key string, fn func() (any, error)) (any, error) {
	p.mu.Lock()
	if c, ok := p.cache[key]; ok && time.Since(c.at) < 10*time.Minute {
		p.mu.Unlock()
		return c.data, nil
	}
	p.mu.Unlock()
	v, err := fn()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]cached{}
	}
	p.cache[key] = cached{time.Now(), v}
	p.mu.Unlock()
	return v, nil
}

// ErrUnknownVersion means the provider does not know the requested version.
var ErrUnknownVersion = errors.New("the provider does not offer that version")

// Versions lists a provider's versions, newest first.
func (p *Providers) Versions(ctx context.Context, name, project string) ([]Version, error) {
	pr, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	v, err := p.memo("v|"+name+"|"+project, func() (any, error) { return pr.versions(ctx, p, project) })
	if err != nil {
		return nil, err
	}
	return v.([]Version), nil
}

// Resolve finds the download for version ("latest" = newest stable).
func (p *Providers) Resolve(ctx context.Context, d Download, vars map[string]string) (Artifact, error) {
	pr, ok := providers[d.Name]
	if !ok {
		return Artifact{}, fmt.Errorf("unknown provider %q", d.Name)
	}
	version := strings.TrimSpace(Render(d.Version, vars))
	build := strings.TrimSpace(Render(d.Build, vars))
	if version == "" {
		version = "latest"
	}
	if !versionRe.MatchString(version) || (build != "" && build != "latest" && !versionRe.MatchString(build)) {
		return Artifact{}, ErrUnknownVersion
	}
	if build == "latest" {
		build = ""
	}
	return pr.resolve(ctx, p, d.Project, version, build)
}

var versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

// JavaFor returns the Java major version a Minecraft release needs, or 0
// when it is unknown. provider/project translate provider-specific versions.
func (p *Providers) JavaFor(ctx context.Context, providerName, project, version string) int {
	if pr, ok := providers[providerName]; ok {
		version = pr.gameVersion(project, version)
	}
	if version == "" || version == "latest" {
		return 0
	}
	v, err := p.memo("j|"+version, func() (any, error) {
		m, err := vanillaManifest(ctx, p)
		if err != nil {
			return 0, err
		}
		for _, e := range m.Versions {
			if e.ID == version {
				var meta struct {
					JavaVersion struct {
						Major int `json:"majorVersion"`
					} `json:"javaVersion"`
				}
				if err := p.getJSON(ctx, e.URL, &meta); err != nil {
					return 0, err
				}
				return meta.JavaVersion.Major, nil
			}
		}
		return 0, nil
	})
	if err != nil {
		return 0
	}
	return v.(int)
}

// Fetch streams an artifact into w, enforcing the size cap and checksum.
func (p *Providers) Fetch(ctx context.Context, a Artifact, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "RivetPanel (+https://github.com/xenycx/rivetpanel)")
	cl := *p.client()
	cl.Timeout = 30 * time.Minute
	resp, err := cl.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download from %s: %w", hostOf(a.URL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download from %s answered %d", hostOf(a.URL), resp.StatusCode)
	}
	if resp.ContentLength > MaxDownloadBytes {
		return 0, errors.New("download is larger than 1 GiB")
	}
	var h hash.Hash
	var want string
	switch {
	case a.SHA256 != "":
		h, want = sha256.New(), a.SHA256
	case a.SHA1 != "":
		h, want = sha1.New(), a.SHA1 //nolint:gosec
	case a.MD5 != "":
		h, want = md5.New(), a.MD5 //nolint:gosec
	}
	dst := w
	if h != nil {
		dst = io.MultiWriter(w, h)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, MaxDownloadBytes+1))
	if err != nil {
		return n, err
	}
	if n > MaxDownloadBytes {
		return n, errors.New("download is larger than 1 GiB")
	}
	if a.Size > 0 && n != a.Size {
		return n, fmt.Errorf("download is %d bytes, expected %d", n, a.Size)
	}
	if h != nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return n, errors.New("download does not match its published checksum")
	}
	return n, nil
}

// compareVersions orders dotted versions numerically ("1.21.10" > "1.21.9").
func compareVersions(a, b string) int {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	}
	pa, pb := split(a), split(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		if i >= len(pa) || i >= len(pb) {
			// "1.21" vs "1.21-rc1": a pre-release suffix sorts before the release.
			longer, sign := pb, -1
			if i >= len(pb) {
				longer, sign = pa, 1
			}
			if _, err := strconv.Atoi(longer[i]); err != nil {
				return -sign
			}
			return sign
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil && na != nb:
			if na < nb {
				return -1
			}
			return 1
		case ea == nil && eb != nil:
			return 1 // 1.21 > 1.21-rc1
		case ea != nil && eb == nil:
			return -1
		case pa[i] != pb[i]:
			return strings.Compare(pa[i], pb[i])
		}
	}
	return 0
}

func unstable(v string) bool {
	l := strings.ToLower(v)
	for _, s := range []string{"pre", "rc", "snapshot", "beta", "alpha", "experimental"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func newestFirst(ids []string, stableOnly bool) []Version {
	out := make([]Version, 0, len(ids))
	for _, id := range ids {
		st := !unstable(id)
		if stableOnly && !st {
			continue
		}
		out = append(out, Version{ID: id, Stable: st})
	}
	sort.SliceStable(out, func(i, j int) bool { return compareVersions(out[i].ID, out[j].ID) > 0 })
	return out
}

func latestStable(vs []Version) (string, error) {
	for _, v := range vs {
		if v.Stable {
			return v.ID, nil
		}
	}
	return "", ErrUnknownVersion
}

// --- Mojang (vanilla) ---

type manifest struct {
	Latest struct {
		Release string `json:"release"`
	} `json:"latest"`
	Versions []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"versions"`
}

func vanillaManifest(ctx context.Context, p *Providers) (manifest, error) {
	v, err := p.memo("mojang-manifest", func() (any, error) {
		var m manifest
		err := p.getJSON(ctx, p.ep().Mojang+"/mc/game/version_manifest_v2.json", &m)
		return m, err
	})
	if err != nil {
		return manifest{}, err
	}
	return v.(manifest), nil
}

type vanilla struct{}

func (vanilla) versions(ctx context.Context, p *Providers, _ string) ([]Version, error) {
	m, err := vanillaManifest(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []Version
	for _, e := range m.Versions { // the manifest is newest first
		if e.Type == "release" {
			out = append(out, Version{ID: e.ID, Stable: true})
		}
	}
	return out, nil
}

func (vanilla) resolve(ctx context.Context, p *Providers, _, version, _ string) (Artifact, error) {
	m, err := vanillaManifest(ctx, p)
	if err != nil {
		return Artifact{}, err
	}
	if version == "latest" {
		version = m.Latest.Release
	}
	for _, e := range m.Versions {
		if e.ID != version {
			continue
		}
		var meta struct {
			JavaVersion struct {
				Major int `json:"majorVersion"`
			} `json:"javaVersion"`
			Downloads struct {
				Server *struct {
					SHA1 string `json:"sha1"`
					Size int64  `json:"size"`
					URL  string `json:"url"`
				} `json:"server"`
			} `json:"downloads"`
		}
		if err := p.getJSON(ctx, e.URL, &meta); err != nil {
			return Artifact{}, err
		}
		if meta.Downloads.Server == nil {
			return Artifact{}, fmt.Errorf("Minecraft %s has no dedicated server download", version)
		}
		s := meta.Downloads.Server
		return Artifact{URL: s.URL, Name: "server.jar", Version: version, Size: s.Size, SHA1: s.SHA1, JavaHint: meta.JavaVersion.Major}, nil
	}
	return Artifact{}, ErrUnknownVersion
}

func (vanilla) gameVersion(_, v string) string { return v }

// --- PaperMC (paper, folia, velocity, waterfall) through the Fill v3 API ---

type paperMC struct{}

var paperProjects = map[string]bool{"paper": true, "folia": true, "velocity": true, "waterfall": true}

func (paperMC) versions(ctx context.Context, p *Providers, project string) ([]Version, error) {
	if !paperProjects[project] {
		return nil, fmt.Errorf("unknown PaperMC project %q", project)
	}
	var r struct {
		Versions map[string][]string `json:"versions"`
	}
	if err := p.getJSON(ctx, p.ep().Paper+"/v3/projects/"+project, &r); err != nil {
		return nil, err
	}
	var ids []string
	for _, group := range r.Versions {
		ids = append(ids, group...)
	}
	return newestFirst(ids, true), nil
}

func (pm paperMC) resolve(ctx context.Context, p *Providers, project, version, build string) (Artifact, error) {
	if !paperProjects[project] {
		return Artifact{}, fmt.Errorf("unknown PaperMC project %q", project)
	}
	if version == "latest" {
		vs, err := pm.versions(ctx, p, project)
		if err != nil {
			return Artifact{}, err
		}
		if version, err = latestStable(vs); err != nil {
			return Artifact{}, err
		}
	}
	b := "latest"
	if build != "" {
		if _, err := strconv.Atoi(build); err != nil {
			return Artifact{}, ErrUnknownVersion
		}
		b = build
	}
	var r struct {
		Downloads map[string]struct {
			Name      string `json:"name"`
			Size      int64  `json:"size"`
			URL       string `json:"url"`
			Checksums struct {
				SHA256 string `json:"sha256"`
			} `json:"checksums"`
		} `json:"downloads"`
	}
	u := p.ep().Paper + "/v3/projects/" + project + "/versions/" + url.PathEscape(version) + "/builds/" + b
	if err := p.getJSON(ctx, u, &r); err != nil {
		return Artifact{}, err
	}
	d, ok := r.Downloads["server:default"]
	if !ok || d.URL == "" {
		return Artifact{}, fmt.Errorf("%s %s has no server download", project, version)
	}
	return Artifact{URL: d.URL, Name: d.Name, Version: version, Size: d.Size, SHA256: d.Checksums.SHA256}, nil
}

func (paperMC) gameVersion(project, v string) string {
	if project == "velocity" || project == "waterfall" {
		return "" // proxies do not follow Minecraft versions
	}
	return v
}

// --- Purpur ---

type purpur struct{}

func (purpur) versions(ctx context.Context, p *Providers, _ string) ([]Version, error) {
	var r struct {
		Versions []string `json:"versions"`
	}
	if err := p.getJSON(ctx, p.ep().Purpur+"/v2/purpur", &r); err != nil {
		return nil, err
	}
	return newestFirst(r.Versions, true), nil
}

func (pu purpur) resolve(ctx context.Context, p *Providers, _, version, build string) (Artifact, error) {
	if version == "latest" {
		vs, err := pu.versions(ctx, p, "")
		if err != nil {
			return Artifact{}, err
		}
		if version, err = latestStable(vs); err != nil {
			return Artifact{}, err
		}
	}
	b := "latest"
	if build != "" {
		b = build
	}
	base := p.ep().Purpur + "/v2/purpur/" + url.PathEscape(version) + "/" + url.PathEscape(b)
	var r struct {
		Build string `json:"build"`
		MD5   string `json:"md5"`
	}
	if err := p.getJSON(ctx, base, &r); err != nil {
		return Artifact{}, err
	}
	return Artifact{URL: base + "/download", Name: "purpur-" + version + "-" + r.Build + ".jar", Version: version, MD5: r.MD5}, nil
}

func (purpur) gameVersion(_, v string) string { return v }

// --- Fabric ---

type fabric struct{}

type fabricVersion struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

func (fabric) versions(ctx context.Context, p *Providers, _ string) ([]Version, error) {
	var r []fabricVersion
	if err := p.getJSON(ctx, p.ep().Fabric+"/v2/versions/game", &r); err != nil {
		return nil, err
	}
	var out []Version
	for _, v := range r { // newest first
		if v.Stable {
			out = append(out, Version{ID: v.Version, Stable: true})
		}
	}
	return out, nil
}

func (f fabric) resolve(ctx context.Context, p *Providers, _, version, loader string) (Artifact, error) {
	if version == "latest" {
		vs, err := f.versions(ctx, p, "")
		if err != nil {
			return Artifact{}, err
		}
		if version, err = latestStable(vs); err != nil {
			return Artifact{}, err
		}
	}
	firstStable := func(path string) (string, error) {
		var r []fabricVersion
		if err := p.getJSON(ctx, p.ep().Fabric+path, &r); err != nil {
			return "", err
		}
		for _, v := range r {
			if v.Stable {
				return v.Version, nil
			}
		}
		return "", ErrUnknownVersion
	}
	var err error
	if loader == "" {
		if loader, err = firstStable("/v2/versions/loader"); err != nil {
			return Artifact{}, err
		}
	}
	installer, err := firstStable("/v2/versions/installer")
	if err != nil {
		return Artifact{}, err
	}
	u := fmt.Sprintf("%s/v2/versions/loader/%s/%s/%s/server/jar", p.ep().Fabric, url.PathEscape(version), url.PathEscape(loader), url.PathEscape(installer))
	return Artifact{URL: u, Name: "fabric-server-launch.jar", Version: version}, nil
}

func (fabric) gameVersion(_, v string) string { return v }

// --- Forge (installer; the blueprint script runs it) ---

type forge struct{}

func (forge) promotions(ctx context.Context, p *Providers) (map[string]string, error) {
	v, err := p.memo("forge-promos", func() (any, error) {
		var r struct {
			Promos map[string]string `json:"promos"`
		}
		err := p.getJSON(ctx, p.ep().Forge+"/net/minecraftforge/forge/promotions_slim.json", &r)
		return r.Promos, err
	})
	if err != nil {
		return nil, err
	}
	return v.(map[string]string), nil
}

func (f forge) versions(ctx context.Context, p *Providers, _ string) ([]Version, error) {
	promos, err := f.promotions(ctx, p)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for k := range promos {
		mc, _, ok := strings.Cut(k, "-")
		if ok && !seen[mc] {
			seen[mc] = true
			ids = append(ids, mc)
		}
	}
	return newestFirst(ids, true), nil
}

func (f forge) resolve(ctx context.Context, p *Providers, _, version, build string) (Artifact, error) {
	promos, err := f.promotions(ctx, p)
	if err != nil {
		return Artifact{}, err
	}
	if version == "latest" {
		vs, err := f.versions(ctx, p, "")
		if err != nil {
			return Artifact{}, err
		}
		if version, err = latestStable(vs); err != nil {
			return Artifact{}, err
		}
	}
	if build == "" {
		build = promos[version+"-recommended"]
		if build == "" {
			build = promos[version+"-latest"]
		}
	}
	if build == "" {
		return Artifact{}, ErrUnknownVersion
	}
	full := version + "-" + build
	u := fmt.Sprintf("%s/net/minecraftforge/forge/%s/forge-%s-installer.jar", p.ep().ForgeMaven, url.PathEscape(full), url.PathEscape(full))
	sum, err := p.getText(ctx, u+".sha1")
	if err != nil || len(sum) != 40 {
		return Artifact{}, fmt.Errorf("Forge %s: checksum unavailable", full)
	}
	return Artifact{URL: u, Name: "forge-installer.jar", Version: version, SHA1: sum}, nil
}

func (forge) gameVersion(_, v string) string { return v }

// --- NeoForge (installer; the blueprint script runs it) ---

type neoForge struct{}

func (neoForge) versions(ctx context.Context, p *Providers, _ string) ([]Version, error) {
	var r struct {
		Versions []string `json:"versions"`
	}
	if err := p.getJSON(ctx, p.ep().NeoForge+"/api/maven/versions/releases/net/neoforged/neoforge", &r); err != nil {
		return nil, err
	}
	out := newestFirst(r.Versions, true)
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}

func (n neoForge) resolve(ctx context.Context, p *Providers, _, version, _ string) (Artifact, error) {
	if version == "latest" {
		vs, err := n.versions(ctx, p, "")
		if err != nil {
			return Artifact{}, err
		}
		if version, err = latestStable(vs); err != nil {
			return Artifact{}, err
		}
	}
	u := fmt.Sprintf("%s/releases/net/neoforged/neoforge/%s/neoforge-%s-installer.jar", p.ep().NeoForge, url.PathEscape(version), url.PathEscape(version))
	sum, err := p.getText(ctx, u+".sha1")
	if err != nil || len(sum) != 40 {
		return Artifact{}, fmt.Errorf("NeoForge %s: checksum unavailable", version)
	}
	return Artifact{URL: u, Name: "neoforge-installer.jar", Version: version, SHA1: sum}, nil
}

// gameVersion maps NeoForge 21.1.x to Minecraft 1.21.1, 20.4.x to 1.20.4 and,
// from the 2026 scheme on, 26.3.0.x to 26.3.
func (neoForge) gameVersion(_, v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return ""
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return ""
	}
	if major >= 26 {
		if len(parts) >= 3 && parts[2] != "0" {
			return parts[0] + "." + parts[1] + "." + parts[2]
		}
		return parts[0] + "." + parts[1]
	}
	if parts[1] == "0" {
		return "1." + parts[0]
	}
	return "1." + parts[0] + "." + parts[1]
}

func newSHA1() hash.Hash { return sha1.New() } //nolint:gosec
