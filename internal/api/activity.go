package api

import (
	"bytes"
	"encoding/csv"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// The unified activity timeline: recorded changes and long-running work in
// one stream, with filters, a summary report and a CSV export.

type activityAppDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // bot | game | web | site
	LogoURL     string `json:"logo_url"`
	LogoVariant int64  `json:"logo_variant"`
}

type activityItemDTO struct {
	Key        string          `json:"key"`
	AtMS       int64           `json:"at_ms"`
	Type       string          `json:"type"`
	Source     string          `json:"source"`
	Category   string          `json:"category"`
	Outcome    string          `json:"outcome"`
	Actor      *personDTO      `json:"actor"`
	ActorLabel *string         `json:"actor_label"`
	App        *activityAppDTO `json:"app"`
	Action     string          `json:"action"`
	Target     *string         `json:"target"`
	Op         *opDTO          `json:"op,omitempty"`
}

func toActivityItem(it domain.ActivityItem) activityItemDTO {
	d := activityItemDTO{Key: it.Key, AtMS: it.AtMS, Type: it.Type, Source: it.Source, Category: it.Category, Outcome: it.Outcome,
		ActorLabel: it.ActorLabel, Action: it.Action, Target: it.Target}
	if it.Actor != nil {
		d.Actor = toPerson(*it.Actor)
	}
	switch {
	case it.BotID != nil:
		a := &activityAppDTO{ID: *it.BotID, Kind: it.BotKind, LogoURL: it.BotLogoURL, LogoVariant: it.BotVariant}
		if it.BotName != nil {
			a.Name = *it.BotName
		}
		if a.Kind == "" {
			a.Kind = "bot"
		}
		d.App = a
	case it.SiteID != nil:
		a := &activityAppDTO{ID: *it.SiteID, Kind: "site", LogoVariant: it.SiteVariant}
		if it.SiteName != nil {
			a.Name = *it.SiteName
		}
		d.App = a
	}
	if it.Op != nil {
		o := toOp(*it.Op)
		d.Op = &o
	}
	return d
}

// csvList splits a comma-separated query value, keeping only allowed values
// (nil allows anything up to the length bounds).
func csvList(c fiber.Ctx, key string, allowed []string) ([]string, error) {
	v := strings.TrimSpace(c.Query(key))
	if v == "" {
		return nil, nil
	}
	parts := strings.Split(v, ",")
	if len(parts) > 50 {
		return nil, domain.Invalid(key + " has too many values")
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > 64 || (allowed != nil && !slices.Contains(allowed, p)) {
			return nil, domain.Invalid(key + " has an unknown value: " + p)
		}
		out = append(out, strings.Clone(p))
	}
	return out, nil
}

func queryMS(c fiber.Ctx, key string) (int64, error) {
	v := c.Query(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, domain.Invalid(key + " must be a time in milliseconds")
	}
	return n, nil
}

// activityFilter reads the shared filter parameters.
func activityFilter(c fiber.Ctx) (sqlite.ActivityFilter, error) {
	var f sqlite.ActivityFilter
	var err error
	if f.Apps, err = csvList(c, "apps", nil); err != nil {
		return f, err
	}
	if f.Actors, err = csvList(c, "actors", nil); err != nil {
		return f, err
	}
	if f.Sources, err = csvList(c, "sources", domain.ActivitySources); err != nil {
		return f, err
	}
	if f.Categories, err = csvList(c, "categories", domain.ActivityCategories); err != nil {
		return f, err
	}
	if f.Outcomes, err = csvList(c, "outcomes", domain.ActivityOutcomes); err != nil {
		return f, err
	}
	if f.Types, err = csvList(c, "types", []string{"change", "work"}); err != nil {
		return f, err
	}
	if q := c.Query("q"); len(q) > 200 {
		return f, domain.Invalid("search must be at most 200 characters")
	} else {
		f.Query = strings.Clone(q)
	}
	if f.FromMS, err = queryMS(c, "from"); err != nil {
		return f, err
	}
	if f.ToMS, err = queryMS(c, "to"); err != nil {
		return f, err
	}
	if f.FromMS > 0 && f.ToMS > 0 && f.ToMS <= f.FromMS {
		return f, domain.Invalid("to must be after from")
	}
	return f, nil
}

func (s *panel) activityTimeline(c fiber.Ctx) error {
	f, err := activityFilter(c)
	if err != nil {
		return err
	}
	f.Limit = 50
	if v := c.Query("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil || f.Limit < 1 || f.Limit > 200 {
			return domain.Invalid("limit must be between 1 and 200")
		}
	}
	if f.BeforeMS, err = queryMS(c, "before"); err != nil {
		return err
	}
	if k := c.Query("before_key"); len(k) > 80 {
		return domain.Invalid("before_key is not a cursor")
	} else {
		f.BeforeKey = strings.Clone(k)
	}
	if f.BeforeMS > 0 && f.BeforeKey == "" {
		f.BeforeKey = "￿" // everything at that time is still to come
	}
	items, err := s.audit.Timeline(c.Context(), currentUser(c), f)
	if err != nil {
		return err
	}
	out := make([]activityItemDTO, len(items))
	for i, it := range items {
		out[i] = toActivityItem(it)
	}
	m := fiber.Map{"items": out}
	if len(items) == f.Limit {
		last := items[len(items)-1]
		m["next"] = fiber.Map{"before": last.AtMS, "before_key": last.Key}
	}
	return c.JSON(m)
}

// activityBuckets are the bucket sizes a report may use; the smallest that
// keeps the histogram at or under maxBuckets bars wins.
var activityBuckets = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour,
	3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour}

const (
	maxBuckets     = 120
	maxStatsWindow = 400 * 24 * time.Hour
)

func (s *panel) activityStats(c fiber.Ctx) error {
	f, err := activityFilter(c)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if f.ToMS == 0 || f.ToMS > now+time.Minute.Milliseconds() {
		f.ToMS = now
	}
	if f.FromMS == 0 {
		f.FromMS = f.ToMS - (7 * 24 * time.Hour).Milliseconds()
	}
	if f.ToMS <= f.FromMS {
		return domain.Invalid("to must be after from")
	}
	if time.Duration(f.ToMS-f.FromMS)*time.Millisecond > maxStatsWindow {
		return domain.Invalid("a report covers at most 400 days")
	}
	tz := 0
	if v := c.Query("tz"); v != "" {
		if tz, err = strconv.Atoi(v); err != nil || tz < -840 || tz > 840 {
			return domain.Invalid("tz must be a UTC offset in minutes")
		}
	}
	span := f.ToMS - f.FromMS
	bucket := activityBuckets[len(activityBuckets)-1].Milliseconds()
	for _, b := range activityBuckets {
		if span/b.Milliseconds() <= maxBuckets {
			bucket = b.Milliseconds()
			break
		}
	}
	// Align the window to whole buckets in local time so bars start on the
	// hour or at local midnight.
	off := int64(tz) * 60_000
	f.FromMS = (f.FromMS+off)/bucket*bucket - off
	st, err := s.audit.TimelineStats(c.Context(), currentUser(c), f, bucket, tz)
	if err != nil {
		return err
	}
	buckets := make([]fiber.Map, len(st.Buckets))
	for i, b := range st.Buckets {
		buckets[i] = fiber.Map{"t": b.StartMS, "people": b.People, "automatic": b.Automatic, "failed": b.Failed}
	}
	people := make([]fiber.Map, len(st.People))
	for i, p := range st.People {
		var pd any
		if p.Person.Email != "" {
			pd = toPerson(p.Person)
		}
		people[i] = fiber.Map{"id": p.Person.ID, "label": p.Label, "person": pd, "count": p.Count, "failed": p.Failed}
	}
	apps := make([]fiber.Map, len(st.Apps))
	for i, a := range st.Apps {
		apps[i] = fiber.Map{"app": activityAppDTO{ID: a.ID, Name: a.Name, Kind: a.Kind, LogoURL: a.LogoURL, LogoVariant: a.Variant},
			"count": a.Count, "failed": a.Failed}
	}
	return c.JSON(fiber.Map{"from": st.FromMS, "to": st.ToMS, "bucket_ms": st.BucketMS, "buckets": buckets, "total": st.Total,
		"by_source": st.BySource, "by_outcome": st.ByOutcome, "by_category": st.ByCategory, "people": people, "apps": apps, "week": st.Week})
}

func (s *panel) activityPeople(c fiber.Ctx) error {
	f, err := activityFilter(c)
	if err != nil {
		return err
	}
	ps, err := s.audit.TimelinePeople(c.Context(), currentUser(c), f)
	if err != nil {
		return err
	}
	out := make([]*personDTO, len(ps))
	for i, p := range ps {
		out[i] = toPerson(p)
	}
	return c.JSON(fiber.Map{"people": out})
}

// maxExportRows bounds one CSV export.
const maxExportRows = 10_000

// csvSafe keeps a spreadsheet from reading a cell as a formula.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func (s *panel) activityExport(c fiber.Ctx) error {
	f, err := activityFilter(c)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"time_utc", "type", "performed_by", "actor", "category", "action", "outcome", "app", "app_kind", "detail"})
	f.Limit = 200
	rows := 0
	for rows < maxExportRows {
		items, err := s.audit.Timeline(c.Context(), currentUser(c), f)
		if err != nil {
			return err
		}
		for _, it := range items {
			actor := ""
			switch {
			case it.Actor != nil && it.Actor.Email != "":
				actor = it.Actor.Email
			case it.ActorLabel != nil:
				actor = *it.ActorLabel
			}
			app, kind := "", ""
			switch {
			case it.BotName != nil:
				app, kind = *it.BotName, it.BotKind
			case it.SiteName != nil:
				app, kind = *it.SiteName, "site"
			}
			detail := ""
			if it.Target != nil {
				detail = *it.Target
			}
			if it.Op != nil {
				parts := []string{it.Op.Status}
				if it.Op.SourceLabel != nil {
					parts = append(parts, *it.Op.SourceLabel)
				}
				if it.Op.Message != nil {
					parts = append(parts, *it.Op.Message)
				}
				detail = strings.Join(parts, " · ")
			}
			rec := []string{time.UnixMilli(it.AtMS).UTC().Format(time.RFC3339), it.Type, it.Source, actor, it.Category, it.Action, it.Outcome, app, kind, detail}
			for i := range rec {
				rec[i] = csvSafe(rec[i])
			}
			_ = w.Write(rec)
			rows++
		}
		if len(items) < f.Limit {
			break
		}
		last := items[len(items)-1]
		f.BeforeMS, f.BeforeKey = last.AtMS, last.Key
	}
	w.Flush()
	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="activity-`+time.Now().UTC().Format("20060102-1504")+`.csv"`)
	return c.Send(buf.Bytes())
}
