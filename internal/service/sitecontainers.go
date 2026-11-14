package service

import (
	"context"
	"errors"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Web addresses for container apps. A site in app mode normally runs its own
// web unit (a KindWeb app built from a repository); a container-backed site
// instead proxies to an existing container app, such as the web service of a
// one-click app. The container joins the shared apps network (routable) so
// the sites listener can reach it; the site never owns the container: deleting
// the site only takes the container off that network.

// ReserveContainerSite creates the (not yet routed) site that will become a
// container's web address and returns it with its URL.
func (s *SiteService) ReserveContainerSite(ctx context.Context, actor domain.User, workspaceID, name, slug, domainID string) (domain.Site, string, error) {
	st, err := s.Create(ctx, actor, CreateSiteInput{Name: name, Slug: slug, DomainID: domainID, WorkspaceID: workspaceID})
	if err != nil {
		return domain.Site{}, "", err
	}
	return st, s.URLOf(st), nil
}

// AttachContainer turns a site into the web address of a container app that
// listens on port. Only workspace administrators attach containers: the site
// then forwards public traffic to it.
func (s *SiteService) AttachContainer(ctx context.Context, actor domain.User, siteID, botID string, port int, health string) error {
	st, _, err := s.requireSite(ctx, actor, siteID, domain.WorkspaceAdmin)
	if err != nil {
		return err
	}
	b, err := s.Bots.Store.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	if !b.IsContainer() || b.WorkspaceID != st.WorkspaceID {
		return domain.Invalid("a web address can only point at a container app of the site's workspace")
	}
	if b.NetworkDisabled {
		return domain.Invalid("this container has no network access, so a web address cannot reach it")
	}
	if port < 1 || port > 65535 {
		return domain.Invalid("the container port must be 1-65535")
	}
	if st.AppID != nil && *st.AppID != botID {
		return domain.Invalid("this site already runs another app")
	}
	if health == "" {
		health = "/"
	}
	cs, ok := s.Bots.Store.(interface {
		SetContainerRoutable(ctx context.Context, botID string, on bool, nowMS int64) error
	})
	if !ok {
		return errors.New("container web addresses are not supported by this store")
	}
	now := s.now().UnixMilli()
	if err := cs.SetContainerRoutable(ctx, botID, true, now); err != nil {
		return err
	}
	st.AppID, st.Mode, st.AppPort, st.HealthPath, st.UpdatedAtMS = &b.ID, domain.SiteModeApp, port, health, now
	st.Framework, st.BuildOutputDir, st.SPA = "container", "", false
	if err := s.Store.UpdateSite(ctx, st); err != nil {
		return err
	}
	if s.Health != nil {
		if err := s.Health.SetAppProbe(ctx, b.ID, port, health); err != nil {
			s.warn("create the container's health check", err)
		}
	}
	s.reloadSoon()
	return nil
}

// containerBacked reports whether a site proxies to a container app (which
// the site does not own) and returns that app.
func (s *SiteService) containerBacked(ctx context.Context, st domain.Site) (domain.Bot, bool) {
	if st.AppID == nil {
		return domain.Bot{}, false
	}
	b, err := s.Bots.Store.GetBot(ctx, *st.AppID)
	if err != nil || !b.IsContainer() {
		return domain.Bot{}, false
	}
	return b, true
}

// detachContainer takes a container off the apps network when its site goes.
func (s *SiteService) detachContainer(ctx context.Context, b domain.Bot) {
	if cs, ok := s.Bots.Store.(interface {
		SetContainerRoutable(ctx context.Context, botID string, on bool, nowMS int64) error
	}); ok {
		if err := cs.SetContainerRoutable(ctx, b.ID, false, s.now().UnixMilli()); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.warn("take a container off the apps network", err)
		}
	}
}

// SitesForApps returns the sites that proxy to any of the given apps.
func (s *SiteService) SitesForApps(ctx context.Context, ids []string) map[string]domain.Site {
	out := map[string]domain.Site{}
	if s == nil || !s.Enabled() {
		return out
	}
	for _, id := range ids {
		if st, err := s.Store.GetSiteForApp(ctx, id); err == nil {
			out[id] = st
		}
	}
	return out
}

// deleteContainerSites removes the sites of a stack's services before the
// services themselves are deleted.
func (s *SiteService) deleteContainerSites(ctx context.Context, ids []string) error {
	for _, st := range s.SitesForApps(ctx, ids) {
		st.AppID = nil // the container goes with its stack, not with the site
		if err := s.deleteSite(ctx, st); err != nil {
			return err
		}
	}
	return nil
}

// ContainerWebAddressInput gives a container app a web address.
type ContainerWebAddressInput struct {
	Slug         string
	DomainID     string // sites domain; "" = the primary one
	UserDomainID string // also serve at <slug>.<one of the actor's domains>
	Port         int    // the port the container listens on
	HealthPath   string
}

// AddContainerWebAddress creates a site that forwards to an existing
// container app, so it answers at <slug>.<sites domain> (and, with a user
// domain, at <slug>.<that domain>). Workspace administrators only; the
// container is recreated on the apps network.
func (s *SiteService) AddContainerWebAddress(ctx context.Context, actor domain.User, botID string, in ContainerWebAddressInput) (domain.Site, error) {
	if !s.Enabled() {
		return domain.Site{}, domain.Invalid("site hosting is not enabled on this panel")
	}
	b, err := s.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin)
	if err != nil {
		return domain.Site{}, err
	}
	if !b.IsContainer() {
		return domain.Site{}, domain.Invalid("only container apps get a web address this way; sites from GitHub have their own")
	}
	if _, err := s.Store.GetSiteForApp(ctx, b.ID); err == nil {
		return domain.Site{}, domain.Invalid("this app already has a web address; manage its domains on the site page")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Site{}, err
	}
	if b.NetworkDisabled {
		return domain.Site{}, domain.Invalid("this container has no network access, so a web address cannot reach it")
	}
	if in.Port < 1 || in.Port > 65535 {
		return domain.Site{}, domain.Invalid("enter the port the container listens on (1-65535)")
	}
	slug := in.Slug
	if slug == "" {
		slug = slugFrom(b.Name)
	}
	st, err := s.Create(ctx, actor, CreateSiteInput{Name: b.Name, Slug: slug, DomainID: in.DomainID, WorkspaceID: b.WorkspaceID, UserDomainID: in.UserDomainID})
	if err != nil {
		return domain.Site{}, err
	}
	if err := s.AttachContainer(ctx, actor, st.ID, b.ID, in.Port, in.HealthPath); err != nil {
		if derr := s.deleteSite(ctx, st); derr != nil {
			s.warn("remove a site after a failed container attach", derr)
		}
		return domain.Site{}, err
	}
	return s.Store.GetSite(ctx, st.ID)
}
