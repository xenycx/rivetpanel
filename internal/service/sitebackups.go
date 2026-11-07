package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/siterules"
)

// Backups of files sites (uploaded or edited; no app unit). A backup is a
// tar.gz of the serving release, kept under <BackupDir>/sites/<site>/ outside
// the sites directory, so it survives the loss of a release (only the newest
// five releases are kept, and the installation backup does not copy site
// files). Restoring publishes the archive as a new release; the current one
// stays available for rollback.

// siteManualBackups bounds the backups people create by hand per site.
const siteManualBackups = 10

// siteBackupTick is how often the schedule looks for sites that are due.
const siteBackupTick = 10 * time.Minute

// SiteBackupStore is the storage site backups need (the panel's database).
type SiteBackupStore interface {
	SetSiteAutoBackup(ctx context.Context, siteID string, on bool, nowMS int64) error
	InsertSiteBackup(ctx context.Context, b domain.SiteBackup) error
	ListSiteBackups(ctx context.Context, siteID string) ([]domain.SiteBackup, error)
	GetSiteBackup(ctx context.Context, siteID, id string) (domain.SiteBackup, error)
	DeleteSiteBackup(ctx context.Context, siteID, id string) error
}

// SiteBackupHealth is what a site's Backups card shows.
type SiteBackupHealth struct {
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
	IntervalMS    int64  `json:"interval_ms"` // 0: automatic backups are off on this panel
	Keep          int    `json:"keep"`
	ManualLimit   int    `json:"manual_limit"`
	Enabled       bool   `json:"enabled"` // this site takes part in the schedule
	UpToDate      bool   `json:"up_to_date"`
	LastSuccessMS int64  `json:"last_success_ms"`
	NextDueMS     int64  `json:"next_due_ms"`
	Count         int    `json:"count"`
	TotalBytes    int64  `json:"total_bytes"`
}

func (s *SiteService) backupStore() (SiteBackupStore, error) {
	bs, ok := s.Store.(SiteBackupStore)
	if !ok || s.BackupDir == "" {
		return nil, domain.Invalid("site backups are not available on this panel")
	}
	return bs, nil
}

func (s *SiteService) siteBackupDir(siteID string) string {
	return filepath.Join(s.BackupDir, "sites", siteID)
}

func (s *SiteService) siteBackupPath(b domain.SiteBackup) string {
	return filepath.Join(s.siteBackupDir(b.SiteID), b.ID+".tar.gz")
}

// backupFilesSite refuses sites whose backups live elsewhere.
func backupFilesSite(st domain.Site) error {
	switch {
	case st.AppID != nil:
		return domain.Invalid("this site is backed up as its app: use its Backups tab")
	case st.Mode != domain.SiteModeFiles:
		return domain.Invalid("a generated bot page has no files of its own to back up")
	}
	return nil
}

// SiteBackups lists a site's backups with its backup health.
func (s *SiteService) SiteBackups(ctx context.Context, actor domain.User, id string) ([]domain.SiteBackup, SiteBackupHealth, error) {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceViewer)
	if err != nil {
		return nil, SiteBackupHealth{}, err
	}
	h := SiteBackupHealth{IntervalMS: s.BackupInterval.Milliseconds(), Keep: s.backupKeep(), ManualLimit: siteManualBackups, Enabled: !st.AutoBackupOff}
	if err := backupFilesSite(st); err != nil {
		h.Reason = err.Error()
		return []domain.SiteBackup{}, h, nil
	}
	bs, err := s.backupStore()
	if err != nil {
		h.Reason = err.Error()
		return []domain.SiteBackup{}, h, nil
	}
	h.Available = true
	list, err := bs.ListSiteBackups(ctx, st.ID)
	if err != nil {
		return nil, h, err
	}
	var lastAuto int64
	for _, b := range list {
		h.Count++
		h.TotalBytes += b.Bytes
		h.LastSuccessMS = max(h.LastSuccessMS, b.CreatedAtMS)
		if b.Kind == "auto" {
			lastAuto = max(lastAuto, b.CreatedAtMS)
		}
	}
	h.UpToDate = len(list) > 0 && st.CurrentRelease != nil && list[0].ReleaseID == *st.CurrentRelease
	if h.IntervalMS > 0 && h.Enabled && !h.UpToDate && st.CurrentRelease != nil {
		h.NextDueMS = s.now().UnixMilli()
		if lastAuto > 0 {
			h.NextDueMS = max(h.NextDueMS, lastAuto+h.IntervalMS)
		}
	}
	return list, h, nil
}

func (s *SiteService) backupKeep() int {
	if s.BackupKeep > 0 {
		return s.BackupKeep
	}
	return 7
}

// CreateSiteBackup archives the serving release now (site developers).
func (s *SiteService) CreateSiteBackup(ctx context.Context, actor domain.User, id string, label string) (domain.SiteBackup, error) {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceDeveloper)
	if err != nil {
		return domain.SiteBackup{}, err
	}
	if err := backupFilesSite(st); err != nil {
		return domain.SiteBackup{}, err
	}
	bs, err := s.backupStore()
	if err != nil {
		return domain.SiteBackup{}, err
	}
	label = strings.TrimSpace(label)
	if len(label) > 100 || strings.ContainsAny(label, "\x00\r\n") {
		return domain.SiteBackup{}, domain.Invalid("the label must be one line of at most 100 characters")
	}
	list, err := bs.ListSiteBackups(ctx, st.ID)
	if err != nil {
		return domain.SiteBackup{}, err
	}
	manual := 0
	for _, b := range list {
		if b.Kind == "manual" {
			manual++
		}
	}
	if manual >= siteManualBackups {
		return domain.SiteBackup{}, domain.Invalid(fmt.Sprintf("a site keeps at most %d manual backups; delete an older one first", siteManualBackups))
	}
	var lp *string
	if label != "" {
		lp = &label
	}
	return s.createSiteBackup(ctx, bs, st, "manual", lp, &actor.ID)
}

// createSiteBackup writes the archive of a site's serving release and
// records it. One backup runs at a time.
func (s *SiteService) createSiteBackup(ctx context.Context, bs SiteBackupStore, st domain.Site, kind string, label, actorID *string) (domain.SiteBackup, error) {
	if st.CurrentRelease == nil {
		return domain.SiteBackup{}, domain.Invalid("nothing is published yet, so there is nothing to back up")
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if err := diskPreflight(s.root, s.MinFreeDisk); err != nil {
		return domain.SiteBackup{}, err
	}
	m, err := filesystem.NewManager(filepath.Join(s.Dir, st.ID))
	if err != nil {
		return domain.SiteBackup{}, err
	}
	defer m.Close()
	src, err := m.Open(*st.CurrentRelease)
	if err != nil {
		return domain.SiteBackup{}, fmt.Errorf("open the serving release: %w", err)
	}
	defer src.Close()
	files, _, err := src.Usage()
	if err != nil {
		return domain.SiteBackup{}, err
	}
	dir := s.siteBackupDir(st.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return domain.SiteBackup{}, err
	}
	b := domain.SiteBackup{ID: uuid.NewString(), SiteID: st.ID, Kind: kind, ReleaseID: *st.CurrentRelease, Label: label, Files: files,
		CreatedBy: actorID, CreatedAtMS: s.now().UnixMilli()}
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return domain.SiteBackup{}, err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	_, lim := s.limits()
	if _, _, err := src.WriteTarGz(io.MultiWriter(tmp, h), nil, lim); err != nil {
		return domain.SiteBackup{}, fmt.Errorf("archive the release: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return domain.SiteBackup{}, err
	}
	fi, err := tmp.Stat()
	if err != nil {
		return domain.SiteBackup{}, err
	}
	if err := tmp.Close(); err != nil {
		return domain.SiteBackup{}, err
	}
	b.Bytes, b.SHA256 = fi.Size(), hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(tmp.Name(), s.siteBackupPath(b)); err != nil {
		return domain.SiteBackup{}, err
	}
	done = true
	if err := bs.InsertSiteBackup(context.WithoutCancel(ctx), b); err != nil {
		_ = os.Remove(s.siteBackupPath(b))
		return domain.SiteBackup{}, err
	}
	return b, nil
}

// OpenSiteBackup opens a backup archive for download (site developers).
func (s *SiteService) OpenSiteBackup(ctx context.Context, actor domain.User, id, backupID string) (*os.File, domain.SiteBackup, error) {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceDeveloper)
	if err != nil {
		return nil, domain.SiteBackup{}, err
	}
	bs, err := s.backupStore()
	if err != nil {
		return nil, domain.SiteBackup{}, err
	}
	b, err := bs.GetSiteBackup(ctx, st.ID, backupID)
	if err != nil {
		return nil, domain.SiteBackup{}, err
	}
	f, err := os.Open(s.siteBackupPath(b))
	if errors.Is(err, os.ErrNotExist) {
		return nil, b, domain.Invalid("the backup file is missing on the server")
	}
	return f, b, err
}

// RestoreSiteBackup publishes a backup as a new release (site developers,
// like publishing). The release served before stays available for rollback.
func (s *SiteService) RestoreSiteBackup(ctx context.Context, actor domain.User, id, backupID string) (domain.SiteRelease, error) {
	f, b, err := s.OpenSiteBackup(ctx, actor, id, backupID)
	if err != nil {
		return domain.SiteRelease{}, err
	}
	defer f.Close()
	st, err := s.Store.GetSite(ctx, b.SiteID)
	if err != nil {
		return domain.SiteRelease{}, err
	}
	if err := backupFilesSite(st); err != nil {
		return domain.SiteRelease{}, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return domain.SiteRelease{}, err
	}
	if b.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) != b.SHA256 {
		return domain.SiteRelease{}, domain.Invalid("the backup file is damaged (its checksum does not match)")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return domain.SiteRelease{}, err
	}
	rel, w, done, err := s.newRelease(st.ID)
	if err != nil {
		return domain.SiteRelease{}, err
	}
	ok := false
	defer func() { done(ok) }()
	_, lim := s.limits()
	if _, err := w.RestoreTarGz(f, lim); err != nil {
		var ae *filesystem.ErrArchive
		if errors.As(err, &ae) {
			return domain.SiteRelease{}, domain.Invalid("the backup cannot be restored: " + ae.Msg)
		}
		return domain.SiteRelease{}, err
	}
	_ = w.Remove(siterules.Dir) // generated at publish; rebuilt below
	label := "Restored from the backup of " + time.UnixMilli(b.CreatedAtMS).UTC().Format("2006-01-02 15:04 UTC")
	r, err := s.finishRelease(ctx, st, rel, w, "upload", &label, &actor.ID, nil, false)
	if err != nil {
		return domain.SiteRelease{}, err
	}
	ok = true
	return r, nil
}

// DeleteSiteBackup removes a backup (site administrators).
func (s *SiteService) DeleteSiteBackup(ctx context.Context, actor domain.User, id, backupID string) error {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceAdmin)
	if err != nil {
		return err
	}
	bs, err := s.backupStore()
	if err != nil {
		return err
	}
	b, err := bs.GetSiteBackup(ctx, st.ID, backupID)
	if err != nil {
		return err
	}
	if err := bs.DeleteSiteBackup(ctx, st.ID, b.ID); err != nil {
		return err
	}
	if err := os.Remove(s.siteBackupPath(b)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.warn("remove site backup file", err)
	}
	return nil
}

// SetSiteAutoBackup turns a site's scheduled backups on or off (site
// administrators). Existing backups are kept.
func (s *SiteService) SetSiteAutoBackup(ctx context.Context, actor domain.User, id string, on bool) (domain.Site, error) {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceAdmin)
	if err != nil {
		return domain.Site{}, err
	}
	if err := backupFilesSite(st); err != nil {
		return domain.Site{}, err
	}
	bs, err := s.backupStore()
	if err != nil {
		return domain.Site{}, err
	}
	if err := bs.SetSiteAutoBackup(ctx, st.ID, on, s.now().UnixMilli()); err != nil {
		return domain.Site{}, err
	}
	return s.Store.GetSite(ctx, st.ID)
}

// RunScheduledBackups backs up every files site that is due: automatic
// backups on, something published, the serving release not backed up yet,
// and the newest automatic backup older than the interval. Old automatic
// backups beyond the retention are removed.
func (s *SiteService) RunScheduledBackups(ctx context.Context) {
	if s.BackupInterval <= 0 {
		return
	}
	bs, err := s.backupStore()
	if err != nil {
		return
	}
	sites, err := s.Store.ListAllSites(ctx)
	if err != nil {
		s.warn("scheduled site backups: list sites", err)
		return
	}
	for _, st := range sites {
		if ctx.Err() != nil {
			return
		}
		if !st.BacksUpFiles() || st.AutoBackupOff || st.CurrentRelease == nil {
			continue
		}
		list, err := bs.ListSiteBackups(ctx, st.ID)
		if err != nil {
			continue
		}
		if len(list) > 0 && list[0].ReleaseID == *st.CurrentRelease {
			continue // the serving release is already backed up
		}
		var lastAuto int64
		for _, b := range list {
			if b.Kind == "auto" {
				lastAuto = max(lastAuto, b.CreatedAtMS)
			}
		}
		if lastAuto > 0 && s.now().Sub(time.UnixMilli(lastAuto)) < s.BackupInterval {
			continue
		}
		if _, err := s.createSiteBackup(ctx, bs, st, "auto", nil, nil); err != nil {
			s.warn("scheduled site backup", err)
			continue
		}
		s.trimSiteBackups(ctx, bs, st.ID)
	}
}

// trimSiteBackups keeps the newest automatic backups of a site.
func (s *SiteService) trimSiteBackups(ctx context.Context, bs SiteBackupStore, siteID string) {
	list, err := bs.ListSiteBackups(ctx, siteID)
	if err != nil {
		return
	}
	kept := 0
	for _, b := range list {
		if b.Kind != "auto" {
			continue
		}
		if kept++; kept <= s.backupKeep() {
			continue
		}
		if err := bs.DeleteSiteBackup(ctx, siteID, b.ID); err == nil {
			_ = os.Remove(s.siteBackupPath(b))
		}
	}
}

// removeSiteBackups deletes a deleted site's archives (its rows cascade).
func (s *SiteService) removeSiteBackups(siteID string) {
	if s.BackupDir == "" {
		return
	}
	if u, err := uuid.Parse(siteID); err != nil || u.String() != siteID {
		return
	}
	if err := os.RemoveAll(s.siteBackupDir(siteID)); err != nil {
		s.warn("remove site backups", err)
	}
}
