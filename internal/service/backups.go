package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// MaxBackupsPerBot bounds stored backups per bot (all kinds).
const MaxBackupsPerBot = 50

// BackupService creates, lists, serves, restores and prunes per-bot snapshots.
//
// An archive holds the bot's files (minus rebuildable directories) and, unless
// disabled, its environment as SEALED rows: names plus ciphertext, never
// plaintext. Those rows can only be opened with this server's key and the same
// bot ID, so a downloaded archive does not expose secrets, and restore is only
// meaningful on the same bot.
type BackupService struct {
	Bots     *BotService
	Files    *filesystem.Manager
	Remote   RemoteArchives // optional: workspace archives for agent nodes
	Keys     *secrets.Keyring
	Dir      string // <dir>/<bot_id>/<backup_id>.tar.gz
	Limits   filesystem.BackupLimits
	Keep     int           // scheduled backups kept per bot
	Interval time.Duration // scheduled backup period; 0 disables
	Log      *slog.Logger
	Now      func() time.Time
	Ops      *Operations   // optional: backup/restore history
	Alerts   *AlertService // optional: failure notifications
	// MinFreeDisk is the free space a backup or restore needs to start.
	MinFreeDisk int64

	sem     chan struct{}
	wg      sync.WaitGroup
	baseCtx context.Context
}

// RemoteArchives is the authenticated, node-routed archive surface. A remote
// restore remains reversible until the panel records the matching database
// change and explicitly completes the transaction.
type RemoteArchives interface {
	Backup(ctx context.Context, nodeID, botID string, extra map[string][]byte, limits filesystem.BackupLimits, dst io.Writer) error
	BeginRestore(ctx context.Context, nodeID, botID string, src io.Reader, limits filesystem.BackupLimits) (transaction string, err error)
	CompleteRestore(ctx context.Context, nodeID, botID, transaction string, commit bool) error
}

func (s *BackupService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *BackupService) store() Store { return s.Bots.Store }

func (s *BackupService) botDir(botID string) string { return filepath.Join(s.Dir, botID) }

func (s *BackupService) init() {
	if s.sem == nil {
		s.sem = make(chan struct{}, 1) // one archive at a time keeps CPU and IO bounded
	}
	if s.Limits.MaxBytes == 0 {
		s.Limits = filesystem.DefaultBackupLimits
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
}

// Start prepares the directory, fails backups a crash left half-done, sweeps
// backups of deleted bots and, if configured, runs the scheduler until ctx ends.
func (s *BackupService) Start(ctx context.Context) error {
	s.init()
	s.baseCtx = ctx
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("backup directory: %w", err)
	}
	if n, err := s.store().FailStaleBackups(ctx); err != nil {
		return err
	} else if n > 0 {
		s.Log.Warn("marked interrupted backups as failed", "count", n)
	}
	s.sweepOrphans(ctx)
	go s.loop(ctx)
	return nil
}

// Wait blocks until running backup jobs finish (used on shutdown and in tests).
func (s *BackupService) Wait() { s.wg.Wait() }

func (s *BackupService) loop(ctx context.Context) {
	tick := s.Interval / 12
	if tick < time.Minute {
		tick = time.Minute
	}
	if tick > time.Hour {
		tick = time.Hour
	}
	sweep := time.NewTicker(24 * time.Hour)
	defer sweep.Stop()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		if s.Interval > 0 {
			s.runScheduled(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			s.sweepOrphans(ctx)
		case <-t.C:
		}
	}
}

// runScheduled backs up every bot whose newest scheduled backup is older than
// the interval, then trims old scheduled backups.
func (s *BackupService) runScheduled(ctx context.Context) {
	bots, err := s.store().ListBots(ctx, "")
	if err != nil {
		s.Log.Warn("scheduled backups: list bots", "err", err)
		return
	}
	// Bots with their own backup schedule are backed up by it, not here.
	var own map[string]bool
	if bs, ok := s.store().(interface {
		BotsWithBackupSchedule(context.Context) (map[string]bool, error)
	}); ok {
		own, _ = bs.BotsWithBackupSchedule(ctx)
	}
	for _, b := range bots {
		if ctx.Err() != nil {
			return
		}
		if b.DesiredState == domain.DesiredDeleted || b.AutoBackupOff || own[b.ID] {
			continue
		}
		last, err := s.store().LastBackupAtMS(ctx, b.ID, "auto")
		if err != nil || (last != 0 && s.now().Sub(time.UnixMilli(last)) < s.Interval) {
			continue
		}
		if err := diskPreflight(s.Files, s.MinFreeDisk); err != nil {
			s.Log.Warn("scheduled backups skipped: low disk space")
			return
		}
		claim, cctx, err := s.Bots.Coord.Claim(ctx, b.ID, "A backup", false)
		if err != nil {
			continue // busy with a deployment or restore: the next tick retries
		}
		row, err := s.begin(cctx, b, "auto", true, nil)
		if err != nil {
			claim.Release()
			s.Log.Warn("scheduled backup", "bot", b.ID, "err", err)
			continue
		}
		op := s.Ops.Begin(cctx, OpStart{BotID: b.ID, Kind: domain.OpBackup, Trigger: "schedule", SourceRef: &row.ID})
		s.execute(cctx, row, true, op) // synchronous: one at a time
		claim.Release()
		s.trim(ctx, b.ID)
	}
}

func (s *BackupService) trim(ctx context.Context, botID string) {
	if s.Keep <= 0 {
		return
	}
	old, err := s.store().ListBackupsByKind(ctx, botID, "auto")
	if err != nil || len(old) <= s.Keep {
		return
	}
	for _, b := range old[s.Keep:] {
		s.remove(ctx, b)
	}
}

func (s *BackupService) sweepOrphans(ctx context.Context) {
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	ids, err := s.store().BotIDs(ctx)
	if err != nil {
		return
	}
	for _, e := range ents {
		if u, err := uuid.Parse(e.Name()); err != nil || u.String() != e.Name() || ids[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.Dir, e.Name())); err != nil {
			s.Log.Warn("remove backups of deleted bot", "bot", e.Name(), "err", err)
		}
	}
}

// PurgeBot deletes a bot's backup files (its rows go with the bot).
func (s *BackupService) PurgeBot(botID string) {
	if u, err := uuid.Parse(botID); err != nil || u.String() != botID {
		return
	}
	if err := os.RemoveAll(s.botDir(botID)); err != nil {
		s.Log.Warn("remove backups of deleted bot", "bot", botID, "err", err)
	}
}

// begin records a 'creating' backup row after checking the per-bot cap.
func (s *BackupService) begin(ctx context.Context, b domain.Bot, kind string, includeEnv bool, createdBy *string) (domain.Backup, error) {
	return s.beginWith(ctx, b, kind, includeEnv, createdBy, nil, kind == "pre_restore")
}

func (s *BackupService) beginWith(ctx context.Context, b domain.Bot, kind string, includeEnv bool, createdBy *string, label *string, consistent bool) (domain.Backup, error) {
	s.init()
	existing, err := s.store().ListBackups(ctx, b.ID)
	if err != nil {
		return domain.Backup{}, err
	}
	// Manual backups may fill only part of the budget, so scheduled and
	// safety (pre-restore) backups always have room. Those make room by
	// removing their own oldest copies, never the newest ready backup.
	if kind == "manual" && countKind(existing, "manual") >= MaxBackupsPerBot-reservedSlots {
		return domain.Backup{}, domain.Invalid(fmt.Sprintf("manual backups are limited to %d per bot; delete an old one first", MaxBackupsPerBot-reservedSlots))
	}
	if len(existing) >= MaxBackupsPerBot {
		if !s.makeRoom(ctx, existing, kind) {
			return domain.Backup{}, domain.Invalid(fmt.Sprintf("this bot already keeps %d backups; delete old ones so scheduled backups can continue", MaxBackupsPerBot))
		}
	}
	for _, e := range existing {
		if e.Status == "creating" && kind != "pre_restore" {
			return domain.Backup{}, domain.Invalid("a backup of this bot is already in progress")
		}
	}
	id := uuid.NewString()
	row := domain.Backup{ID: id, BotID: b.ID, Kind: kind, Status: "creating", FileName: id + ".tar.gz",
		IncludesEnv: includeEnv, CreatedBy: createdBy, CreatedAtMS: s.now().UnixMilli(), Label: label, Consistent: consistent}
	if err := s.store().InsertBackup(ctx, row); err != nil {
		return domain.Backup{}, err
	}
	return row, nil
}

// reservedSlots of the per-bot budget are kept for scheduled and safety backups.
const reservedSlots = 5

func countKind(bs []domain.Backup, kind string) int {
	n := 0
	for _, b := range bs {
		if b.Kind == kind {
			n++
		}
	}
	return n
}

// makeRoom removes the oldest finished backup of kind (auto or pre_restore),
// keeping at least the newest ready one of that kind. existing is newest first.
func (s *BackupService) makeRoom(ctx context.Context, existing []domain.Backup, kind string) bool {
	if kind == "manual" {
		return false
	}
	keptReady := false
	var victim *domain.Backup
	for i := range existing {
		b := existing[i]
		if b.Kind != kind || b.Status == "creating" {
			continue
		}
		if b.Status == "ready" && !keptReady {
			keptReady = true
			continue
		}
		victim = &existing[i] // keeps moving to the oldest candidate
	}
	if victim == nil {
		return false
	}
	s.remove(ctx, *victim)
	return true
}

// CreateOptions describe a manual backup.
type CreateOptions struct {
	IncludeEnv bool
	Label      string
	// Consistent stops a running bot for the copy and then restores its
	// previous intent, so files are not changing while they are archived.
	Consistent bool
}

func cleanLabel(l string) (*string, error) {
	l = strings.TrimSpace(l)
	if l == "" {
		return nil, nil
	}
	if len([]rune(l)) > 80 || strings.ContainsAny(l, "\x00\n\r") {
		return nil, domain.Invalid("labels are at most 80 characters on one line")
	}
	return &l, nil
}

// Create starts a manual backup in the background and returns its row.
func (s *BackupService) Create(ctx context.Context, actor domain.User, botID string, opt CreateOptions) (domain.Backup, error) {
	return s.create(ctx, actor, botID, opt, "manual", "manual")
}

// CreateScheduled starts a backup for a per-bot schedule. It counts as a
// scheduled ("auto") backup: it uses the reserved slots and is trimmed with
// the other scheduled copies.
func (s *BackupService) CreateScheduled(ctx context.Context, actor domain.User, botID string) (domain.Backup, error) {
	return s.create(ctx, actor, botID, CreateOptions{IncludeEnv: true}, "auto", "schedule")
}

func (s *BackupService) create(ctx context.Context, actor domain.User, botID string, opt CreateOptions, kind, trigger string) (domain.Backup, error) {
	b, err := s.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles)
	if err != nil {
		return domain.Backup{}, err
	}
	label, err := cleanLabel(opt.Label)
	if err != nil {
		return domain.Backup{}, err
	}
	if err := diskPreflight(s.Files, s.MinFreeDisk); err != nil {
		return domain.Backup{}, err
	}
	includeEnv := opt.IncludeEnv
	if opt.Consistent {
		if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermPower); err != nil {
			return domain.Backup{}, domain.Invalid("stopping the bot for the backup needs the start and stop permission")
		}
		if s.Bots.Notifier == nil {
			return domain.Backup{}, domain.ErrRunnerUnavailable
		}
	}
	base := s.baseCtx
	if base == nil {
		base = context.Background()
	}
	// A consistent copy holds the bot exclusively so nobody starts it or
	// edits files while it is stopped for the archive.
	claim, cctx, err := s.Bots.Coord.Claim(base, botID, "A backup", opt.Consistent)
	if err != nil {
		return domain.Backup{}, err
	}
	row, err := s.beginWith(ctx, b, kind, includeEnv, &actor.ID, label, opt.Consistent)
	if err != nil {
		claim.Release()
		return domain.Backup{}, err
	}
	op := s.Ops.Begin(ctx, OpStart{BotID: botID, Kind: domain.OpBackup, Trigger: trigger, ActorID: &actor.ID, SourceRef: &row.ID})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer claim.Release()
		if opt.Consistent {
			resume, err := s.stopForBackup(cctx, botID, op)
			if err != nil {
				msg := "the bot could not be stopped for the backup"
				fctx, cancel := bg(cctx)
				s.store().FinishBackup(fctx, row.ID, "failed", 0, nil, &msg)
				cancel()
				s.Ops.Finish(cctx, op, domain.OpFailed, "stop_failed", msg, nil)
				return
			}
			defer resume()
		}
		s.execute(cctx, row, includeEnv, op)
		if kind == "auto" {
			s.trim(cctx, botID)
		}
	}()
	return row, nil
}

type envSnapshot struct {
	Version int          `json:"version"`
	BotID   string       `json:"bot_id"`
	Vars    []envSnapVar `json:"vars"`
}

type envSnapVar struct {
	Name       string `json:"name"`
	Ciphertext string `json:"ciphertext"`
	Nonce      string `json:"nonce"`
	KeyID      string `json:"key_id"`
}

func (s *BackupService) snapshotEnv(ctx context.Context, botID string) ([]byte, error) {
	rows, err := s.store().ListEnv(ctx, botID)
	if err != nil {
		return nil, err
	}
	snap := envSnapshot{Version: 1, BotID: botID}
	for _, r := range rows {
		if strings.HasPrefix(strings.ToUpper(r.Name), "RIVET_") {
			continue // system-managed (telemetry key), regenerated on demand
		}
		snap.Vars = append(snap.Vars, envSnapVar{r.Name, base64.StdEncoding.EncodeToString(r.Ciphertext),
			base64.StdEncoding.EncodeToString(r.Nonce), r.KeyID})
	}
	return json.Marshal(snap)
}

// execute writes the archive and records the outcome. Errors shown to users
// are limited to safe archive messages.
func (s *BackupService) execute(ctx context.Context, row domain.Backup, includeEnv bool, op string) {
	s.init()
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		msg := "cancelled before it started"
		s.store().FinishBackup(context.WithoutCancel(ctx), row.ID, "failed", 0, nil, &msg)
		s.Ops.Finish(ctx, op, domain.OpCancelled, "cancelled", msg, nil)
		return
	}
	s.Ops.Stage(ctx, op, "Writing the archive")
	size, sum, err := s.write(ctx, row, includeEnv)
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err != nil {
		msg := "backup failed"
		var ae *filesystem.ErrArchive
		if errors.As(err, &ae) {
			msg = ae.Msg
		} else {
			s.Log.Warn("backup failed", "bot", row.BotID, "backup", row.ID, "err", err)
		}
		_ = s.store().FinishBackup(fctx, row.ID, "failed", 0, nil, &msg)
		s.Ops.Finish(fctx, op, domain.OpFailed, "backup_failed", msg, nil)
		if s.Alerts.Wants(fctx, row.BotID, "backup") {
			if b, err := s.store().GetBot(fctx, row.BotID); err == nil {
				s.Alerts.Notify(fctx, b.OwnerID, domain.NotifyBackups, "❌ Backup failed: "+b.Name, msg, BotLink(b))
			}
		}
		return
	}
	if err := s.store().FinishBackup(fctx, row.ID, "ready", size, &sum, nil); err != nil {
		s.Log.Warn("record backup", "backup", row.ID, "err", err)
	}
	s.Ops.Finish(fctx, op, domain.OpSucceeded, "", fmt.Sprintf("Backup saved (%s)", humanBytes(size)),
		map[string]any{"backup_id": row.ID, "size_bytes": size})
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func (s *BackupService) write(ctx context.Context, row domain.Backup, includeEnv bool) (int64, string, error) {
	dir := s.botDir(row.BotID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, "", err
	}
	final := filepath.Join(dir, row.FileName)
	tmp := final + ".partial"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	fail := func(err error) (int64, string, error) { f.Close(); os.Remove(tmp); return 0, "", err }
	extra := map[string][]byte{}
	if includeEnv {
		if extra["env.json"], err = s.snapshotEnv(ctx, row.BotID); err != nil {
			return fail(err)
		}
	}
	h := sha256.New()
	b, err := s.store().GetBot(ctx, row.BotID)
	if err != nil {
		return fail(err)
	}
	if s.Bots.remote(b.NodeID) {
		if s.Remote == nil {
			return fail(domain.ErrRunnerUnavailable)
		}
		if err := s.Remote.Backup(ctx, b.NodeID, row.BotID, extra, s.Limits, io.MultiWriter(f, h)); err != nil {
			return fail(err)
		}
	} else {
		w, err := s.Files.Open(row.BotID)
		if err != nil {
			return fail(err)
		}
		defer w.Close()
		if _, _, err := w.WriteTarGz(io.MultiWriter(f, h), extra, s.Limits); err != nil {
			return fail(err)
		}
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return 0, "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return 0, "", err
	}
	return st.Size(), hex.EncodeToString(h.Sum(nil)), nil
}

// List returns a bot's backups, newest first.
func (s *BackupService) List(ctx context.Context, actor domain.User, botID string) ([]domain.Backup, error) {
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles); err != nil {
		return nil, err
	}
	return s.store().ListBackups(ctx, botID)
}

// Open returns a ready backup's file for download.
func (s *BackupService) Open(ctx context.Context, actor domain.User, botID, id string) (*os.File, domain.Backup, error) {
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles); err != nil {
		return nil, domain.Backup{}, err
	}
	b, err := s.store().GetBackup(ctx, botID, id)
	if err != nil {
		return nil, b, err
	}
	if b.Status != "ready" {
		return nil, b, domain.Invalid("this backup is not available")
	}
	f, err := os.Open(filepath.Join(s.botDir(botID), b.FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, b, domain.ErrNotFound
	}
	return f, b, err
}

func (s *BackupService) remove(ctx context.Context, b domain.Backup) {
	if err := os.Remove(filepath.Join(s.botDir(b.BotID), b.FileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Log.Warn("remove backup file", "backup", b.ID, "err", err)
		return // keep the row so the file is not orphaned
	}
	_ = s.store().DeleteBackup(ctx, b.BotID, b.ID)
}

// Delete removes a backup (full-admin permission).
func (s *BackupService) Delete(ctx context.Context, actor domain.User, botID, id string) error {
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin); err != nil {
		return err
	}
	b, err := s.store().GetBackup(ctx, botID, id)
	if err != nil {
		return err
	}
	if b.Status == "creating" {
		return domain.Invalid("this backup is still being created")
	}
	if err := os.Remove(filepath.Join(s.botDir(botID), b.FileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.store().DeleteBackup(ctx, botID, id)
}

// RestoreOptions selects what a restore applies.
type RestoreOptions struct{ RestoreEnv bool }

// Restore replaces the bot's files (and optionally environment) with a backup.
// The bot must be stopped. Safety net, in order: the archive checksum is
// verified; the environment is checked to be decryptable BEFORE anything
// changes; a 'pre_restore' backup of the current state is taken; the archive
// is unpacked into staging and swapped in only when fully valid.
func (s *BackupService) Restore(ctx context.Context, actor domain.User, botID, id string, opt RestoreOptions) (bot domain.Bot, err error) {
	s.init()
	b, err := s.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin)
	if err != nil {
		return domain.Bot{}, err
	}
	if err := diskPreflight(s.Files, s.MinFreeDisk); err != nil {
		return domain.Bot{}, err
	}
	// The restore holds the bot exclusively: Start, file edits and
	// environment edits wait (409) until it finishes.
	claim, cctx, err := s.Bots.Coord.Claim(ctx, botID, "A restore", true)
	if err != nil {
		return domain.Bot{}, err
	}
	defer claim.Release()
	ctx = cctx
	// Re-read under the claim: a Start that won the race must stop us.
	if b, err = s.Bots.Store.GetBot(ctx, botID); err != nil {
		return domain.Bot{}, err
	}
	op := s.Ops.Begin(ctx, OpStart{BotID: botID, Kind: domain.OpRestore, Trigger: "manual", ActorID: &actor.ID, SourceRef: &id})
	defer func() {
		switch {
		case err == nil:
			s.Ops.Finish(ctx, op, domain.OpSucceeded, "", "Backup restored. Start the bot to use it.", map[string]any{"backup_id": id, "environment": opt.RestoreEnv})
		case errors.Is(err, domain.ErrNotStopped):
			s.Ops.Finish(ctx, op, domain.OpFailed, "not_stopped", "the bot must be stopped", nil)
		default:
			msg := "the restore failed; nothing was changed unless a later step says so"
			var ve *domain.ValidationError
			var ae *filesystem.ErrArchive
			if errors.As(err, &ve) {
				msg = ve.Msg
			} else if errors.As(err, &ae) {
				msg = ae.Msg
			}
			s.Ops.Finish(ctx, op, domain.OpFailed, "restore_failed", msg, nil)
		}
	}()
	if b.DesiredState != domain.DesiredStopped || (b.ObservedState != "stopped" && b.ObservedState != "failed" && b.ObservedState != "unknown") {
		return domain.Bot{}, domain.ErrNotStopped
	}
	bk, err := s.store().GetBackup(ctx, botID, id)
	if err != nil {
		return domain.Bot{}, err
	}
	if bk.Status != "ready" || bk.SHA256Hex == nil {
		return domain.Bot{}, domain.Invalid("this backup is not available")
	}
	path := filepath.Join(s.botDir(botID), bk.FileName)
	s.Ops.Stage(ctx, op, "Verifying the backup")
	if err := verifyChecksum(path, *bk.SHA256Hex); err != nil {
		return domain.Bot{}, err
	}

	// Everything that can be checked without changing anything is checked first:
	// the archive's environment snapshot must decrypt with this server's keys.
	var envRows []domain.EnvVar
	wantEnv := opt.RestoreEnv && bk.IncludesEnv
	if wantEnv {
		mf, err := os.Open(path)
		if err != nil {
			return domain.Bot{}, err
		}
		meta, err := filesystem.ReadArchiveMeta(mf, s.Limits)
		mf.Close()
		if err != nil {
			return domain.Bot{}, err
		}
		if raw, ok := meta["env.json"]; ok {
			if envRows, err = s.parseEnv(botID, raw); err != nil {
				return domain.Bot{}, err
			}
		} else {
			wantEnv = false
		}
	}

	// Pre-restore snapshot, waiting for its turn and completion.
	s.Ops.Stage(ctx, op, "Saving a safety backup")
	pre, err := s.begin(ctx, b, "pre_restore", true, &actor.ID)
	if err != nil {
		return domain.Bot{}, err
	}
	s.execute(ctx, pre, true, "")
	if got, err := s.store().GetBackup(ctx, botID, pre.ID); err != nil || got.Status != "ready" {
		return domain.Bot{}, domain.Invalid("could not take a safety backup of the current state; restore aborted")
	}

	s.Ops.Stage(ctx, op, "Replacing files")
	f, err := os.Open(path)
	if err != nil {
		return domain.Bot{}, err
	}
	defer f.Close()
	var finish func(bool) error
	if s.Bots.remote(b.NodeID) {
		if s.Remote == nil {
			return domain.Bot{}, domain.ErrRunnerUnavailable
		}
		tx, rerr := s.Remote.BeginRestore(ctx, b.NodeID, botID, f, s.Limits)
		if rerr != nil {
			return domain.Bot{}, rerr
		}
		finish = func(ok bool) error {
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			for {
				err := s.Remote.CompleteRestore(fctx, b.NodeID, botID, tx, ok)
				if err == nil {
					return nil
				}
				select {
				case <-fctx.Done():
					return err
				case <-time.After(250 * time.Millisecond):
				}
			}
		}
	} else {
		w, werr := s.Files.Open(botID)
		if werr != nil {
			return domain.Bot{}, werr
		}
		defer w.Close()
		// The previous files are kept until the database records the restore;
		// a failure here or a crash rolls them back (Manager.Recover at start).
		_, commit, rerr := w.RestoreTarGzCommit(f, s.Limits)
		if rerr != nil {
			return domain.Bot{}, rerr
		}
		finish = func(ok bool) error {
			if ok {
				return commit.Finish()
			}
			return commit.Rollback()
		}
	}
	dctx, dcancel := bg(ctx) // the database step must not be cut short by a closed request
	defer dcancel()
	now := s.now().UnixMilli()
	if wantEnv {
		err = s.store().ReplaceEnvAll(dctx, botID, envRows, now)
	} else {
		// Files changed: advance the generation so dependencies are rebuilt.
		var cur domain.Bot
		if cur, err = s.store().GetBot(dctx, botID); err == nil {
			err = s.store().UpdateBotConfig(dctx, cur, now)
		}
	}
	if err != nil {
		if rerr := finish(false); rerr != nil {
			s.Log.Error("restore: files could not be rolled back after a database error", "bot", botID, "err", rerr)
		}
		return domain.Bot{}, err
	}
	if err := finish(true); err != nil {
		s.Log.Warn("restore: clean up the previous files", "bot", botID, "err", err)
	}
	return s.store().GetBot(dctx, botID)
}

func (s *BackupService) parseEnv(botID string, raw []byte) ([]domain.EnvVar, error) {
	var snap envSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil || snap.Version != 1 || snap.BotID != botID {
		return nil, domain.Invalid("the backup's environment data does not belong to this bot")
	}
	if len(snap.Vars) > maxEnvVars {
		return nil, domain.Invalid("the backup's environment data is invalid")
	}
	out := make([]domain.EnvVar, 0, len(snap.Vars))
	for _, v := range snap.Vars {
		if err := validateEnvName(v.Name, false); err != nil {
			return nil, domain.Invalid("the backup's environment data is invalid")
		}
		ct, e1 := base64.StdEncoding.DecodeString(v.Ciphertext)
		nonce, e2 := base64.StdEncoding.DecodeString(v.Nonce)
		if e1 != nil || e2 != nil || len(nonce) != 12 || len(ct) < 16 {
			return nil, domain.Invalid("the backup's environment data is invalid")
		}
		sealed := secrets.Sealed{Ciphertext: ct, Nonce: nonce, KeyID: v.KeyID}
		if _, err := s.Keys.Open(botID, v.Name, sealed); err != nil {
			return nil, domain.Invalid("the backup's environment cannot be decrypted with this server's keys; restore without the environment")
		}
		out = append(out, domain.EnvVar{BotID: botID, Name: v.Name, Ciphertext: ct, Nonce: nonce, KeyID: v.KeyID})
	}
	return out, nil
}

func verifyChecksum(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.ErrNotFound
		}
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return domain.Invalid("the backup file failed its integrity check")
	}
	return nil
}

// RunScheduledForTest and SweepOrphansForTest expose single passes of the
// scheduler for deterministic tests.
func (s *BackupService) RunScheduledForTest(ctx context.Context) { s.init(); s.runScheduled(ctx) }
func (s *BackupService) SweepOrphansForTest(ctx context.Context) { s.init(); s.sweepOrphans(ctx) }

// stopForBackup records stopped intent for a running bot and waits until no
// container runs. The returned resume restores the previous intent, but only
// if nobody changed it meanwhile (a newer Stop or Start always wins).
func (s *BackupService) stopForBackup(ctx context.Context, botID, op string) (func(), error) {
	b, err := s.store().GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	if b.DesiredState != domain.DesiredRunning {
		return func() {}, nil
	}
	s.Ops.Stage(ctx, op, "Stopping the bot")
	nb, changed, err := s.store().SetDesired(ctx, botID, domain.DesiredStopped, false, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if changed && s.Bots.Notifier != nil {
		s.Bots.Bus.Publish(events.Status{BotID: botID, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
			Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
		s.Bots.Notifier.Notify(botID)
	}
	stoppedGen := nb.Generation
	deadline := time.Now().Add(2 * time.Minute)
	for {
		cur, err := s.store().GetBot(ctx, botID)
		if err != nil {
			return nil, err
		}
		if cur.Generation != stoppedGen {
			return nil, errors.New("intent changed while stopping")
		}
		if cur.ObservedGeneration == cur.Generation && (cur.ObservedState == "stopped" || cur.ObservedState == "failed") {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the bot did not stop in time")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return func() {
		fctx, cancel := bg(ctx)
		defer cancel()
		cur, err := s.store().GetBot(fctx, botID)
		if err != nil || cur.DesiredState != domain.DesiredStopped || cur.Generation != stoppedGen {
			return
		}
		if nb, changed, err := s.store().SetDesired(fctx, botID, domain.DesiredRunning, true, s.now().UnixMilli()); err == nil && changed {
			s.Bots.Bus.Publish(events.Status{BotID: botID, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
				Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
			s.Bots.Notifier.Notify(botID)
		}
	}, nil
}

// SetLabel changes a backup's label (full admin).
func (s *BackupService) SetLabel(ctx context.Context, actor domain.User, botID, id, label string) (domain.Backup, error) {
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin); err != nil {
		return domain.Backup{}, err
	}
	l, err := cleanLabel(label)
	if err != nil {
		return domain.Backup{}, err
	}
	if err := s.store().SetBackupLabel(ctx, botID, id, l); err != nil {
		return domain.Backup{}, err
	}
	return s.store().GetBackup(ctx, botID, id)
}

// Verify re-reads a backup completely: the checksum recorded when it was
// written, the gzip stream and every archive entry. The result is stored so
// the list shows when each backup was last known to be good.
func (s *BackupService) Verify(ctx context.Context, actor domain.User, botID, id string) (domain.Backup, error) {
	s.init()
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles); err != nil {
		return domain.Backup{}, err
	}
	b, err := s.store().GetBackup(ctx, botID, id)
	if err != nil {
		return domain.Backup{}, err
	}
	if b.Status != "ready" || b.SHA256Hex == nil {
		return domain.Backup{}, domain.Invalid("only finished backups can be verified")
	}
	path := filepath.Join(s.botDir(botID), b.FileName)
	var verr *string
	if err := verifyChecksum(path, *b.SHA256Hex); err != nil {
		m := "the file is missing or does not match its checksum"
		verr = &m
	} else if f, err := os.Open(path); err != nil {
		m := "the file could not be read"
		verr = &m
	} else {
		_, err := filesystem.ReadArchiveMeta(f, s.Limits)
		f.Close()
		if err != nil {
			m := "the archive is damaged"
			verr = &m
		}
	}
	if err := s.store().SetBackupVerified(ctx, id, s.now().UnixMilli(), verr); err != nil {
		return domain.Backup{}, err
	}
	return s.store().GetBackup(ctx, botID, id)
}

// Health summarizes a bot's backup protection for display.
type Health struct {
	IntervalMS      int64 // 0 = scheduled backups are off on this panel
	Keep            int
	Enabled         bool // this bot takes part in the schedule
	LastSuccessMS   int64
	LastScheduledMS int64
	NextDueMS       int64
	LastFailure     *domain.Backup // newest backup, when it failed
	TotalBytes      int64
	Count           int
	Limit           int
	ManualLimit     int
}

func (s *BackupService) Health(ctx context.Context, actor domain.User, botID string) (Health, []domain.Backup, error) {
	b, err := s.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles)
	if err != nil {
		return Health{}, nil, err
	}
	list, err := s.store().ListBackups(ctx, botID)
	if err != nil {
		return Health{}, nil, err
	}
	h := Health{IntervalMS: s.Interval.Milliseconds(), Keep: s.Keep, Enabled: !b.AutoBackupOff, Count: len(list),
		Limit: MaxBackupsPerBot, ManualLimit: MaxBackupsPerBot - reservedSlots}
	for i, x := range list {
		h.TotalBytes += x.SizeBytes
		if x.Status == "ready" && h.LastSuccessMS == 0 {
			h.LastSuccessMS = x.CreatedAtMS
		}
		if x.Kind == "auto" && x.Status != "failed" && h.LastScheduledMS == 0 {
			h.LastScheduledMS = x.CreatedAtMS
		}
		if i == 0 && x.Status == "failed" {
			f := x
			h.LastFailure = &f
		}
	}
	if h.IntervalMS > 0 && h.Enabled {
		h.NextDueMS = s.now().UnixMilli()
		if h.LastScheduledMS > 0 {
			h.NextDueMS = max(h.NextDueMS, h.LastScheduledMS+h.IntervalMS)
		}
	}
	return h, list, nil
}
