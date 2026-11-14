package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/publicsuffix"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// ---- user domains ----
//
// A user proves a domain once, with a TXT record holding their own token:
//
//	_rivetpanel-verify.example.com  TXT  rivetpanel-verify=<user token>
//
// The record may sit on the domain itself or on any parent up to its
// registrable domain. A verified user domain covers every name below it:
// custom domains at or under it that the user adds to a site are verified at
// once (site_domains.user_domain_id), and new sites can be created straight
// at <slug>.<domain>. Custom domains proven by their own per-domain record
// (site_domains.token, the original scheme) keep working.

// MaxUserDomains is how many domains one user may keep.
const MaxUserDomains = 50

// VerificationZone is the registrable domain of name (example.com for
// www.shop.example.com): where a user's verification record goes by default.
func VerificationZone(name string) string {
	if z, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil {
		return z
	}
	return name
}

// DomainVerificationRecord is the TXT record proving a user controls name
// and everything below it.
func DomainVerificationRecord(name, token string) (txtName, value string) {
	return domainTXTPrefix + name, domainTXTValue + token
}

// userToken returns the user's domain-verification token, creating it on
// first use.
func (s *SiteService) userToken(ctx context.Context, userID string) (string, error) {
	return s.Store.UserDomainToken(ctx, userID, randHex(16), s.now().UnixMilli())
}

// UserDomainToken returns the actor's verification token.
func (s *SiteService) UserDomainToken(ctx context.Context, actor domain.User) (string, error) {
	return s.userToken(ctx, actor.ID)
}

// findRecord looks for a TXT record holding one of want on name and each
// parent of name up to its registrable domain (legacy is accepted on name
// itself only). It returns the name the record was found on, or a message
// about the record expected at the registrable domain.
func (s *SiteService) findRecord(ctx context.Context, name, want, legacy string) (string, string) {
	zone := VerificationZone(name)
	r := s.txtResolverFor(ctx, zone)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var zoneErr error
	var zoneRecs []string
	for n := name; ; {
		recs, err := r.LookupTXT(ctx, domainTXTPrefix+n)
		for _, v := range recs {
			v = strings.TrimSpace(v)
			if (want != "" && v == want) || (legacy != "" && n == name && v == legacy) {
				return n, ""
			}
		}
		if n == zone {
			zoneErr, zoneRecs = err, recs
			break
		}
		i := strings.IndexByte(n, '.')
		if i < 0 || !strings.HasSuffix(n, "."+zone) {
			break
		}
		n = n[i+1:]
	}
	at := domainTXTPrefix + zone
	var dnsErr *net.DNSError
	switch {
	case len(zoneRecs) > 0:
		return "", mismatchMessage(at, want, zoneRecs)
	case zoneErr != nil && !(errors.As(zoneErr, &dnsErr) && dnsErr.IsNotFound):
		return "", "The DNS lookup failed; try again in a minute"
	}
	return "", "No TXT record found at " + at + " yet; DNS changes can take several minutes to appear"
}

// covers reports whether a user domain covers name.
func covers(parent, name string) bool { return name == parent || strings.HasSuffix(name, "."+parent) }

// coveringUserDomain returns the user's verified domain closest to name that
// covers it.
func (s *SiteService) coveringUserDomain(ctx context.Context, userID, name string) (domain.UserDomain, bool, error) {
	list, err := s.Store.ListUserDomains(ctx, userID)
	if err != nil {
		return domain.UserDomain{}, false, err
	}
	var best domain.UserDomain
	for _, d := range list {
		if d.VerifiedAtMS != nil && covers(d.Domain, name) && len(d.Domain) > len(best.Domain) {
			best = d
		}
	}
	return best, best.ID != "", nil
}

// checkHostable refuses names no site may answer on.
func (s *SiteService) checkHostable(ctx context.Context, d string) error {
	if p := normalizeHost(s.PanelHost); p != "" && covers(p, d) {
		return domain.Invalid("the panel's own domain (and its subdomains) cannot host a site")
	}
	if base, err := s.underBaseDomain(ctx, d); err != nil {
		return err
	} else if base != "" {
		return domain.Invalid("addresses under " + base + " are assigned automatically; change the site address instead")
	}
	return nil
}

// ListUserDomains returns the actor's domains.
func (s *SiteService) ListUserDomains(ctx context.Context, actor domain.User) ([]domain.UserDomain, error) {
	return s.Store.ListUserDomains(ctx, actor.ID)
}

// AddUserDomain adds a domain to the actor's list and verifies it at once
// when their record is already in place.
func (s *SiteService) AddUserDomain(ctx context.Context, actor domain.User, name string) (domain.UserDomain, error) {
	if !s.Enabled() {
		return domain.UserDomain{}, domain.Invalid("site hosting is not enabled on this panel")
	}
	d, err := NormalizeDomain(name)
	if err != nil {
		return domain.UserDomain{}, err
	}
	if err := s.checkHostable(ctx, d); err != nil {
		return domain.UserDomain{}, err
	}
	list, err := s.Store.ListUserDomains(ctx, actor.ID)
	if err != nil {
		return domain.UserDomain{}, err
	}
	if len(list) >= MaxUserDomains {
		return domain.UserDomain{}, domain.Invalid(fmt.Sprintf("you can keep at most %d domains", MaxUserDomains))
	}
	now := s.now().UnixMilli()
	ud := domain.UserDomain{ID: uuid.NewString(), UserID: actor.ID, Domain: d, CreatedAtMS: now}
	// A verified parent already proves this name.
	if p, ok, err := s.coveringUserDomain(ctx, actor.ID, d); err != nil {
		return domain.UserDomain{}, err
	} else if ok && p.Domain != d {
		ud.VerifiedAtMS, ud.LastCheckedAtMS = &now, &now
	}
	if ud.VerifiedAtMS == nil {
		tok, err := s.userToken(ctx, actor.ID)
		if err != nil {
			return domain.UserDomain{}, err
		}
		if found, _ := s.findRecord(ctx, d, domainTXTValue+tok, ""); found != "" {
			ud.VerifiedAtMS, ud.LastCheckedAtMS = &now, &now
		}
	}
	if err := s.Store.InsertUserDomain(ctx, ud); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.UserDomain{}, domain.Invalid("that domain is already in your list")
		}
		return domain.UserDomain{}, err
	}
	return ud, nil
}

func (s *SiteService) ownUserDomain(ctx context.Context, actor domain.User, id string) (domain.UserDomain, error) {
	ud, err := s.Store.GetUserDomain(ctx, id)
	if err != nil {
		return domain.UserDomain{}, err
	}
	if ud.UserID != actor.ID {
		return domain.UserDomain{}, domain.ErrNotFound
	}
	return ud, nil
}

// VerifyUserDomain checks the actor's TXT record for one of their domains.
func (s *SiteService) VerifyUserDomain(ctx context.Context, actor domain.User, id string) (domain.UserDomain, error) {
	ud, err := s.ownUserDomain(ctx, actor, id)
	if err != nil {
		return domain.UserDomain{}, err
	}
	tok, err := s.userToken(ctx, actor.ID)
	if err != nil {
		return domain.UserDomain{}, err
	}
	found, msg := s.findRecord(ctx, ud.Domain, domainTXTValue+tok, "")
	if found == "" {
		if p, ok, err := s.coveringUserDomain(ctx, actor.ID, ud.Domain); err != nil {
			return domain.UserDomain{}, err
		} else if ok && p.ID != ud.ID {
			found = p.Domain
		}
	}
	if found == "" {
		if err := s.checkHostable(ctx, ud.Domain); err != nil {
			msg = err.Error()
		}
	}
	now := s.now().UnixMilli()
	var at *int64
	var errMsg *string
	if found != "" {
		at = &now
	} else {
		errMsg = &msg
	}
	if err := s.Store.RecordUserDomainCheck(ctx, ud.ID, at, errMsg, now); err != nil {
		return domain.UserDomain{}, err
	}
	return s.Store.GetUserDomain(ctx, ud.ID)
}

// RemoveUserDomain removes one of the actor's domains. Custom domains it
// proved stay attached; each needs a record of its own at the next check.
func (s *SiteService) RemoveUserDomain(ctx context.Context, actor domain.User, id string) error {
	return s.Store.DeleteUserDomain(ctx, actor.ID, id)
}

// UserDomainUses lists the site addresses a user domain verified.
func (s *SiteService) UserDomainUses(ctx context.Context, id string) []domain.UserDomainUse {
	list, err := s.Store.ListDomainsVia(ctx, id)
	if err != nil {
		s.warn("list user domain uses", err)
	}
	return list
}

// SetDefaultUserDomain makes one of the actor's verified domains the one new
// sites start on (on false, clears it).
func (s *SiteService) SetDefaultUserDomain(ctx context.Context, actor domain.User, id string, on bool) (domain.UserDomain, error) {
	ud, err := s.ownUserDomain(ctx, actor, id)
	if err != nil {
		return domain.UserDomain{}, err
	}
	target := ""
	if on {
		if ud.VerifiedAtMS == nil {
			return domain.UserDomain{}, domain.Invalid("verify " + ud.Domain + " before making it the default")
		}
		target = ud.ID
	} else if !ud.Default {
		return ud, nil
	}
	if err := s.Store.SetDefaultUserDomain(ctx, actor.ID, target); err != nil {
		return domain.UserDomain{}, err
	}
	return s.Store.GetUserDomain(ctx, ud.ID)
}

// usableUserDomain returns one of the actor's verified domains for a new site.
func (s *SiteService) usableUserDomain(ctx context.Context, actor domain.User, id string) (domain.UserDomain, error) {
	ud, err := s.ownUserDomain(ctx, actor, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.UserDomain{}, domain.Invalid("that domain is not one of yours")
	} else if err != nil {
		return domain.UserDomain{}, err
	}
	if ud.VerifiedAtMS == nil {
		return domain.UserDomain{}, domain.Invalid(ud.Domain + " is not verified yet; check its DNS record under Settings → Domains")
	}
	return ud, nil
}

// proveSiteDomain tries to verify a custom domain for actor: through one of
// their verified domains, their TXT record (which also adds the proven name
// to their domains), or the domain's own per-domain record. It returns the
// user domain that proved it ("" for the per-domain record) and, on failure,
// a message.
func (s *SiteService) proveSiteDomain(ctx context.Context, actor domain.User, d domain.SiteDomain) (bool, string, string, error) {
	if ud, ok, err := s.coveringUserDomain(ctx, actor.ID, d.Domain); err != nil {
		return false, "", "", err
	} else if ok {
		return true, ud.ID, "", nil
	}
	tok, err := s.userToken(ctx, actor.ID)
	if err != nil {
		return false, "", "", err
	}
	_, legacy := VerificationRecord(d)
	found, msg := s.findRecord(ctx, d.Domain, domainTXTValue+tok, legacy)
	switch {
	case found == "":
		return false, "", msg, nil
	case found == d.Domain && !s.hasRecord(ctx, d.Domain, domainTXTValue+tok):
		return true, "", "", nil // the per-domain record
	}
	// The user's record: remember the proven name as one of their domains.
	now := s.now().UnixMilli()
	ud := domain.UserDomain{ID: uuid.NewString(), UserID: actor.ID, Domain: found, VerifiedAtMS: &now, LastCheckedAtMS: &now, CreatedAtMS: now}
	if err := s.Store.InsertUserDomain(ctx, ud); errors.Is(err, domain.ErrConflict) {
		list, err := s.Store.ListUserDomains(ctx, actor.ID)
		if err != nil {
			return false, "", "", err
		}
		for _, x := range list {
			if x.Domain == found {
				ud = x
			}
		}
		if err := s.Store.RecordUserDomainCheck(ctx, ud.ID, &now, nil, now); err != nil {
			return false, "", "", err
		}
	} else if err != nil {
		return false, "", "", err
	}
	return true, ud.ID, "", nil
}

// hasRecord reports whether name's own verification TXT holds value.
func (s *SiteService) hasRecord(ctx context.Context, name, value string) bool {
	ok, _ := s.checkTXT(ctx, domainTXTPrefix+name, value)
	return ok
}

// recordSiteDomainProof stores a successful or failed site domain check.
func (s *SiteService) recordSiteDomainProof(ctx context.Context, name string, ok bool, via, msg string) error {
	now := s.now().UnixMilli()
	if ok && via != "" {
		return s.Store.VerifyDomainVia(ctx, name, via, now)
	}
	var at *int64
	var errMsg *string
	if ok {
		at = &now
	} else {
		errMsg = &msg
	}
	return s.Store.RecordDomainCheck(ctx, name, at, errMsg, now)
}

// AddDomain attaches a custom domain; it is served once verified. A domain
// at or under one of the actor's verified domains is verified at once.
func (s *SiteService) AddDomain(ctx context.Context, actor domain.User, id, name string) (domain.SiteDomain, error) {
	if _, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceDeveloper); err != nil {
		return domain.SiteDomain{}, err
	}
	d, err := NormalizeDomain(name)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	if err := s.checkHostable(ctx, d); err != nil {
		return domain.SiteDomain{}, err
	}
	existing, err := s.Store.ListDomains(ctx, id)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	if len(existing) >= MaxDomainsPerSite {
		return domain.SiteDomain{}, domain.Invalid(fmt.Sprintf("a site can have at most %d domains", MaxDomainsPerSite))
	}
	sd := domain.SiteDomain{Domain: d, SiteID: id, Token: randHex(16), CreatedAtMS: s.now().UnixMilli()}
	if err := s.Store.ClaimDomain(ctx, sd); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.SiteDomain{}, domain.Invalid(d + " is already verified for another site on this panel")
		}
		return domain.SiteDomain{}, err
	}
	// Only a success is recorded here: a brand-new domain has not failed yet.
	ok, via, _, err := s.proveSiteDomain(ctx, actor, sd)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	if ok {
		if err := s.recordSiteDomainProof(ctx, d, true, via, ""); err != nil {
			return domain.SiteDomain{}, err
		}
		s.reloadSoon()
	}
	return s.Store.GetDomain(ctx, id, d)
}

// VerifyDomain checks the DNS records now and, when one matches, starts
// serving the domain.
func (s *SiteService) VerifyDomain(ctx context.Context, actor domain.User, id, name string) (domain.SiteDomain, error) {
	if _, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceDeveloper); err != nil {
		return domain.SiteDomain{}, err
	}
	d, err := s.Store.GetDomain(ctx, id, strings.ToLower(name))
	if err != nil {
		return domain.SiteDomain{}, err
	}
	ok, via, msg, err := s.proveSiteDomain(ctx, actor, d)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	// A base domain added after this claim owns every name below it.
	if base, err := s.underBaseDomain(ctx, d.Domain); err != nil {
		return domain.SiteDomain{}, err
	} else if base != "" {
		ok, msg = false, d.Domain+" is under the sites domain "+base+" and cannot be a custom domain; remove it"
	}
	if err := s.recordSiteDomainProof(ctx, d.Domain, ok, via, msg); err != nil {
		return domain.SiteDomain{}, err
	}
	s.reloadSoon()
	return s.Store.GetDomain(ctx, id, d.Domain)
}

// recheckUserDomains re-checks every verified user domain's record. A domain
// proven only through a verified parent follows that parent.
func (s *SiteService) recheckUserDomains(ctx context.Context) {
	list, err := s.Store.ListVerifiedUserDomains(ctx)
	if err != nil {
		s.warn("list user domains", err)
		return
	}
	for _, ud := range list {
		if ctx.Err() != nil {
			return
		}
		tok, err := s.userToken(ctx, ud.UserID)
		if err != nil {
			s.warn("user domain token", err)
			continue
		}
		found, msg := s.findRecord(ctx, ud.Domain, domainTXTValue+tok, "")
		var errMsg *string
		if found == "" {
			errMsg = &msg
		}
		if err := s.Store.RecordUserDomainCheck(ctx, ud.ID, nil, errMsg, s.now().UnixMilli()); err != nil {
			s.warn("record user domain check", err)
		}
	}
}

// recheckSiteDomain re-checks a verified custom domain: through the user
// domain that proved it (re-checked just before), else its own record.
func (s *SiteService) recheckSiteDomain(ctx context.Context, d domain.SiteDomain) (bool, string) {
	if d.UserDomainID != nil {
		ud, err := s.Store.GetUserDomain(ctx, *d.UserDomainID)
		if err == nil {
			if ud.LastError != nil {
				return false, *ud.LastError
			}
			return true, ""
		}
	}
	name, value := VerificationRecord(d)
	return s.checkTXT(ctx, name, value)
}
