package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// userDomainDTO is one of the caller's domains. The TXT record holds the
// caller's own token; Uses counts the custom domains it proved.
type userDomainDTO struct {
	ID              string  `json:"id"`
	Domain          string  `json:"domain"`
	Verified        bool    `json:"verified"`
	VerifiedAtMS    *int64  `json:"verified_at_ms"`
	LastCheckedAtMS *int64  `json:"last_checked_at_ms"`
	LastError       *string `json:"last_error"`
	TXTName         string  `json:"txt_name"`
	TXTValue        string  `json:"txt_value"`
	ExampleURL      string  `json:"example_url"`
	Uses            int     `json:"uses"`
	Default         bool    `json:"default"`
	// Addresses are the site addresses this domain verified.
	Addresses   []userDomainUseDTO `json:"addresses"`
	CreatedAtMS int64              `json:"created_at_ms"`
}

type userDomainUseDTO struct {
	Domain   string `json:"domain"`
	URL      string `json:"url"`
	SiteID   string `json:"site_id"`
	SiteName string `json:"site_name"`
}

func (s *panel) toUserDomain(c fiber.Ctx, d domain.UserDomain, token string) userDomainDTO {
	name, value := service.DomainVerificationRecord(d.Domain, token)
	uses := s.sites.UserDomainUses(c.Context(), d.ID)
	addrs := make([]userDomainUseDTO, len(uses))
	for i, u := range uses {
		addrs[i] = userDomainUseDTO{u.Domain, s.sites.DomainURL(u.Domain), u.SiteID, u.SiteName}
	}
	return userDomainDTO{d.ID, d.Domain, d.VerifiedAtMS != nil, d.VerifiedAtMS, d.LastCheckedAtMS, d.LastError, name, value,
		s.sites.DomainURL("example." + d.Domain), len(uses), d.Default, addrs, d.CreatedAtMS}
}

func (s *panel) userDomainsJSON(c fiber.Ctx) ([]userDomainDTO, error) {
	u := currentUser(c)
	list, err := s.sites.ListUserDomains(c.Context(), u)
	if err != nil {
		return nil, err
	}
	tok, err := s.sites.UserDomainToken(c.Context(), u)
	if err != nil {
		return nil, err
	}
	out := make([]userDomainDTO, len(list))
	for i, d := range list {
		out[i] = s.toUserDomain(c, d, tok)
	}
	return out, nil
}

func (s *panel) listUserDomains(c fiber.Ctx) error {
	out, err := s.userDomainsJSON(c)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"domains": out, "max": service.MaxUserDomains})
}

func (s *panel) addUserDomain(c fiber.Ctx) error {
	var in struct {
		Domain string `json:"domain"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	d, err := s.sites.AddUserDomain(c.Context(), currentUser(c), in.Domain)
	if err != nil {
		return err
	}
	tok, err := s.sites.UserDomainToken(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.toUserDomain(c, d, tok))
}

func (s *panel) verifyUserDomain(c fiber.Ctx) error {
	d, err := s.sites.VerifyUserDomain(c.Context(), currentUser(c), strings.Clone(c.Params("did")))
	if err != nil {
		return err
	}
	tok, err := s.sites.UserDomainToken(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(s.toUserDomain(c, d, tok))
}

func (s *panel) removeUserDomain(c fiber.Ctx) error {
	if err := s.sites.RemoveUserDomain(c.Context(), currentUser(c), strings.Clone(c.Params("did"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// updateUserDomain changes a domain's settings: {default} makes it the one
// new sites start on.
func (s *panel) updateUserDomain(c fiber.Ctx) error {
	var in struct {
		Default *bool `json:"default"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Default == nil {
		return domain.Invalid("nothing to change")
	}
	d, err := s.sites.SetDefaultUserDomain(c.Context(), currentUser(c), strings.Clone(c.Params("did")), *in.Default)
	if err != nil {
		return err
	}
	tok, err := s.sites.UserDomainToken(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(s.toUserDomain(c, d, tok))
}
