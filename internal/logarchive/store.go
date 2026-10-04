// Package logarchive keeps logs on disk one file per day and turns closed
// days into gzip archives in a separate folder, with retention by age and
// total size.
//
// Layout below the root (the folder next to the database):
//
//	logs/<scope>/<YYYY-MM-DD>.log              the open (live) day, appended to
//	log-archive/<scope>/<YYYY-MM-DD>.log.gz    closed days
//
// A scope is "panel" or "bots/<bot id>". A day is the period that starts at
// the configured archive time (default 00:00) in the configured time zone
// and is named by the date it starts on.
//
// Archiving is crash safe and idempotent: the day file is first renamed to a
// sealed name (new lines for that day start a fresh file), then the archive
// is rebuilt as the existing archive plus one new gzip member named after the
// sealed file, written to a temporary file, fsynced and renamed into place;
// only then is the sealed file removed. A retry that finds a member with the
// sealed file's name in the archive only removes the sealed file, so nothing
// is archived twice. Archives with several members are ordinary gzip files
// (gzip -d, zcat and Go's gzip reader read them whole).
package logarchive

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // time zones for the archive time, also without system zoneinfo

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

const (
	LiveDir    = "logs"
	ArchiveDir = "log-archive"
	PanelScope = "panel"
	dateFmt    = "2006-01-02"
	sealedTag  = ".sealed-"
)

var (
	dateRe  = lazyre.New(`^\d{4}-\d{2}-\d{2}$`)
	botIDRe = lazyre.New(`^[0-9a-f-]{36}$`)
	// ErrNotFound means the requested day has no log.
	ErrNotFound = errors.New("no log for this day")
	// ErrLowDisk means lines were not written because the filesystem of the
	// log folders has less free space than Limits.MinFreeBytes.
	ErrLowDisk = errors.New("log lines not written: the disk is almost full")
)

// capMarker starts the one line written when a day file reaches its cap.
const capMarker = "[rivetpanel] log capped:"

// freeCheckEvery bounds how often Append asks the filesystem for free space.
const freeCheckEvery = 10 * time.Second

// Limits bound what the live (not yet archived) day files may use.
type Limits struct {
	// DayMaxBytes caps one scope's file for one day; when a write would
	// exceed it, the lines that fit are written, then one marker line, and
	// the rest of that day is dropped. 0 = no cap.
	DayMaxBytes int64
	// MinFreeBytes: nothing is written while the filesystem has less free
	// space than this (Append returns ErrLowDisk). 0 = no check.
	MinFreeBytes int64
}

// BotScope is the scope of one bot's console log.
func BotScope(botID string) string { return "bots/" + botID }

// ValidScope reports whether s is "panel" or "bots/<uuid>".
func ValidScope(s string) bool {
	if s == PanelScope {
		return true
	}
	id, ok := strings.CutPrefix(s, "bots/")
	return ok && botIDRe.MatchString(id)
}

// ValidDate reports whether d is a YYYY-MM-DD date.
func ValidDate(d string) bool {
	if !dateRe.MatchString(d) {
		return false
	}
	_, err := time.Parse(dateFmt, d)
	return err == nil
}

// Clock maps instants to days. The zero value is midnight, server local time.
type Clock struct {
	Loc *time.Location
	// At is the archive time as an offset from midnight (0 to 23h59m).
	At time.Duration
}

func (c Clock) loc() *time.Location {
	if c.Loc == nil {
		return time.Local
	}
	return c.Loc
}

// Day returns the name of the day t belongs to.
func (c Clock) Day(t time.Time) string {
	lt := t.In(c.loc())
	start := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, c.loc()).Add(c.At)
	if lt.Before(start) {
		lt = lt.AddDate(0, 0, -1)
	}
	return lt.Format(dateFmt)
}

// End returns the instant day ends (the next archive time).
func (c Clock) End(day string) time.Time {
	d, err := time.ParseInLocation(dateFmt, day, c.loc())
	if err != nil {
		return time.Time{}
	}
	return time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, c.loc()).Add(c.At)
}

// Store owns the two folders.
type Store struct {
	Root string
	Now  func() time.Time

	// FreeSpace reports the free bytes of the filesystem holding path
	// (statfs by default; tests replace it).
	FreeSpace func(path string) (uint64, error)

	mu     sync.Mutex // serializes appends with sealing
	clock  Clock
	limits Limits
	// capped remembers day files known to be capped ("scope/day"); reset
	// by Rotate and by a limit change.
	capped  map[string]bool
	freeAt  time.Time // last free space check (wall clock)
	freeLow bool
}

// New creates the folders (0700) below root.
func New(root string) (*Store, error) {
	for _, d := range []string{LiveDir, ArchiveDir} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			return nil, err
		}
	}
	return &Store{Root: root}, nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// SetClock changes how instants map to days (from the settings).
func (s *Store) SetClock(c Clock) {
	s.mu.Lock()
	s.clock = c
	s.mu.Unlock()
}

// SetLimits changes the live file limits (from the settings).
func (s *Store) SetLimits(l Limits) {
	s.mu.Lock()
	if l != s.limits {
		s.limits, s.capped, s.freeAt = l, nil, time.Time{}
	}
	s.mu.Unlock()
}

// Limits returns the live file limits.
func (s *Store) Limits() Limits {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limits
}

func statfsFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// LowDisk reports whether writes are refused for lack of free space. The
// filesystem is asked at most every 10 seconds.
func (s *Store) LowDisk() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lowDiskLocked()
}

func (s *Store) lowDiskLocked() bool {
	if s.limits.MinFreeBytes <= 0 {
		return false
	}
	if !s.freeAt.IsZero() && time.Since(s.freeAt) < freeCheckEvery {
		return s.freeLow
	}
	free := s.FreeSpace
	if free == nil {
		free = statfsFree
	}
	n, err := free(s.Root)
	s.freeAt = time.Now()
	// An unknown answer does not stop logging.
	s.freeLow = err == nil && n < uint64(s.limits.MinFreeBytes)
	return s.freeLow
}

// hasCapMarker reports whether the file at p ends with the cap marker line.
func hasCapMarker(p string, size int64) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	n := int64(512)
	if size < n {
		n = size
	}
	if n <= 0 {
		return false
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, size-n); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	buf = bytes.TrimRight(buf, "\n")
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		buf = buf[i+1:]
	}
	return bytes.HasPrefix(buf, []byte(capMarker))
}

func capLine(limit int64) []byte {
	return []byte(fmt.Sprintf("%s this day's file reached its limit of %d MiB; later lines of this day are not stored (Administration > Logs and retention)\n",
		capMarker, limit>>20))
}

// Clock returns the current day mapping.
func (s *Store) Clock() Clock {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

func (s *Store) liveDir(scope string) string {
	return filepath.Join(s.Root, LiveDir, filepath.FromSlash(scope))
}
func (s *Store) archiveDir(scope string) string {
	return filepath.Join(s.Root, ArchiveDir, filepath.FromSlash(scope))
}

// Line is one log line with the instant it belongs to.
type Line struct {
	At   time.Time
	Text []byte // without the trailing newline is fine; one is added if missing
}

// Append writes lines to the day files of their instants. Lines are grouped
// per day and written with one open/append/close per day.
func (s *Store) Append(scope string, lines []Line) error {
	if !ValidScope(scope) {
		return fmt.Errorf("invalid log scope %q", scope)
	}
	if len(lines) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lowDiskLocked() {
		return ErrLowDisk
	}
	byDay := map[string][]byte{}
	var order []string
	for _, l := range lines {
		d := s.clock.Day(l.At)
		if _, ok := byDay[d]; !ok {
			order = append(order, d)
		}
		b := append(byDay[d], l.Text...)
		if len(l.Text) == 0 || l.Text[len(l.Text)-1] != '\n' {
			b = append(b, '\n')
		}
		byDay[d] = b
	}
	dir := s.liveDir(scope)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, d := range order {
		p := filepath.Join(dir, d+".log")
		data := byDay[d]
		if lim := s.limits.DayMaxBytes; lim > 0 {
			var ok bool
			if data, ok = s.fitLocked(scope+"/"+d, p, data, lim); !ok {
				continue
			}
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr != nil || cerr != nil {
			return errors.Join(werr, cerr)
		}
	}
	return nil
}

// fitLocked applies the day cap to data bound for the file at p: it returns
// what to write (the whole lines that fit, plus the marker line when the cap
// is reached) and false when the day is capped already.
func (s *Store) fitLocked(key, p string, data []byte, lim int64) ([]byte, bool) {
	if s.capped[key] {
		return nil, false
	}
	var size int64
	if st, err := os.Stat(p); err == nil {
		size = st.Size()
	}
	if size >= lim || (size > 0 && hasCapMarker(p, size)) {
		s.markCapped(key)
		return nil, false
	}
	if size+int64(len(data)) <= lim {
		return data, true
	}
	room := lim - size
	cut := bytes.LastIndexByte(data[:room], '\n') + 1 // whole lines only
	out := append(data[:cut:cut], capLine(lim)...)
	s.markCapped(key)
	return out, true
}

func (s *Store) markCapped(key string) {
	if s.capped == nil {
		s.capped = map[string]bool{}
	}
	s.capped[key] = true
}

// Result summarizes one archiving pass.
type Result struct {
	Archived      int      `json:"archived"`       // day files gzipped
	ArchivedBytes int64    `json:"archived_bytes"` // their uncompressed size
	Deleted       int      `json:"deleted"`        // archives removed by retention
	DeletedBytes  int64    `json:"deleted_bytes"`
	Errors        []string `json:"errors,omitempty"`
}

func (r *Result) fail(err error) {
	if err != nil && len(r.Errors) < 20 {
		r.Errors = append(r.Errors, err.Error())
	}
}

// scopes lists every scope below base ("panel", "bots/<id>").
func scopes(base string) []string {
	var out []string
	if st, err := os.Stat(filepath.Join(base, PanelScope)); err == nil && st.IsDir() {
		out = append(out, PanelScope)
	}
	ents, _ := os.ReadDir(filepath.Join(base, "bots"))
	for _, e := range ents {
		if e.IsDir() && botIDRe.MatchString(e.Name()) {
			out = append(out, "bots/"+e.Name())
		}
	}
	return out
}

// Rotate archives every day file whose day has ended (catching up on any
// number of missed days) and finishes interrupted archiving.
func (s *Store) Rotate() Result {
	var r Result
	s.mu.Lock()
	s.capped = nil // sealed files start fresh day files
	s.mu.Unlock()
	today := s.Clock().Day(s.now())
	for _, sc := range scopes(filepath.Join(s.Root, LiveDir)) {
		dir := s.liveDir(sc)
		ents, err := os.ReadDir(dir)
		if err != nil {
			r.fail(err)
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if !e.Type().IsRegular() {
				continue
			}
			day, rest, _ := strings.Cut(name, ".")
			if !ValidDate(day) {
				continue
			}
			switch {
			case rest == "log" && day < today:
				n, err := s.archive(sc, day, name)
				r.fail(err)
				if err == nil {
					r.Archived++
					r.ArchivedBytes += n
				}
			case strings.HasPrefix(rest, "log"+sealedTag):
				// Left over from an interrupted pass.
				n, err := s.archiveSealed(sc, day, name)
				r.fail(err)
				if err == nil {
					r.Archived++
					r.ArchivedBytes += n
				}
			case strings.HasPrefix(rest, "log.gz.tmp"):
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
	// Temporary archive files a crash left behind.
	for _, sc := range scopes(filepath.Join(s.Root, ArchiveDir)) {
		ents, _ := os.ReadDir(s.archiveDir(sc))
		for _, e := range ents {
			if strings.Contains(e.Name(), ".tmp-") {
				_ = os.Remove(filepath.Join(s.archiveDir(sc), e.Name()))
			}
		}
	}
	return r
}

// archive seals day's live file and archives it.
func (s *Store) archive(scope, day, name string) (int64, error) {
	dir := s.liveDir(scope)
	// Unique per seal (never reused, even with a frozen or stepped clock):
	// the name identifies the gzip member this file becomes.
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	sealed := day + ".log" + sealedTag + strconv.FormatInt(time.Now().UnixNano(), 36) + hex.EncodeToString(rnd[:])
	s.mu.Lock()
	err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, sealed))
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return s.archiveSealed(scope, day, sealed)
}

// archiveSealed appends a sealed file to day's archive as a gzip member
// named after it (unless such a member exists already), then removes it.
func (s *Store) archiveSealed(scope, day, sealed string) (int64, error) {
	src := filepath.Join(s.liveDir(scope), sealed)
	st, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	adir := s.archiveDir(scope)
	if err := os.MkdirAll(adir, 0o700); err != nil {
		return 0, err
	}
	final := filepath.Join(adir, day+".log.gz")
	members, err := memberNames(final)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("%s: existing archive is unreadable: %w", final, err)
	}
	if !members[sealed] {
		if st.Size() == 0 {
			return 0, os.Remove(src)
		}
		if err := s.writeArchive(final, src, sealed, st.ModTime()); err != nil {
			return 0, err
		}
	}
	return st.Size(), os.Remove(src)
}

func (s *Store) writeArchive(final, src, member string, mtime time.Time) (err error) {
	tmp := final + ".tmp-" + strconv.FormatInt(s.now().UnixNano(), 36)
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			out.Close()
			os.Remove(tmp)
		}
	}()
	if prev, perr := os.Open(final); perr == nil {
		_, err = io.Copy(out, prev)
		prev.Close()
		if err != nil {
			return err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zw, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return err
	}
	zw.Name, zw.ModTime = member, mtime
	if _, err = io.Copy(zw, bufio.NewReaderSize(in, 64<<10)); err != nil {
		return err
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, final); err != nil {
		return err
	}
	syncDir(filepath.Dir(final))
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// memberNames returns the header names of an archive's gzip members.
func memberNames(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	br := bufio.NewReader(f)
	zr, err := gzip.NewReader(br)
	if err != nil {
		return nil, err
	}
	for {
		zr.Multistream(false)
		out[zr.Name] = true
		if _, err := io.Copy(io.Discard, zr); err != nil {
			return nil, err
		}
		if err := zr.Reset(br); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, err
		}
	}
}

// Retention selects which archives the retention pass removes.
type Retention struct {
	Days     int   // archives of days older than this many days are removed; 0 keeps all
	MaxBytes int64 // total archive size cap, oldest days first; 0 = no cap
}

type archiveFile struct {
	scope, day, path string
	size             int64
}

func (s *Store) archives() []archiveFile {
	var out []archiveFile
	for _, sc := range scopes(filepath.Join(s.Root, ArchiveDir)) {
		ents, _ := os.ReadDir(s.archiveDir(sc))
		for _, e := range ents {
			day, ok := strings.CutSuffix(e.Name(), ".log.gz")
			if !ok || !ValidDate(day) || !e.Type().IsRegular() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, archiveFile{sc, day, filepath.Join(s.archiveDir(sc), e.Name()), info.Size()})
		}
	}
	return out
}

// Prune applies retention to the archive folder.
func (s *Store) Prune(ret Retention) Result {
	var r Result
	all := s.archives()
	sort.Slice(all, func(i, j int) bool {
		if all[i].day != all[j].day {
			return all[i].day < all[j].day
		}
		return all[i].scope < all[j].scope
	})
	cutoff := ""
	if ret.Days > 0 {
		cutoff = s.cutoffDay(ret.Days)
	}
	// The cap covers the live day files too (they cannot be removed, so
	// archives make room for them).
	total := s.liveBytes()
	for _, a := range all {
		total += a.size
	}
	remove := func(a archiveFile) {
		if err := os.Remove(a.path); err != nil {
			r.fail(err)
			return
		}
		r.Deleted++
		r.DeletedBytes += a.size
		total -= a.size
	}
	kept := all[:0]
	for _, a := range all {
		if cutoff != "" && a.day < cutoff {
			remove(a)
			continue
		}
		kept = append(kept, a)
	}
	for _, a := range kept {
		if ret.MaxBytes <= 0 || total <= ret.MaxBytes {
			break
		}
		remove(a)
	}
	s.removeEmptyScopes()
	return r
}

// cutoffDay is the oldest day that is kept when days are retained: today
// and the days-1 days before it are kept, older days are removed.
func (s *Store) cutoffDay(days int) string {
	c := s.Clock()
	today, _ := time.ParseInLocation(dateFmt, c.Day(s.now()), c.loc())
	return today.AddDate(0, 0, -days).Format(dateFmt)
}

func (s *Store) removeEmptyScopes() {
	for _, base := range []string{LiveDir, ArchiveDir} {
		for _, sc := range scopes(filepath.Join(s.Root, base)) {
			if sc == PanelScope {
				continue
			}
			// Remove fails on a non-empty folder, which is what we want.
			p := filepath.Join(s.Root, base, filepath.FromSlash(sc))
			if ents, err := os.ReadDir(p); err == nil && len(ents) == 0 {
				_ = os.Remove(p)
			}
		}
	}
}

// RemoveScope deletes a scope's live and archived logs (a deleted bot).
func (s *Store) RemoveScope(scope string) error {
	if !ValidScope(scope) || scope == PanelScope {
		return fmt.Errorf("invalid log scope %q", scope)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(os.RemoveAll(s.liveDir(scope)), os.RemoveAll(s.archiveDir(scope)))
}

// Day describes one day of a scope's log.
type Day struct {
	Date     string `json:"date"`
	Bytes    int64  `json:"bytes"`    // on disk (compressed for archives)
	Archived bool   `json:"archived"` // false: the live (or not yet archived) file
	// Capped: the live file reached the day limit and later lines of the
	// day were dropped (only reported for live files).
	Capped bool `json:"capped,omitempty"`
}

// Days lists a scope's days, newest first. A day can appear twice while
// late lines wait to be added to its archive.
func (s *Store) Days(scope string) ([]Day, error) {
	if !ValidScope(scope) {
		return nil, fmt.Errorf("invalid log scope %q", scope)
	}
	var out []Day
	if ents, err := os.ReadDir(s.liveDir(scope)); err == nil {
		for _, e := range ents {
			if day, ok := strings.CutSuffix(e.Name(), ".log"); ok && ValidDate(day) && e.Type().IsRegular() {
				if info, err := e.Info(); err == nil {
					p := filepath.Join(s.liveDir(scope), e.Name())
					out = append(out, Day{Date: day, Bytes: info.Size(), Capped: hasCapMarker(p, info.Size())})
				}
			}
		}
	}
	if ents, err := os.ReadDir(s.archiveDir(scope)); err == nil {
		for _, e := range ents {
			if day, ok := strings.CutSuffix(e.Name(), ".log.gz"); ok && ValidDate(day) && e.Type().IsRegular() {
				if info, err := e.Info(); err == nil {
					out = append(out, Day{Date: day, Bytes: info.Size(), Archived: true})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return !out[i].Archived
	})
	return out, nil
}

// Open opens one day: the archive (gzip) when archived is true, the live
// file (plain text) otherwise. The caller closes it.
func (s *Store) Open(scope, day string, archived bool) (*os.File, int64, error) {
	if !ValidScope(scope) || !ValidDate(day) {
		return nil, 0, ErrNotFound
	}
	p := filepath.Join(s.liveDir(scope), day+".log")
	if archived {
		p = filepath.Join(s.archiveDir(scope), day+".log.gz")
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, 0, ErrNotFound
	}
	return f, st.Size(), nil
}

// Usage is the disk use of the two folders.
type Usage struct {
	LiveBytes     int64  `json:"live_bytes"`
	LiveFiles     int    `json:"live_files"`
	ArchivedBytes int64  `json:"archived_bytes"`
	ArchivedFiles int    `json:"archived_files"`
	OldestArchive string `json:"oldest_archive,omitempty"`
}

func (s *Store) liveFiles() (n int, size int64) {
	_ = filepath.WalkDir(filepath.Join(s.Root, LiveDir), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				size += info.Size()
				n++
			}
		}
		return nil
	})
	return n, size
}

func (s *Store) liveBytes() int64 { _, n := s.liveFiles(); return n }

// Usage walks both folders.
func (s *Store) Usage() Usage {
	var u Usage
	u.LiveFiles, u.LiveBytes = s.liveFiles()
	for _, a := range s.archives() {
		u.ArchivedBytes += a.size
		u.ArchivedFiles++
		if u.OldestArchive == "" || a.day < u.OldestArchive {
			u.OldestArchive = a.day
		}
	}
	return u
}
