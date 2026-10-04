package api

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type siteDTO struct {
	ID              string  `json:"id"`
	WorkspaceID     string  `json:"workspace_id"`
	WorkspaceName   string  `json:"workspace_name"`
	OwnerID         string  `json:"owner_id"`
	OwnerEmail      string  `json:"owner_email"`
	BotID           *string `json:"bot_id"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	DomainID        *string `json:"domain_id"`
	BaseDomain      string  `json:"base_domain"`
	URL             string  `json:"url"`
	SPA             bool    `json:"spa"`
	CleanURLs       bool    `json:"clean_urls"`
	Mode            string  `json:"mode"`
	PageTitle       string  `json:"page_title"`
	PageDescription string  `json:"page_description"`
	PageTheme       string  `json:"page_theme"`
	PageAccent      string  `json:"page_accent"`
	PageHTML        string  `json:"page_html"`
	PageCSS         string  `json:"page_css"`
	WidgetsPublic   bool    `json:"widgets_public"`
	CurrentRelease  *string `json:"current_release"`
	ReleaseBytes    int64   `json:"release_bytes"`
	Disabled        bool    `json:"disabled"`
	Domains         int     `json:"domains"`
	RepoFullName    *string `json:"repo_full_name"`
	RepoBranch      *string `json:"repo_branch"`
	RepoRoot        string  `json:"repo_root"`
	CreatedAtMS     int64   `json:"created_at_ms"`
	UpdatedAtMS     int64   `json:"updated_at_ms"`
	// IconURL serves the custom logo, the release favicon or the bot's logo
	// (404 when there is none); the version changes when any of them may have.
	IconURL    string `json:"icon_url"`
	CustomLogo bool   `json:"custom_logo"`
}

func (s *panel) toSite(st domain.Site) siteDTO {
	return siteDTO{ID: st.ID, WorkspaceID: st.WorkspaceID, WorkspaceName: st.WorkspaceName, OwnerID: st.OwnerID, OwnerEmail: st.OwnerEmail,
		BotID: st.BotID, Name: st.Name, Slug: st.Slug, DomainID: st.DomainID, BaseDomain: st.BaseDomain, URL: s.sites.URLOf(st), SPA: st.SPA, CleanURLs: st.CleanURLs,
		Mode: st.Mode, PageTitle: st.PageTitle, PageDescription: st.PageDescription, PageTheme: st.PageTheme, PageAccent: st.PageAccent,
		PageHTML: st.PageHTML, PageCSS: st.PageCSS, WidgetsPublic: st.WidgetsPublic, CurrentRelease: st.CurrentRelease,
		ReleaseBytes: st.ReleaseBytes, Disabled: st.Disabled, Domains: st.Domains, RepoFullName: st.RepoFullName, RepoBranch: st.RepoBranch,
		RepoRoot: st.RepoRoot, CreatedAtMS: st.CreatedAtMS, UpdatedAtMS: st.UpdatedAtMS, CustomLogo: st.LogoUpdatedMS > 0,
		IconURL: fmt.Sprintf("/api/v1/sites/%s/icon?v=%d-%s", st.ID, st.LogoUpdatedMS, strOr(st.CurrentRelease))}
}

func strOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *panel) siteList(list []domain.Site) []siteDTO {
	out := make([]siteDTO, len(list))
	for i, st := range list {
		out[i] = s.toSite(st)
	}
	return out
}

// sitesIn lists a workspace's sites for the administrator workspace view
// (empty when hosting is off).
func (s *panel) sitesIn(c fiber.Ctx, workspaceID string) []siteDTO {
	if !s.sites.Enabled() {
		return []siteDTO{}
	}
	list, err := s.sites.ListWorkspace(c.Context(), currentUser(c), workspaceID)
	if err != nil {
		return []siteDTO{}
	}
	return s.siteList(list)
}

type domainDTO struct {
	Domain          string  `json:"domain"`
	URL             string  `json:"url"`
	Verified        bool    `json:"verified"`
	VerifiedAtMS    *int64  `json:"verified_at_ms"`
	LastCheckedAtMS *int64  `json:"last_checked_at_ms"`
	LastError       *string `json:"last_error"`
	TXTName         string  `json:"txt_name"`
	TXTValue        string  `json:"txt_value"`
	// The record that routes visitors: CNAME to a host, or A/AAAA to an
	// address ("" = this server's public address, unknown to the panel).
	RecordType   string `json:"record_type"`
	RecordTarget string `json:"record_target"`
}

func (s *panel) toDomain(d domain.SiteDomain, st domain.Site) domainDTO {
	name, value := service.VerificationRecord(d)
	kind, target := s.sites.TrafficRecord(st.Slug, st.BaseDomain)
	return domainDTO{d.Domain, s.sites.DomainURL(d.Domain), d.VerifiedAtMS != nil, d.VerifiedAtMS, d.LastCheckedAtMS, d.LastError,
		name, value, kind, target}
}

type releaseDTO struct {
	ID          string  `json:"id"`
	Source      string  `json:"source"`
	SourceLabel *string `json:"source_label"`
	Files       int     `json:"files"`
	Bytes       int64   `json:"bytes"`
	Actor       *string `json:"actor"`
	Current     bool    `json:"current"`
	CreatedAtMS int64   `json:"created_at_ms"`
}

// baseChoiceDTO is a base domain a site can be placed under.
type baseChoiceDTO struct {
	ID         string `json:"id"`
	Domain     string `json:"domain"`
	Label      string `json:"label"`
	Primary    bool   `json:"primary"`
	ExampleURL string `json:"example_url"`
}

// sitesInfo tells the interface whether hosting is on and how addresses look.
func (s *panel) sitesInfo(c fiber.Ctx) error {
	if !s.sites.Enabled() {
		return c.JSON(fiber.Map{"enabled": false})
	}
	bases, err := s.sites.ServingBaseDomains(c.Context())
	if err != nil {
		return err
	}
	choices := make([]baseChoiceDTO, len(bases))
	for i, b := range bases {
		choices[i] = baseChoiceDTO{b.ID, b.Domain, b.Label, b.Primary, s.sites.SiteURL("example", b.Domain)}
	}
	return c.JSON(fiber.Map{"enabled": true, "domain": s.sites.SitesDomain(), "example_url": s.sites.SiteURL("example", ""),
		"domains": choices, "max_bytes": s.sites.MaxBytes, "max_domains": service.MaxDomainsPerSite})
}

func (s *panel) listSites(c fiber.Ctx) error {
	list, err := s.sites.List(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"sites": s.siteList(list)})
}

func (s *panel) createSite(c fiber.Ctx) error {
	var in struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		DomainID    string `json:"domain_id"`
		WorkspaceID string `json:"workspace_id"`
		SPA         bool   `json:"spa"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := s.sites.Create(c.Context(), currentUser(c), service.CreateSiteInput{Name: in.Name, Slug: in.Slug, DomainID: in.DomainID,
		WorkspaceID: in.WorkspaceID, SPA: in.SPA})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.toSite(st))
}

func (s *panel) createBotSite(c fiber.Ctx) error {
	botID := strings.Clone(c.Params("id"))
	b, err := s.bots.Authorize(c.Context(), currentUser(c), botID, domain.PermEditFiles)
	if err != nil {
		return err
	}
	var in struct {
		Slug     string `json:"slug"`
		DomainID string `json:"domain_id"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := s.sites.Create(c.Context(), currentUser(c), service.CreateSiteInput{
		Name: b.Name, Slug: in.Slug, DomainID: in.DomainID, WorkspaceID: b.WorkspaceID, BotID: b.ID, Mode: "page",
	})
	if err != nil {
		return err
	}
	d, err := s.sites.Get(c.Context(), currentUser(c), st.ID)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.siteDetailJSON(d))
}

func (s *panel) getBotSite(c fiber.Ctx) error {
	d, err := s.sites.GetForBot(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(s.siteDetailJSON(d))
}

func (s *panel) getSite(c fiber.Ctx) error {
	d, err := s.sites.Get(c.Context(), currentUser(c), strings.Clone(c.Params("sid")))
	if err != nil {
		return err
	}
	return c.JSON(s.siteDetailJSON(d))
}

func (s *panel) siteDetailJSON(d service.SiteDetail) fiber.Map {
	domains := make([]domainDTO, len(d.Domains))
	for i, x := range d.Domains {
		domains[i] = s.toDomain(x, d.Site)
	}
	releases := make([]releaseDTO, len(d.Releases))
	for i, r := range d.Releases {
		releases[i] = releaseDTO{r.ID, r.Source, r.SourceLabel, r.Files, r.Bytes, r.ActorEmail,
			d.Site.CurrentRelease != nil && *d.Site.CurrentRelease == r.ID, r.CreatedAtMS}
	}
	return fiber.Map{"site": s.toSite(d.Site), "role": d.Role, "domains": domains, "releases": releases,
		"deploy": fiber.Map{"running": d.Job.Running, "last_error": d.Job.LastError, "finished_at_ms": d.Job.FinishedMS}}
}

func (s *panel) patchSite(c fiber.Ctx) error {
	var in struct {
		Name            *string `json:"name"`
		Slug            *string `json:"slug"`
		DomainID        *string `json:"domain_id"`
		SPA             *bool   `json:"spa"`
		CleanURLs       *bool   `json:"clean_urls"`
		WorkspaceID     *string `json:"workspace_id"`
		Mode            *string `json:"mode"`
		PageTitle       *string `json:"page_title"`
		PageDescription *string `json:"page_description"`
		PageTheme       *string `json:"page_theme"`
		PageAccent      *string `json:"page_accent"`
		PageHTML        *string `json:"page_html"`
		PageCSS         *string `json:"page_css"`
		WidgetsPublic   *bool   `json:"widgets_public"`
		Repo            *struct {
			FullName string `json:"full_name"`
			Branch   string `json:"branch"`
			RootDir  string `json:"root_dir"`
			Clear    bool   `json:"clear"`
		} `json:"repo"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	up := service.UpdateSiteInput{Name: in.Name, Slug: in.Slug, DomainID: in.DomainID, SPA: in.SPA, CleanURLs: in.CleanURLs, WorkspaceID: in.WorkspaceID,
		Mode: in.Mode, PageTitle: in.PageTitle, PageDescription: in.PageDescription, PageTheme: in.PageTheme,
		PageAccent: in.PageAccent, PageHTML: in.PageHTML, PageCSS: in.PageCSS, WidgetsPublic: in.WidgetsPublic}
	if in.Repo != nil {
		up.Repo = &service.RepoInput{FullName: in.Repo.FullName, Branch: in.Repo.Branch, RootDir: in.Repo.RootDir, Clear: in.Repo.Clear}
	}
	if _, err := s.sites.Update(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), up); err != nil {
		return err
	}
	return s.getSite(c)
}

func (s *panel) deleteSite(c fiber.Ctx) error {
	if err := s.sites.Delete(c.Context(), currentUser(c), strings.Clone(c.Params("sid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// uploadSite publishes a ZIP archive (the raw request body) as a new release.
func (s *panel) uploadSite(c fiber.Ctx) error {
	if err := s.checkLength(c); err != nil {
		return err
	}
	r, err := s.sites.Upload(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), bodyReader(c))
	if err != nil {
		return mapFSError(err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"release": r.ID, "files": r.Files, "bytes": r.Bytes})
}

func (s *panel) deploySite(c fiber.Ctx) error {
	if err := s.sites.Deploy(c.Context(), currentUser(c), strings.Clone(c.Params("sid"))); err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "queued"})
}

func (s *panel) activateRelease(c fiber.Ctx) error {
	if err := s.sites.Activate(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), strings.Clone(c.Params("rid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) addSiteDomain(c fiber.Ctx) error {
	var in struct {
		Domain string `json:"domain"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	id := strings.Clone(c.Params("sid"))
	d, err := s.sites.AddDomain(c.Context(), currentUser(c), id, in.Domain)
	if err != nil {
		return err
	}
	st, err := s.sites.Get(c.Context(), currentUser(c), id)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.toDomain(d, st.Site))
}

func (s *panel) verifySiteDomain(c fiber.Ctx) error {
	id := strings.Clone(c.Params("sid"))
	d, err := s.sites.VerifyDomain(c.Context(), currentUser(c), id, strings.Clone(c.Params("domain")))
	if err != nil {
		return err
	}
	st, err := s.sites.Get(c.Context(), currentUser(c), id)
	if err != nil {
		return err
	}
	return c.JSON(s.toDomain(d, st.Site))
}

func (s *panel) removeSiteDomain(c fiber.Ctx) error {
	if err := s.sites.RemoveDomain(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), strings.Clone(c.Params("domain"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) adminListSites(c fiber.Ctx) error {
	list, err := s.sites.ListAll(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"sites": s.siteList(list)})
}

func (s *panel) adminPatchSite(c fiber.Ctx) error {
	var in struct {
		Disabled *bool `json:"disabled"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Disabled == nil {
		return domain.Invalid("nothing to change")
	}
	if err := s.sites.SetDisabled(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), *in.Disabled); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// baseDomainDTO is a base domain as administrators see it, with the DNS
// records that make it work.
type baseDomainDTO struct {
	ID              string  `json:"id"`
	Domain          string  `json:"domain"`
	Label           string  `json:"label"`
	Enabled         bool    `json:"enabled"`
	Primary         bool    `json:"primary"`
	FromConfig      bool    `json:"from_config"`
	Serving         bool    `json:"serving"`
	Verified        bool    `json:"verified"`
	VerifiedAtMS    *int64  `json:"verified_at_ms"`
	LastCheckedAtMS *int64  `json:"last_checked_at_ms"`
	LastError       *string `json:"last_error"`
	DNSTarget       string  `json:"dns_target"`
	Sites           int     `json:"sites"`
	ExampleURL      string  `json:"example_url"`
	TXTName         string  `json:"txt_name"`
	TXTValue        string  `json:"txt_value"`
	// The record that routes visitors for the domain and *.domain: CNAME to
	// a host, or A/AAAA to an address ("" = this server's public address).
	RecordType   string `json:"record_type"`
	RecordTarget string `json:"record_target"`
	CreatedAtMS  int64  `json:"created_at_ms"`
}

func (s *panel) toBaseDomain(b domain.SiteBaseDomain) baseDomainDTO {
	name, value := service.BaseDomainVerificationRecord(b)
	kind, target := s.sites.BaseDomainRecord(b)
	return baseDomainDTO{ID: b.ID, Domain: b.Domain, Label: b.Label, Enabled: b.Enabled, Primary: b.Primary, FromConfig: b.FromConfig,
		Serving: b.Serving(), Verified: b.VerifiedAtMS != nil, VerifiedAtMS: b.VerifiedAtMS, LastCheckedAtMS: b.LastCheckedAtMS,
		LastError: b.LastError, DNSTarget: b.DNSTarget, Sites: b.Sites, ExampleURL: s.sites.SiteURL("example", b.Domain),
		TXTName: name, TXTValue: value, RecordType: kind, RecordTarget: target, CreatedAtMS: b.CreatedAtMS}
}

func (s *panel) adminListBaseDomains(c fiber.Ctx) error {
	list, err := s.sites.ListBaseDomains(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]baseDomainDTO, len(list))
	for i, b := range list {
		out[i] = s.toBaseDomain(b)
	}
	return c.JSON(fiber.Map{"domains": out})
}

func (s *panel) adminAddBaseDomain(c fiber.Ctx) error {
	var in struct {
		Domain    string `json:"domain"`
		Label     string `json:"label"`
		DNSTarget string `json:"dns_target"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.sites.AddBaseDomain(c.Context(), currentUser(c), service.BaseDomainInput{Domain: in.Domain, Label: in.Label, DNSTarget: in.DNSTarget})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.toBaseDomain(b))
}

func (s *panel) adminVerifyBaseDomain(c fiber.Ctx) error {
	b, err := s.sites.VerifyBaseDomain(c.Context(), currentUser(c), strings.Clone(c.Params("domain")))
	if err != nil {
		return err
	}
	return c.JSON(s.toBaseDomain(b))
}

func (s *panel) adminPatchBaseDomain(c fiber.Ctx) error {
	var in struct {
		Enabled   *bool   `json:"enabled"`
		Primary   *bool   `json:"primary"`
		Label     *string `json:"label"`
		DNSTarget *string `json:"dns_target"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.sites.UpdateBaseDomain(c.Context(), currentUser(c), strings.Clone(c.Params("domain")),
		service.BaseDomainUpdate{Enabled: in.Enabled, Primary: in.Primary, Label: in.Label, DNSTarget: in.DNSTarget})
	if err != nil {
		return err
	}
	return c.JSON(s.toBaseDomain(b))
}

func (s *panel) adminMoveBaseDomainSites(c fiber.Ctx) error {
	var in struct {
		To string `json:"to"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	n, err := s.sites.MoveBaseDomainSites(c.Context(), currentUser(c), strings.Clone(c.Params("domain")), in.To)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"moved": n})
}

func (s *panel) adminDeleteBaseDomain(c fiber.Ctx) error {
	if err := s.sites.DeleteBaseDomain(c.Context(), currentUser(c), strings.Clone(c.Params("domain"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
