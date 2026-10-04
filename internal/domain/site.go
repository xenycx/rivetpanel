package domain

// Site is a static website served by the sites listener. It serves exactly
// one immutable release (CurrentRelease) at a time.
type Site struct {
	ID              string
	WorkspaceID     string
	OwnerID         string
	BotID           *string // set when this is the public page for a Discord bot
	Name            string
	Slug            string  // unique per base domain
	DomainID        *string // base domain the slug lives under (nil only before start-up assigns one)
	SPA             bool    // unknown paths serve /index.html (client-side routing)
	CleanURLs       bool    // /about serves about.html or about/index.html
	Mode            string  // page | files
	PageTitle       string
	PageDescription string
	PageTheme       string // midnight | daylight | system
	PageAccent      string // validated #rrggbb
	PageHTML        string // trusted author HTML, served only on the sites origin
	PageCSS         string // scoped by origin, never injected into the panel
	WidgetsPublic   bool   // explicit opt-in: declarative bot widgets are public
	CurrentRelease  *string
	Disabled        bool // suspended by an administrator
	RepoFullName    *string
	RepoBranch      *string
	RepoRoot        string
	RepoTokenUser   *string // whose GitHub token deploys the repository
	CreatedAtMS     int64
	UpdatedAtMS     int64
	LogoUpdatedMS   int64 // when a custom logo was set (0 = none)

	// Filled by listing queries.
	OwnerEmail    string
	WorkspaceName string
	BaseDomain    string // host of DomainID ("" when unassigned)
	Domains       int
	ReleaseBytes  int64 // size of the current release
}

// SiteRelease is one deployed, immutable set of files.
type SiteRelease struct {
	ID          string
	SiteID      string
	Source      string // upload | github
	SourceLabel *string
	Files       int
	Bytes       int64
	ActorID     *string
	ActorEmail  *string
	CreatedAtMS int64
}

// SiteDomain is a custom host name attached to a site. It is served only
// once VerifiedAtMS is set (DNS TXT ownership proof).
type SiteDomain struct {
	Domain          string
	SiteID          string
	Token           string
	VerifiedAtMS    *int64
	LastCheckedAtMS *int64
	LastError       *string
	CreatedAtMS     int64
}

// SiteBaseDomain is a domain every site may live under: a site is served at
// <slug>.<domain>. Domains listed in the configuration are trusted; any other
// is served only once a DNS TXT record proves the administrator controls it.
type SiteBaseDomain struct {
	ID              string
	Domain          string
	Enabled         bool
	Primary         bool   // the default for new sites
	Label           string // display name
	DNSTarget       string // optional host name or address shown in DNS instructions
	FromConfig      bool   // listed in RIVET_SITES_BASE_URL or RIVET_SITES_DOMAINS
	Token           string
	VerifiedAtMS    *int64
	LastCheckedAtMS *int64
	LastError       *string
	CreatedAtMS     int64
	UpdatedAtMS     int64

	Sites int // filled by listing queries
}

// Serving reports whether sites under the domain are served.
func (d SiteBaseDomain) Serving() bool { return d.Enabled && d.VerifiedAtMS != nil }

// SiteRouteTable is everything the sites listener needs to map a host name to
// a site: slug routes per base domain, verified custom domains, and the base
// domains being served.
type SiteRouteTable struct {
	BySlug   map[string]SiteRoute // key: SlugKey(domainID, slug)
	ByDomain map[string]SiteRoute // verified custom domains
	Bases    []SiteBaseDomain     // enabled and verified
}

// SlugKey is the BySlug key of a slug under a base domain.
func SlugKey(domainID, slug string) string { return domainID + "\x00" + slug }

// SiteRoute is what the sites listener needs to serve one host name.
type SiteRoute struct {
	SiteID    string
	Release   string
	SPA       bool
	CleanURLs bool
	Disabled  bool
	Mode      string
}

// PublicSitePage is the deliberately small, public view used by the separate
// sites listener. It contains no owner identity, console output, environment,
// raw events, command names, or private analytics.
type PublicSitePage struct {
	SiteID, BotID, BotName, DiscordUsername, DiscordAvatarURL string
	Title, Description, Theme, Accent, HTML, CSS              string
	WidgetsPublic                                             bool
	Widgets                                                   []BotWidget
}
