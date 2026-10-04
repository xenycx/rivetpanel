package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/logarchive"
)

// Setting keys of the log archive and of the retention of graph data.
const (
	SetLogArchiveTime    = "logs.archive_time"
	SetLogTimezone       = "logs.timezone"
	SetLogRetentionDays  = "logs.retention_days"
	SetLogMaxArchiveMB   = "logs.max_archive_mb"
	SetLogMaxDayMB       = "logs.max_day_mb"
	SetLogCaptureBots    = "logs.capture_bots"
	SetLogPanelFile      = "logs.panel_file"
	SetLogCaptureMinutes = "logs.capture_minutes"
	SetLogLastRun        = "logs.last_run"

	SetMetricsTelemetryHours = "metrics.telemetry_hours"
	SetMetricsUsage5mDays    = "metrics.usage_5m_days"
	SetMetricsUsage1hDays    = "metrics.usage_1h_days"
	SetMetricsUsage1dDays    = "metrics.usage_1d_days"
	SetMetricsStatusDays     = "metrics.status_days"
)

// MetricsRetention is how long graph data is kept.
type MetricsRetention struct {
	// TelemetryHours covers host and node telemetry (the host monitor and
	// node graphs) and per-bot telemetry samples.
	TelemetryHours int `json:"telemetry_hours"`
	Usage5mDays    int `json:"usage_5m_days"` // analytics 5-minute buckets
	Usage1hDays    int `json:"usage_1h_days"` // analytics hourly rows
	Usage1dDays    int `json:"usage_1d_days"` // analytics daily rows
	StatusDays     int `json:"status_days"`   // status page daily samples
}

// LogSettings configure the log archive job.
type LogSettings struct {
	ArchiveTime    string           `json:"archive_time"`    // HH:MM, when a day ends and is archived
	Timezone       string           `json:"timezone"`        // IANA name; "" = the server's local time
	RetentionDays  int              `json:"retention_days"`  // archives older than this are deleted
	MaxArchiveMB   int64            `json:"max_archive_mb"`  // total cap (archives and live files), oldest archives first; 0 = none
	MaxDayMB       int64            `json:"max_day_mb"`      // per server (and panel log) per day live file cap
	CaptureBots    bool             `json:"capture_bots"`    // copy bot/game-server console output to day files
	PanelFile      bool             `json:"panel_file"`      // write the panel's own log to day files
	CaptureMinutes int              `json:"capture_minutes"` // how often console output is copied
	Metrics        MetricsRetention `json:"metrics"`
}

// Bounds, shown to the UI with the settings.
type intBounds struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

var logSettingBounds = map[string]intBounds{
	"retention_days":          {1, 3650},
	"max_archive_mb":          {0, 1 << 20},
	"max_day_mb":              {1, 1 << 20},
	"capture_minutes":         {1, 60},
	"metrics.telemetry_hours": {1, 8760},
	"metrics.usage_5m_days":   {1, 14},
	"metrics.usage_1h_days":   {2, 90},
	"metrics.usage_1d_days":   {30, 1825},
	"metrics.status_days":     {90, 730},
}

// DefaultLogSettings are used for every value not saved in the panel.
// telemetry is the environment's RIVET_TELEMETRY_RETENTION.
func DefaultLogSettings(telemetry time.Duration) LogSettings {
	h := int(telemetry / time.Hour)
	if h < 1 {
		h = 168
	}
	return LogSettings{ArchiveTime: "00:00", RetentionDays: 30, MaxDayMB: DefaultLogMaxDayMB, CaptureBots: true, PanelFile: true, CaptureMinutes: 5,
		Metrics: MetricsRetention{TelemetryHours: h, Usage5mDays: int(usageRetention5m / (24 * time.Hour)),
			Usage1hDays: int(usageRetention1h / (24 * time.Hour)), Usage1dDays: int(usageRetention1d / (24 * time.Hour)),
			StatusDays: StatusRetentionDays}}
}

// DefaultLogMaxDayMB caps one server's (or the panel's) log file per day.
const DefaultLogMaxDayMB = 256

// LogSettingsInput is a partial update; nil fields keep their value.
type LogSettingsInput struct {
	ArchiveTime    *string `json:"archive_time"`
	Timezone       *string `json:"timezone"`
	RetentionDays  *int    `json:"retention_days"`
	MaxArchiveMB   *int64  `json:"max_archive_mb"`
	MaxDayMB       *int64  `json:"max_day_mb"`
	CaptureBots    *bool   `json:"capture_bots"`
	PanelFile      *bool   `json:"panel_file"`
	CaptureMinutes *int    `json:"capture_minutes"`
	Metrics        *struct {
		TelemetryHours *int `json:"telemetry_hours"`
		Usage5mDays    *int `json:"usage_5m_days"`
		Usage1hDays    *int `json:"usage_1h_days"`
		Usage1dDays    *int `json:"usage_1d_days"`
		StatusDays     *int `json:"status_days"`
	} `json:"metrics"`
}

// LogArchiveRun is the result of one archive job.
type LogArchiveRun struct {
	StartedAtMS   int64    `json:"started_at_ms"`
	FinishedAtMS  int64    `json:"finished_at_ms"`
	Trigger       string   `json:"trigger"` // schedule, startup, manual, settings
	OK            bool     `json:"ok"`
	CapturedLines int      `json:"captured_lines"`
	Archived      int      `json:"archived"`
	ArchivedBytes int64    `json:"archived_bytes"`
	Deleted       int      `json:"deleted"`
	DeletedBytes  int64    `json:"deleted_bytes"`
	Errors        []string `json:"errors,omitempty"`
}

// LogArchiveView is what the administration page shows.
type LogArchiveView struct {
	Settings  LogSettings          `json:"settings"`
	Defaults  LogSettings          `json:"defaults"`
	Bounds    map[string]intBounds `json:"bounds"`
	Usage     LogDiskUsage         `json:"usage"`
	LastRun   *LogArchiveRun       `json:"last_run"`
	Running   bool                 `json:"running"`
	NextRunMS int64                `json:"next_run_at_ms"`
	// CurrentDay is the day being written now (its file is live).
	CurrentDay string `json:"current_day"`
	// CaptureAvailable is false when this panel has no container runtime
	// and no remote nodes (nothing to capture).
	CaptureAvailable bool   `json:"capture_available"`
	Folder           string `json:"folder"`
	ArchiveFolder    string `json:"archive_folder"`
}

// LogDiskUsage reports where log bytes are on disk.
type LogDiskUsage struct {
	logarchive.Usage
	// OperationLogBytes are build/deploy/backup outputs (bounded per
	// operation and removed with the operation; not archived by day).
	OperationLogBytes int64 `json:"operation_log_bytes"`
	OperationLogFiles int   `json:"operation_log_files"`
}

// LogArchiveStore is the persistence the service needs.
type LogArchiveStore interface {
	Settings(ctx context.Context) (map[string]domain.Setting, error)
	PutSettings(ctx context.Context, set []domain.Setting, nowMS int64) error
}

// LogArchiveService runs the daily log archive job and owns its settings
// and the retention of graph data.
type LogArchiveService struct {
	Store    LogArchiveStore
	Files    *logarchive.Store
	Capture  *logarchive.Capturer // nil: console output is not captured
	Sink     *logarchive.Sink     // nil: the panel log is not written to files
	Bots     *BotService
	OpLogDir string
	Defaults LogSettings
	Log      *slog.Logger
	Now      func() time.Time
	// MinFreeDisk: log files are not written while the log folders'
	// filesystem has less free space (RIVET_MIN_FREE_DISK_BYTES).
	MinFreeDisk int64

	jobMu       sync.Mutex // one job or capture pass at a time
	mu          sync.Mutex
	cur         LogSettings
	loaded      bool
	last        *LogArchiveRun
	running     bool
	lastCapture time.Time
	lastDay     string
	kick        chan string
}

func (s *LogArchiveService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *LogArchiveService) defaults() LogSettings {
	if s.Defaults.ArchiveTime == "" {
		return DefaultLogSettings(7 * 24 * time.Hour)
	}
	return s.Defaults
}

// Settings returns the effective settings (defaults until Load).
func (s *LogArchiveService) Settings() LogSettings {
	if s == nil {
		return DefaultLogSettings(7 * 24 * time.Hour)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return s.defaults()
	}
	return s.cur
}

// Metrics returns the effective retention of graph data.
func (s *LogArchiveService) Metrics() MetricsRetention { return s.Settings().Metrics }

func parseArchiveTime(v string) (time.Duration, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if !ok || e1 != nil || e2 != nil || len(h) != 2 || len(m) != 2 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, domain.Invalid("archive time must be HH:MM (00:00 to 23:59)")
	}
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute, nil
}

func loadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return time.Local, nil
	}
	if len(name) > 64 {
		return nil, domain.Invalid("unknown time zone")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, domain.Invalid("unknown time zone " + strconv.Quote(name))
	}
	return loc, nil
}

func (ls LogSettings) clock() logarchive.Clock {
	at, _ := parseArchiveTime(ls.ArchiveTime)
	loc, err := loadZone(ls.Timezone)
	if err != nil {
		loc = time.Local
	}
	return logarchive.Clock{Loc: loc, At: at}
}

func checkBound(name string, v int64) error {
	b := logSettingBounds[name]
	if v < b.Min || v > b.Max {
		return domain.Invalid(fmt.Sprintf("%s must be between %d and %d", name, b.Min, b.Max))
	}
	return nil
}

// Validate checks every value against its bounds.
func (ls LogSettings) Validate() error {
	if _, err := parseArchiveTime(ls.ArchiveTime); err != nil {
		return err
	}
	if _, err := loadZone(ls.Timezone); err != nil {
		return err
	}
	m := ls.Metrics
	for name, v := range map[string]int64{"retention_days": int64(ls.RetentionDays), "max_archive_mb": ls.MaxArchiveMB, "max_day_mb": ls.MaxDayMB,
		"capture_minutes": int64(ls.CaptureMinutes), "metrics.telemetry_hours": int64(m.TelemetryHours),
		"metrics.usage_5m_days": int64(m.Usage5mDays), "metrics.usage_1h_days": int64(m.Usage1hDays),
		"metrics.usage_1d_days": int64(m.Usage1dDays), "metrics.status_days": int64(m.StatusDays)} {
		if err := checkBound(name, v); err != nil {
			return err
		}
	}
	// Coarser analytics rows are built from finer ones and must outlive them.
	if m.Usage1hDays < m.Usage5mDays || m.Usage1dDays < m.Usage1hDays {
		return domain.Invalid("analytics retention must not shrink with coarser resolution (5-minute <= hourly <= daily)")
	}
	return nil
}

// Load reads the stored settings and applies them.
func (s *LogArchiveService) Load(ctx context.Context) error {
	all, err := s.Store.Settings(ctx)
	if err != nil {
		return err
	}
	ls := s.defaults()
	str := func(k string, dst *string) {
		if v, ok := all[k]; ok {
			*dst = v.Value
		}
	}
	num := func(k string, dst *int) {
		if v, ok := all[k]; ok {
			if n, err := strconv.Atoi(v.Value); err == nil {
				*dst = n
			}
		}
	}
	boolean := func(k string, dst *bool) {
		if v, ok := all[k]; ok {
			*dst = v.Value == "true"
		}
	}
	str(SetLogArchiveTime, &ls.ArchiveTime)
	str(SetLogTimezone, &ls.Timezone)
	num(SetLogRetentionDays, &ls.RetentionDays)
	if v, ok := all[SetLogMaxArchiveMB]; ok {
		if n, err := strconv.ParseInt(v.Value, 10, 64); err == nil {
			ls.MaxArchiveMB = n
		}
	}
	if v, ok := all[SetLogMaxDayMB]; ok {
		if n, err := strconv.ParseInt(v.Value, 10, 64); err == nil {
			ls.MaxDayMB = n
		}
	}
	boolean(SetLogCaptureBots, &ls.CaptureBots)
	boolean(SetLogPanelFile, &ls.PanelFile)
	num(SetLogCaptureMinutes, &ls.CaptureMinutes)
	num(SetMetricsTelemetryHours, &ls.Metrics.TelemetryHours)
	num(SetMetricsUsage5mDays, &ls.Metrics.Usage5mDays)
	num(SetMetricsUsage1hDays, &ls.Metrics.Usage1hDays)
	num(SetMetricsUsage1dDays, &ls.Metrics.Usage1dDays)
	num(SetMetricsStatusDays, &ls.Metrics.StatusDays)
	if err := ls.Validate(); err != nil {
		// A stored value outside today's bounds (edited by hand): keep
		// running on the defaults rather than refusing to start.
		if s.Log != nil {
			s.Log.Warn("log archive settings invalid; using defaults", "err", err)
		}
		ls = s.defaults()
	}
	var last *LogArchiveRun
	if v, ok := all[SetLogLastRun]; ok && v.Value != "" {
		var r LogArchiveRun
		if json.Unmarshal([]byte(v.Value), &r) == nil {
			last = &r
		}
	}
	s.apply(ls)
	s.mu.Lock()
	if last != nil {
		s.last = last
	}
	s.mu.Unlock()
	return nil
}

func (s *LogArchiveService) apply(ls LogSettings) {
	s.mu.Lock()
	s.cur, s.loaded = ls, true
	s.mu.Unlock()
	if s.Files != nil {
		s.Files.SetClock(ls.clock())
		s.Files.SetLimits(logarchive.Limits{DayMaxBytes: ls.MaxDayMB << 20, MinFreeBytes: s.MinFreeDisk})
	}
	if s.Sink != nil {
		s.Sink.SetEnabled(ls.PanelFile)
	}
}

// View returns the settings, disk usage and last run.
func (s *LogArchiveService) View(ctx context.Context, actor domain.User) (LogArchiveView, error) {
	if !actor.Can(domain.PermSettingsManage) && !actor.Can(domain.PermSystemView) {
		return LogArchiveView{}, domain.ErrForbidden
	}
	ls := s.Settings()
	v := LogArchiveView{Settings: ls, Defaults: s.defaults(), Bounds: logSettingBounds, CaptureAvailable: s.Capture != nil}
	if s.Files != nil {
		clock := s.Files.Clock()
		v.CurrentDay = clock.Day(s.now())
		v.NextRunMS = clock.End(v.CurrentDay).UnixMilli()
		v.Usage.Usage = s.Files.Usage()
		v.Folder = filepath.Join(s.Files.Root, logarchive.LiveDir)
		v.ArchiveFolder = filepath.Join(s.Files.Root, logarchive.ArchiveDir)
	}
	if s.OpLogDir != "" {
		_ = filepath.WalkDir(s.OpLogDir, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, e := d.Info(); e == nil {
					v.Usage.OperationLogBytes += info.Size()
					v.Usage.OperationLogFiles++
				}
			}
			return nil
		})
	}
	s.mu.Lock()
	if s.last != nil {
		r := *s.last
		v.LastRun = &r
	}
	v.Running = s.running
	s.mu.Unlock()
	return v, nil
}

// Update validates and saves a partial settings change, applies it at once
// and runs the job so a shorter retention takes effect immediately.
func (s *LogArchiveService) Update(ctx context.Context, actor domain.User, in LogSettingsInput) (LogArchiveView, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return LogArchiveView{}, domain.ErrForbidden
	}
	ls := s.Settings()
	if in.ArchiveTime != nil {
		ls.ArchiveTime = strings.TrimSpace(*in.ArchiveTime)
	}
	if in.Timezone != nil {
		ls.Timezone = strings.TrimSpace(*in.Timezone)
		if ls.Timezone == "Local" {
			ls.Timezone = ""
		}
	}
	if in.RetentionDays != nil {
		ls.RetentionDays = *in.RetentionDays
	}
	if in.MaxArchiveMB != nil {
		ls.MaxArchiveMB = *in.MaxArchiveMB
	}
	if in.MaxDayMB != nil {
		ls.MaxDayMB = *in.MaxDayMB
	}
	if in.CaptureBots != nil {
		ls.CaptureBots = *in.CaptureBots
	}
	if in.PanelFile != nil {
		ls.PanelFile = *in.PanelFile
	}
	if in.CaptureMinutes != nil {
		ls.CaptureMinutes = *in.CaptureMinutes
	}
	if m := in.Metrics; m != nil {
		for _, f := range []struct {
			src *int
			dst *int
		}{{m.TelemetryHours, &ls.Metrics.TelemetryHours}, {m.Usage5mDays, &ls.Metrics.Usage5mDays}, {m.Usage1hDays, &ls.Metrics.Usage1hDays},
			{m.Usage1dDays, &ls.Metrics.Usage1dDays}, {m.StatusDays, &ls.Metrics.StatusDays}} {
			if f.src != nil {
				*f.dst = *f.src
			}
		}
	}
	if err := ls.Validate(); err != nil {
		return LogArchiveView{}, err
	}
	b := func(v bool) string { return strconv.FormatBool(v) }
	i := func(v int) string { return strconv.Itoa(v) }
	set := []domain.Setting{
		{Key: SetLogArchiveTime, Value: ls.ArchiveTime}, {Key: SetLogTimezone, Value: ls.Timezone},
		{Key: SetLogRetentionDays, Value: i(ls.RetentionDays)}, {Key: SetLogMaxArchiveMB, Value: strconv.FormatInt(ls.MaxArchiveMB, 10)},
		{Key: SetLogMaxDayMB, Value: strconv.FormatInt(ls.MaxDayMB, 10)},
		{Key: SetLogCaptureBots, Value: b(ls.CaptureBots)}, {Key: SetLogPanelFile, Value: b(ls.PanelFile)},
		{Key: SetLogCaptureMinutes, Value: i(ls.CaptureMinutes)},
		{Key: SetMetricsTelemetryHours, Value: i(ls.Metrics.TelemetryHours)}, {Key: SetMetricsUsage5mDays, Value: i(ls.Metrics.Usage5mDays)},
		{Key: SetMetricsUsage1hDays, Value: i(ls.Metrics.Usage1hDays)}, {Key: SetMetricsUsage1dDays, Value: i(ls.Metrics.Usage1dDays)},
		{Key: SetMetricsStatusDays, Value: i(ls.Metrics.StatusDays)},
	}
	if err := s.Store.PutSettings(ctx, set, s.now().UnixMilli()); err != nil {
		return LogArchiveView{}, err
	}
	s.apply(ls)
	s.trigger("settings")
	return s.View(ctx, actor)
}

// RunNow runs the job once and returns its result.
func (s *LogArchiveService) RunNow(ctx context.Context, actor domain.User) (LogArchiveRun, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return LogArchiveRun{}, domain.ErrForbidden
	}
	return s.job(context.WithoutCancel(ctx), "manual"), nil
}

func (s *LogArchiveService) trigger(why string) {
	s.mu.Lock()
	k := s.kick
	s.mu.Unlock()
	if k == nil {
		return
	}
	select {
	case k <- why:
	default:
	}
}

// capturePass copies new console output (callers hold jobMu).
func (s *LogArchiveService) capturePass(ctx context.Context) (int, []string) {
	if s.Capture == nil || !s.Settings().CaptureBots {
		return 0, nil
	}
	n, errs := s.Capture.Pass(ctx)
	s.mu.Lock()
	s.lastCapture = s.now()
	s.mu.Unlock()
	var out []string
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return n, out
}

// job captures, archives every ended day and applies retention.
func (s *LogArchiveService) job(ctx context.Context, trigger string) LogArchiveRun {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	r := LogArchiveRun{StartedAtMS: s.now().UnixMilli(), Trigger: trigger}
	n, errs := s.capturePass(ctx)
	r.CapturedLines = n
	r.Errors = append(r.Errors, errs...)
	if s.Sink != nil {
		s.Sink.Flush()
	}
	if s.Files != nil {
		rot := s.Files.Rotate()
		r.Archived, r.ArchivedBytes = rot.Archived, rot.ArchivedBytes
		r.Errors = append(r.Errors, rot.Errors...)
		ls := s.Settings()
		pr := s.Files.Prune(logarchive.Retention{Days: ls.RetentionDays, MaxBytes: ls.MaxArchiveMB << 20})
		r.Deleted, r.DeletedBytes = pr.Deleted, pr.DeletedBytes
		r.Errors = append(r.Errors, pr.Errors...)
	}
	if len(r.Errors) > 20 {
		r.Errors = r.Errors[:20]
	}
	r.OK = len(r.Errors) == 0
	r.FinishedAtMS = s.now().UnixMilli()
	s.mu.Lock()
	s.running = false
	s.last = &r
	s.mu.Unlock()
	if raw, err := json.Marshal(r); err == nil {
		_ = s.Store.PutSettings(context.WithoutCancel(ctx), []domain.Setting{{Key: SetLogLastRun, Value: string(raw)}}, r.FinishedAtMS)
	}
	if s.Log != nil {
		if r.OK {
			s.Log.Info("log archive job", "trigger", trigger, "archived", r.Archived, "deleted", r.Deleted, "captured_lines", r.CapturedLines)
		} else {
			s.Log.Warn("log archive job finished with errors", "trigger", trigger, "errors", len(r.Errors), "first", r.Errors[0])
		}
	}
	return r
}

// Run is the background job: console output is captured every
// CaptureMinutes, and when a day ends (at the archive time) or the panel
// starts, every ended day is archived and retention is applied. Missed days
// are caught up because every day file older than today is archived.
func (s *LogArchiveService) Run(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	s.mu.Lock()
	s.kick = make(chan string, 1)
	kick := s.kick
	s.mu.Unlock()
	t := time.NewTicker(tick)
	defer t.Stop()
	trigger := "startup"
	for {
		day := ""
		if s.Files != nil {
			day = s.Files.Clock().Day(s.now())
		}
		every := time.Duration(s.Settings().CaptureMinutes) * time.Minute
		s.mu.Lock()
		dayChanged := day != s.lastDay
		due := s.now().Sub(s.lastCapture) >= every
		s.mu.Unlock()
		switch {
		case trigger != "" || dayChanged:
			if trigger == "" {
				trigger = "schedule"
			}
			s.job(ctx, trigger)
			s.mu.Lock()
			s.lastDay = day
			s.mu.Unlock()
		case due:
			s.jobMu.Lock()
			if _, errs := s.capturePass(ctx); len(errs) > 0 && s.Log != nil {
				s.Log.Warn("log capture", "errors", len(errs), "first", errs[0])
			}
			// Late lines of an ended day (a node that was offline) are
			// added to that day's archive right away.
			if s.Files != nil {
				s.Files.Rotate()
			}
			s.jobMu.Unlock()
		}
		trigger = ""
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case trigger = <-kick:
		}
	}
}

// PurgeBot deletes a deleted bot's live and archived logs.
func (s *LogArchiveService) PurgeBot(botID string) {
	if s == nil || s.Files == nil {
		return
	}
	if err := s.Files.RemoveScope(logarchive.BotScope(botID)); err != nil && s.Log != nil {
		s.Log.Warn("remove bot logs", "bot", botID, "err", err)
	}
}

// CurrentDay is the day being written now ("" without log files).
func (s *LogArchiveService) CurrentDay() string {
	if s.Files == nil {
		return ""
	}
	return s.Files.Clock().Day(s.now())
}

// BotLogDays lists a bot's days. It needs the same access as its console.
func (s *LogArchiveService) BotLogDays(ctx context.Context, actor domain.User, botID string) ([]logarchive.Day, error) {
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermViewConsole); err != nil {
		return nil, err
	}
	if s.Files == nil {
		return []logarchive.Day{}, nil
	}
	days, err := s.Files.Days(logarchive.BotScope(botID))
	if days == nil {
		days = []logarchive.Day{}
	}
	return days, err
}

// OpenBotLogDay opens one day of a bot's log (gzip when archived).
func (s *LogArchiveService) OpenBotLogDay(ctx context.Context, actor domain.User, botID, day string, archived bool) (*os.File, int64, error) {
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermViewConsole); err != nil {
		return nil, 0, err
	}
	return s.openDay(logarchive.BotScope(botID), day, archived)
}

// PanelLogDays lists the panel log's days (the route requires system.view).
func (s *LogArchiveService) PanelLogDays() ([]logarchive.Day, error) {
	if s.Files == nil {
		return []logarchive.Day{}, nil
	}
	days, err := s.Files.Days(logarchive.PanelScope)
	if days == nil {
		days = []logarchive.Day{}
	}
	return days, err
}

// OpenPanelLogDay opens one day of the panel log.
func (s *LogArchiveService) OpenPanelLogDay(day string, archived bool) (*os.File, int64, error) {
	if s.Sink != nil {
		s.Sink.Flush()
	}
	return s.openDay(logarchive.PanelScope, day, archived)
}

func (s *LogArchiveService) openDay(scope, day string, archived bool) (*os.File, int64, error) {
	if s.Files == nil {
		return nil, 0, domain.ErrNotFound
	}
	if !logarchive.ValidDate(day) {
		return nil, 0, domain.Invalid("date must be YYYY-MM-DD")
	}
	f, n, err := s.Files.Open(scope, day, archived)
	if errors.Is(err, logarchive.ErrNotFound) {
		return nil, 0, domain.ErrNotFound
	}
	return f, n, err
}
