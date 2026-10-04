package api

import (
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type backupDTO struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Status       string  `json:"status"`
	SizeBytes    int64   `json:"size_bytes"`
	SHA256       *string `json:"sha256"`
	IncludesEnv  bool    `json:"includes_env"`
	Error        *string `json:"error"`
	CreatedAtMS  int64   `json:"created_at_ms"`
	Label        *string `json:"label"`
	VerifiedAtMS *int64  `json:"verified_at_ms"`
	VerifyError  *string `json:"verify_error"`
	Consistent   bool    `json:"consistent"`
}

func toBackup(b domain.Backup) backupDTO {
	return backupDTO{b.ID, b.Kind, b.Status, b.SizeBytes, b.SHA256Hex, b.IncludesEnv, b.Error, b.CreatedAtMS,
		b.Label, b.VerifiedAtMS, b.VerifyError, b.Consistent}
}

func (s *panel) listBackups(c fiber.Ctx) error {
	h, bs, err := s.backups.Health(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	out := make([]backupDTO, len(bs))
	for i, b := range bs {
		out[i] = toBackup(b)
	}
	var lf *backupDTO
	if h.LastFailure != nil {
		d := toBackup(*h.LastFailure)
		lf = &d
	}
	return c.JSON(fiber.Map{"backups": out, "health": fiber.Map{
		"interval_ms": h.IntervalMS, "keep": h.Keep, "enabled": h.Enabled, "last_success_ms": h.LastSuccessMS,
		"last_scheduled_ms": h.LastScheduledMS, "next_due_ms": h.NextDueMS, "last_failure": lf,
		"total_bytes": h.TotalBytes, "count": h.Count, "limit": h.Limit, "manual_limit": h.ManualLimit,
	}})
}

func (s *panel) patchBackup(c fiber.Ctx) error {
	var in struct {
		Label string `json:"label"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.backups.SetLabel(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("bid")), in.Label)
	if err != nil {
		return err
	}
	return c.JSON(toBackup(b))
}

func (s *panel) verifyBackup(c fiber.Ctx) error {
	b, err := s.backups.Verify(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("bid")))
	if err != nil {
		return err
	}
	return c.JSON(toBackup(b))
}

func (s *panel) createBackup(c fiber.Ctx) error {
	in := struct {
		IncludeEnv *bool  `json:"include_env"`
		Label      string `json:"label"`
		Consistent bool   `json:"consistent"`
	}{}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	inc := in.IncludeEnv == nil || *in.IncludeEnv
	b, err := s.backups.Create(c.Context(), currentUser(c), strings.Clone(c.Params("id")),
		service.CreateOptions{IncludeEnv: inc, Label: in.Label, Consistent: in.Consistent})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(toBackup(b))
}

var unsafeFile = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// downloadBackup streams the archive as an attachment. It is served as opaque
// gzip data, never as something a browser would render.
func (s *panel) downloadBackup(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	f, b, err := s.backups.Open(c.Context(), currentUser(c), id, strings.Clone(c.Params("bid")))
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	bot, err := s.bots.Get(c.Context(), currentUser(c), id)
	name := "bot"
	if err == nil {
		name = strings.Trim(unsafeFile.ReplaceAllString(bot.Name, "_"), "._-")
		if name == "" {
			name = "bot"
		}
	}
	c.Set(fiber.HeaderContentType, "application/gzip")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+name+"-"+time.UnixMilli(b.CreatedAtMS).UTC().Format("20060102-150405")+`.tar.gz"`)
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	return c.SendStream(f, int(st.Size()))
}

func (s *panel) restoreBackup(c fiber.Ctx) error {
	in := struct {
		RestoreEnv *bool `json:"restore_env"`
	}{}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	env := in.RestoreEnv == nil || *in.RestoreEnv
	b, err := s.backups.Restore(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("bid")), service.RestoreOptions{RestoreEnv: env})
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) deleteBackup(c fiber.Ctx) error {
	if err := s.backups.Delete(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("bid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
