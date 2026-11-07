package api

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Backups of files sites (uploaded or edited sites without an app unit).

type siteBackupDTO struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	ReleaseID   string  `json:"release_id"`
	Label       *string `json:"label"`
	Files       int     `json:"files"`
	Bytes       int64   `json:"bytes"`
	SHA256      string  `json:"sha256"`
	CreatedAtMS int64   `json:"created_at_ms"`
}

func toSiteBackup(b domain.SiteBackup) siteBackupDTO {
	return siteBackupDTO{ID: b.ID, Kind: b.Kind, ReleaseID: b.ReleaseID, Label: b.Label, Files: b.Files, Bytes: b.Bytes,
		SHA256: b.SHA256, CreatedAtMS: b.CreatedAtMS}
}

func (s *panel) siteBackupRoutes(authed fiber.Router) {
	authed.Get("/sites/:sid/backups", s.listSiteBackups)
	authed.Post("/sites/:sid/backups", s.createSiteBackup)
	authed.Put("/sites/:sid/auto-backup", s.setSiteAutoBackup)
	authed.Get("/sites/:sid/backups/:bid/download", s.downloadSiteBackup)
	authed.Post("/sites/:sid/backups/:bid/restore", s.restoreSiteBackup)
	authed.Delete("/sites/:sid/backups/:bid", s.deleteSiteBackup)
}

func (s *panel) listSiteBackups(c fiber.Ctx) error {
	list, h, err := s.sites.SiteBackups(c.Context(), currentUser(c), strings.Clone(c.Params("sid")))
	if err != nil {
		return err
	}
	out := make([]siteBackupDTO, len(list))
	for i, b := range list {
		out[i] = toSiteBackup(b)
	}
	return c.JSON(fiber.Map{"backups": out, "health": h})
}

func (s *panel) createSiteBackup(c fiber.Ctx) error {
	var in struct {
		Label string `json:"label"`
	}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	b, err := s.sites.CreateSiteBackup(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), in.Label)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(toSiteBackup(b))
}

func (s *panel) setSiteAutoBackup(c fiber.Ctx) error {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Enabled == nil {
		return domain.Invalid("enabled is required")
	}
	st, err := s.sites.SetSiteAutoBackup(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), *in.Enabled)
	if err != nil {
		return err
	}
	return c.JSON(s.toSite(st))
}

func (s *panel) downloadSiteBackup(c fiber.Ctx) error {
	sid := strings.Clone(c.Params("sid"))
	f, b, err := s.sites.OpenSiteBackup(c.Context(), currentUser(c), sid, strings.Clone(c.Params("bid")))
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	name := "site"
	if site, err := s.sites.Get(c.Context(), currentUser(c), sid); err == nil {
		if n := strings.Trim(unsafeFile.ReplaceAllString(site.Site.Name, "_"), "._-"); n != "" {
			name = n
		}
	}
	c.Set(fiber.HeaderContentType, "application/gzip")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+name+"-"+time.UnixMilli(b.CreatedAtMS).UTC().Format("20060102-150405")+`.tar.gz"`)
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	return c.SendStream(f, int(st.Size()))
}

func (s *panel) restoreSiteBackup(c fiber.Ctx) error {
	r, err := s.sites.RestoreSiteBackup(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), strings.Clone(c.Params("bid")))
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"release_id": r.ID, "files": r.Files, "bytes": r.Bytes})
}

func (s *panel) deleteSiteBackup(c fiber.Ctx) error {
	if err := s.sites.DeleteSiteBackup(c.Context(), currentUser(c), strings.Clone(c.Params("sid")), strings.Clone(c.Params("bid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
