package sqlite

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// The unified activity timeline reads two tables: audit_events (changes, by
// people, schedules, the panel or the AI operator) and operations (builds,
// deployments, backups and restores). Both are filtered by the same
// ActivityFilter and merged newest first. Items are ordered by (time, key)
// descending, where the key is "a:<zero-padded audit id>" or "o:<operation
// id>", so a cursor of the last item's time and key continues exactly.

// ActivityFilter selects timeline items; zero fields do not filter.
type ActivityFilter struct {
	VisibleTo  string   // non-administrators: only what this account may see
	Apps       []string // bot or site ids
	Actors     []string // account ids
	Sources    []string // domain.ActivitySources
	Categories []string // domain.ActivityCategories
	Outcomes   []string // domain.ActivityOutcomes
	Types      []string // change | work
	Query      string   // free text over names, targets and messages
	FromMS     int64    // inclusive
	ToMS       int64    // exclusive
	BeforeMS   int64    // cursor time (0 = from the newest)
	BeforeKey  string   // cursor key at BeforeMS
	Limit      int
}

// auditCategoryRules map actions to timeline categories. Exact actions are
// checked first, then prefixes, in order.
var auditCategoryExact = map[string][]string{
	"power":     {"bot.start", "bot.stop", "bot.restart", "bot.kill", "bot.crash", "bot.gave_up", "ai.restart", "scheduled.start", "scheduled.stop", "scheduled.restart", "scheduled.chain"},
	"deploys":   {"site.deploy", "site.rollback", "site.upload", "scheduled.deploy"},
	"backups":   {"scheduled.backup"},
	"files":     {"packages.edit", "ai.file_apply"},
	"variables": {"ai.env_input"},
}

var auditCategoryPrefix = [][2]string{
	{"admin.extension_", "extensions"}, {"extension.", "extensions"}, {"addon.", "extensions"},
	{"deploy.", "deploys"}, {"backup.", "backups"}, {"files.", "files"}, {"env.", "variables"},
	{"access.", "access"}, {"workspace.", "access"}, {"account.", "account"}, {"admin.", "admin"},
	{"ai.", "ai"}, {"bot.", "settings"}, {"site.", "settings"}, {"schedule.", "settings"},
	{"telemetry.", "settings"}, {"scheduled.", "power"},
}

// auditCategorySQL is a CASE expression over e.action. It is built from the
// constant rules above only, never from input.
var auditCategorySQL = func() string {
	var b strings.Builder
	b.WriteString("(CASE")
	for _, c := range domain.ActivityCategories {
		acts := auditCategoryExact[c]
		if len(acts) == 0 {
			continue
		}
		b.WriteString(" WHEN e.action IN ('" + strings.Join(acts, "','") + "') THEN '" + c + "'")
	}
	for _, p := range auditCategoryPrefix {
		// "_" is a LIKE wildcard: escape it so admin.extension_ is literal.
		b.WriteString(" WHEN e.action LIKE '" + strings.ReplaceAll(p[0], "_", `\_`) + `%' ESCAPE '\' THEN '` + p[1] + "'")
	}
	b.WriteString(" ELSE 'other' END)")
	return b.String()
}()

// opCategoryKinds are the operation kinds of each category.
var opCategoryKinds = map[string][]string{
	"deploys": {domain.OpBuild, domain.OpDeploy, domain.OpRollback, domain.OpPublish},
	"backups": {domain.OpBackup, domain.OpRestore},
}

const (
	opCategorySQL = `(CASE WHEN o.kind IN ('backup','restore') THEN 'backups' ELSE 'deploys' END)`
	opSourceSQL   = `(CASE o.trigger WHEN 'manual' THEN 'person' WHEN 'api' THEN 'person' WHEN 'schedule' THEN 'schedule' WHEN 'push' THEN 'push' ELSE 'system' END)`
	opOutcomeSQL  = `(CASE o.status WHEN 'succeeded' THEN 'ok' WHEN 'failed' THEN 'failed' WHEN 'interrupted' THEN 'failed' WHEN 'cancelled' THEN 'cancelled' ELSE 'running' END)`
	auditKeySQL   = `printf('a:%020d', e.id)`
	opKeySQL      = `('o:' || o.id)`
)

func actIn(col string, vals []string, where []string, args []any) ([]string, []any) {
	where = append(where, col+" IN (?"+strings.Repeat(",?", len(vals)-1)+")")
	for _, v := range vals {
		args = append(args, v)
	}
	return where, args
}

func likeArg(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// auditWhere returns the WHERE clause for audit_events e; ok is false when
// the filter cannot match any recorded change.
func auditWhere(f ActivityFilter, cursor bool) (string, []any, bool) {
	if len(f.Types) > 0 && !slices.Contains(f.Types, "change") {
		return "", nil, false
	}
	if len(f.Outcomes) > 0 && !slices.ContainsFunc(f.Outcomes, func(o string) bool { return o == "ok" || o == "failed" || o == "denied" }) {
		return "", nil, false
	}
	where := []string{"1"}
	var args []any
	if f.VisibleTo != "" {
		where = append(where, `(e.bot_id IN (SELECT id FROM bots WHERE owner_id = ? AND desired_state != 'deleted'
			UNION SELECT bot_id FROM bot_subusers WHERE user_id = ?
			UNION SELECT b2.id FROM bots b2 JOIN workspace_members m ON m.workspace_id = b2.workspace_id
				WHERE m.user_id = ? AND b2.desired_state != 'deleted')
			OR (e.actor_id = ? AND e.bot_id IS NULL) OR e.subject_user_id = ?)`)
		u := f.VisibleTo
		args = append(args, u, u, u, u, u)
	}
	if len(f.Apps) > 0 {
		ph := "?" + strings.Repeat(",?", len(f.Apps)-1)
		where = append(where, "(e.bot_id IN ("+ph+") OR e.site_id IN ("+ph+"))")
		for range 2 {
			for _, a := range f.Apps {
				args = append(args, a)
			}
		}
	}
	if len(f.Actors) > 0 {
		where, args = actIn("e.actor_id", f.Actors, where, args)
	}
	if len(f.Sources) > 0 {
		where, args = actIn("e.source", f.Sources, where, args)
	}
	if len(f.Categories) > 0 {
		where, args = actIn(auditCategorySQL, f.Categories, where, args)
	}
	if len(f.Outcomes) > 0 {
		where, args = actIn("e.outcome", f.Outcomes, where, args)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		l := likeArg(q)
		where = append(where, `(e.target LIKE ? ESCAPE '\' OR e.bot_name LIKE ? ESCAPE '\' OR e.site_name LIKE ? ESCAPE '\'
			OR e.actor_label LIKE ? ESCAPE '\' OR e.action LIKE ? ESCAPE '\')`)
		args = append(args, l, l, l, l, l)
	}
	if f.FromMS > 0 {
		where, args = append(where, "e.at_ms >= ?"), append(args, f.FromMS)
	}
	if f.ToMS > 0 {
		where, args = append(where, "e.at_ms < ?"), append(args, f.ToMS)
	}
	if cursor && f.BeforeMS > 0 {
		where = append(where, "(e.at_ms < ? OR (e.at_ms = ? AND "+auditKeySQL+" < ?))")
		args = append(args, f.BeforeMS, f.BeforeMS, f.BeforeKey)
	}
	return strings.Join(where, " AND "), args, true
}

// opsWhere returns the WHERE clause for operations o (joined to bots b and
// users u); ok is false when the filter cannot match any operation.
func opsWhere(f ActivityFilter, cursor bool) (string, []any, bool) {
	if len(f.Types) > 0 && !slices.Contains(f.Types, "work") {
		return "", nil, false
	}
	where := []string{"b.desired_state != 'deleted'"}
	var args []any
	if len(f.Categories) > 0 {
		var kinds []string
		for _, c := range f.Categories {
			kinds = append(kinds, opCategoryKinds[c]...)
		}
		if len(kinds) == 0 {
			return "", nil, false
		}
		where, args = actIn("o.kind", kinds, where, args)
	}
	if f.VisibleTo != "" {
		where = append(where, `(b.owner_id = ? OR o.bot_id IN (SELECT bot_id FROM bot_subusers WHERE user_id = ?)
			OR b.workspace_id IN (SELECT workspace_id FROM workspace_members WHERE user_id = ?))`)
		args = append(args, f.VisibleTo, f.VisibleTo, f.VisibleTo)
	}
	if len(f.Apps) > 0 {
		where, args = actIn("o.bot_id", f.Apps, where, args)
	}
	if len(f.Actors) > 0 {
		where, args = actIn("o.actor_id", f.Actors, where, args)
	}
	if len(f.Sources) > 0 {
		where, args = actIn(opSourceSQL, f.Sources, where, args)
	}
	if len(f.Outcomes) > 0 {
		where, args = actIn(opOutcomeSQL, f.Outcomes, where, args)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		l := likeArg(q)
		where = append(where, `(b.name LIKE ? ESCAPE '\' OR o.source_label LIKE ? ESCAPE '\' OR o.message LIKE ? ESCAPE '\'
			OR u.email LIKE ? ESCAPE '\' OR o.kind LIKE ? ESCAPE '\')`)
		args = append(args, l, l, l, l, l)
	}
	if f.FromMS > 0 {
		where, args = append(where, "o.created_at_ms >= ?"), append(args, f.FromMS)
	}
	if f.ToMS > 0 {
		where, args = append(where, "o.created_at_ms < ?"), append(args, f.ToMS)
	}
	if cursor && f.BeforeMS > 0 {
		where = append(where, "(o.created_at_ms < ? OR (o.created_at_ms = ? AND "+opKeySQL+" < ?))")
		args = append(args, f.BeforeMS, f.BeforeMS, f.BeforeKey)
	}
	return strings.Join(where, " AND "), args, true
}

const activityAuditFrom = ` FROM audit_events e LEFT JOIN users u ON u.id = e.actor_id
	LEFT JOIN bots b ON b.id = e.bot_id LEFT JOIN sites s ON s.id = e.site_id`

const activityOpsFrom = opFrom // operations o JOIN bots b LEFT JOIN users u

// ListActivity returns up to f.Limit timeline items, newest first.
func (db *DB) ListActivity(ctx context.Context, f ActivityFilter) ([]domain.ActivityItem, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	var changes, work []domain.ActivityItem
	if w, args, ok := auditWhere(f, true); ok {
		q := `SELECT ` + auditKeySQL + `, e.at_ms, e.source, ` + auditCategorySQL + `, e.outcome, e.actor_label, e.bot_id, e.bot_name,
			COALESCE(NULLIF(b.kind, ''), CASE WHEN e.bot_id IS NULL THEN '' ELSE 'bot' END), COALESCE(b.logo_updated_at_ms, 0), COALESCE(b.discord_avatar_url, ''),
			COALESCE(b.logo_variant, 0), e.site_id, e.site_name, COALESCE(s.logo_variant, 0), e.action, e.target, ` + personCols("u") +
			activityAuditFrom + ` WHERE ` + w + ` ORDER BY e.at_ms DESC, e.id DESC LIMIT ?`
		rows, err := db.QueryContext(ctx, q, append(args, f.Limit)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			it := domain.ActivityItem{Type: "change"}
			var p domain.Person
			var logoAt int64
			var discord string
			dst := append([]any{&it.Key, &it.AtMS, &it.Source, &it.Category, &it.Outcome, &it.ActorLabel, &it.BotID, &it.BotName,
				&it.BotKind, &logoAt, &discord, &it.BotVariant, &it.SiteID, &it.SiteName, &it.SiteVariant, &it.Action, &it.Target}, personDst(&p)...)
			if err := rows.Scan(dst...); err != nil {
				rows.Close()
				return nil, err
			}
			if p.ID != "" {
				it.Actor = &p
			}
			if it.BotID != nil {
				it.BotLogoURL = botLogoURL(*it.BotID, logoAt, discord)
			}
			changes = append(changes, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if w, args, ok := opsWhere(f, true); ok {
		q := `SELECT ` + opCols + `, ` + opKeySQL + `, ` + opSourceSQL + `, ` + opCategorySQL + `, ` + opOutcomeSQL + `,
			COALESCE(b.logo_updated_at_ms, 0), COALESCE(b.discord_avatar_url, ''), b.logo_variant, ` + personCols("u") +
			activityOpsFrom + ` WHERE ` + w + ` ORDER BY o.created_at_ms DESC, o.id DESC LIMIT ?`
		rows, err := db.QueryContext(ctx, q, append(args, f.Limit)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o domain.Operation
			it := domain.ActivityItem{Type: "work"}
			var p domain.Person
			var logoAt int64
			var discord string
			dst := append([]any{&o.ID, &o.BotID, &o.BotName, &o.BotKind, &o.Kind, &o.Trigger, &o.ActorID, &o.ActorEmail, &o.Status, &o.Stage,
				&o.SourceRef, &o.SourceLabel, &o.Generation, &o.ResultCode, &o.Message, &o.DetailJSON, &o.LogBytes,
				&o.CreatedAtMS, &o.StartedAtMS, &o.FinishedAt,
				&it.Key, &it.Source, &it.Category, &it.Outcome, &logoAt, &discord, &it.BotVariant}, personDst(&p)...)
			if err := rows.Scan(dst...); err != nil {
				rows.Close()
				return nil, err
			}
			it.AtMS, it.Action, it.Op = o.CreatedAtMS, "op."+o.Kind, &o
			it.BotID, it.BotName, it.BotKind = &o.BotID, &o.BotName, o.BotKind
			it.BotLogoURL = botLogoURL(o.BotID, logoAt, discord)
			it.ActorLabel = o.ActorEmail
			if p.ID != "" {
				it.Actor = &p
			}
			work = append(work, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return mergeActivity(changes, work, f.Limit), nil
}

// ActivityAfter reports whether a sorts after b (is older) in the timeline.
func ActivityAfter(aMS int64, aKey string, bMS int64, bKey string) bool {
	return aMS < bMS || (aMS == bMS && aKey < bKey)
}

func mergeActivity(a, b []domain.ActivityItem, limit int) []domain.ActivityItem {
	out := make([]domain.ActivityItem, 0, min(limit, len(a)+len(b)))
	i, j := 0, 0
	for len(out) < limit && (i < len(a) || j < len(b)) {
		if j >= len(b) || (i < len(a) && ActivityAfter(b[j].AtMS, b[j].Key, a[i].AtMS, a[i].Key)) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	return out
}

func botLogoURL(id string, logoAtMS int64, discord string) string {
	if logoAtMS > 0 {
		return fmt.Sprintf("/api/v1/bots/%s/logo?v=%d", id, logoAtMS)
	}
	return discord
}

// ActivityStats summarizes the filtered window [f.FromMS, f.ToMS) in buckets
// of bucketMS. tzOffsetMin shifts the weekday/hour grid to local time.
func (db *DB) ActivityStats(ctx context.Context, f ActivityFilter, bucketMS int64, tzOffsetMin int) (domain.ActivityStats, error) {
	st := domain.ActivityStats{FromMS: f.FromMS, ToMS: f.ToMS, BucketMS: bucketMS,
		BySource: map[string]int64{}, ByOutcome: map[string]int64{}, ByCategory: map[string]int64{}}
	n := int((f.ToMS - f.FromMS + bucketMS - 1) / bucketMS)
	st.Buckets = make([]domain.ActivityBucket, n)
	for i := range st.Buckets {
		st.Buckets[i].StartMS = f.FromMS + int64(i)*bucketMS
	}
	off := int64(tzOffsetMin) * 60
	people := map[string]*domain.ActivityPersonCount{}
	apps := map[string]*domain.ActivityAppCount{}

	type part struct {
		where           string
		args            []any
		from, ts, src   string
		cat, outc       string
		actor, personOK string
		app, name, kind string
		logoAt, discord string
		variant         string
	}
	var parts []part
	if w, args, ok := auditWhere(f, false); ok {
		parts = append(parts, part{where: w, args: args, from: activityAuditFrom, ts: "e.at_ms", src: "e.source", cat: auditCategorySQL,
			outc: "e.outcome", actor: "e.actor_id", personOK: "e.source = 'person'", app: "COALESCE(e.bot_id, e.site_id)",
			name: "COALESCE(e.bot_name, e.site_name)", kind: "CASE WHEN e.bot_id IS NULL THEN 'site' ELSE COALESCE(NULLIF(b.kind, ''), 'bot') END",
			logoAt: "COALESCE(b.logo_updated_at_ms, 0)", discord: "COALESCE(b.discord_avatar_url, '')",
			variant: "COALESCE(b.logo_variant, s.logo_variant, 0)"})
	}
	if w, args, ok := opsWhere(f, false); ok {
		parts = append(parts, part{where: w, args: args, from: activityOpsFrom, ts: "o.created_at_ms", src: opSourceSQL, cat: opCategorySQL,
			outc: opOutcomeSQL, actor: "o.actor_id", personOK: "o.trigger IN ('manual','api')", app: "o.bot_id", name: "b.name",
			kind: "COALESCE(NULLIF(b.kind, ''), 'bot')", logoAt: "COALESCE(b.logo_updated_at_ms, 0)",
			discord: "COALESCE(b.discord_avatar_url, '')", variant: "b.logo_variant"})
	}
	failed := func(o string) bool { return o == "failed" || o == "denied" }
	for _, p := range parts {
		// Buckets, sources and outcomes.
		rows, err := db.QueryContext(ctx, `SELECT (`+p.ts+` - ?) / ?, `+p.src+`, `+p.outc+`, `+p.cat+`, COUNT(*)`+p.from+
			` WHERE `+p.where+` GROUP BY 1, 2, 3, 4`, append([]any{f.FromMS, bucketMS}, p.args...)...)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var b, c int64
			var src, outc, cat string
			if err := rows.Scan(&b, &src, &outc, &cat, &c); err != nil {
				rows.Close()
				return st, err
			}
			st.Total += c
			st.BySource[src] += c
			st.ByOutcome[outc] += c
			st.ByCategory[cat] += c
			if b >= 0 && int(b) < n {
				if src == "person" {
					st.Buckets[b].People += c
				} else {
					st.Buckets[b].Automatic += c
				}
				if failed(outc) {
					st.Buckets[b].Failed += c
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return st, err
		}

		// Weekday by hour, local time (1970-01-01 was a Thursday: index 3).
		rows, err = db.QueryContext(ctx, `SELECT ((`+p.ts+` / 1000 + ?) / 86400 + 3) % 7, ((`+p.ts+` / 1000 + ?) % 86400) / 3600, COUNT(*)`+
			p.from+` WHERE `+p.where+` GROUP BY 1, 2`, append([]any{off, off}, p.args...)...)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var d, h, c int64
			if err := rows.Scan(&d, &h, &c); err != nil {
				rows.Close()
				return st, err
			}
			if d >= 0 && d < 7 && h >= 0 && h < 24 {
				st.Week[d][h] += c
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return st, err
		}

		// People: what accounts did themselves.
		rows, err = db.QueryContext(ctx, `SELECT `+p.actor+`, COUNT(*), SUM(`+p.outc+` IN ('failed','denied')), `+personCols("u")+
			p.from+` WHERE `+p.where+` AND `+p.actor+` IS NOT NULL AND `+p.personOK+` GROUP BY 1`, p.args...)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var id string
			var c, fl int64
			var pr domain.Person
			if err := rows.Scan(append([]any{&id, &c, &fl}, personDst(&pr)...)...); err != nil {
				rows.Close()
				return st, err
			}
			x := people[id]
			if x == nil {
				x = &domain.ActivityPersonCount{Person: pr, Label: pr.DisplayName}
				if x.Label == "" {
					x.Label = pr.Email
				}
				if pr.ID == "" {
					x.Person.ID, x.Label = id, "Removed account"
				}
				people[id] = x
			}
			x.Count += c
			x.Failed += fl
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return st, err
		}

		// Apps and sites.
		rows, err = db.QueryContext(ctx, `SELECT `+p.app+`, MAX(`+p.name+`), MAX(`+p.kind+`), MAX(`+p.logoAt+`), MAX(`+p.discord+`), MAX(`+p.variant+`),
			COUNT(*), SUM(`+p.outc+` IN ('failed','denied'))`+p.from+` WHERE `+p.where+` AND `+p.app+` IS NOT NULL GROUP BY 1`, p.args...)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var id, name, kind, discord string
			var logoAt, variant, c, fl int64
			if err := rows.Scan(&id, &name, &kind, &logoAt, &discord, &variant, &c, &fl); err != nil {
				rows.Close()
				return st, err
			}
			x := apps[id]
			if x == nil {
				x = &domain.ActivityAppCount{ID: id, Name: name, Kind: kind, Variant: variant}
				if kind != "site" {
					x.LogoURL = botLogoURL(id, logoAt, discord)
				}
				apps[id] = x
			}
			x.Count += c
			x.Failed += fl
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return st, err
		}
	}
	for _, p := range people {
		st.People = append(st.People, *p)
	}
	sort.Slice(st.People, func(i, j int) bool {
		if st.People[i].Count != st.People[j].Count {
			return st.People[i].Count > st.People[j].Count
		}
		return st.People[i].Label < st.People[j].Label
	})
	for _, a := range apps {
		st.Apps = append(st.Apps, *a)
	}
	sort.Slice(st.Apps, func(i, j int) bool {
		if st.Apps[i].Count != st.Apps[j].Count {
			return st.Apps[i].Count > st.Apps[j].Count
		}
		return st.Apps[i].Name < st.Apps[j].Name
	})
	st.People = st.People[:min(len(st.People), 8)]
	st.Apps = st.Apps[:min(len(st.Apps), 8)]
	return st, nil
}

// ActivityPeople lists the accounts that appear as actors in what the filter
// can see (for the person filter), most active first.
func (db *DB) ActivityPeople(ctx context.Context, f ActivityFilter, limit int) ([]domain.Person, error) {
	f.Actors, f.Sources = nil, nil
	seen := map[string]int64{}
	info := map[string]domain.Person{}
	type query struct {
		q    string
		args []any
	}
	var parts []query
	if w, args, ok := auditWhere(f, false); ok {
		parts = append(parts, query{`SELECT e.actor_id, COUNT(*), ` + personCols("u") + activityAuditFrom + ` WHERE ` + w + ` AND e.actor_id IS NOT NULL GROUP BY 1`, args})
	}
	if w, args, ok := opsWhere(f, false); ok {
		parts = append(parts, query{`SELECT o.actor_id, COUNT(*), ` + personCols("u") + activityOpsFrom + ` WHERE ` + w + ` AND o.actor_id IS NOT NULL GROUP BY 1`, args})
	}
	for _, p := range parts {
		rows, err := db.QueryContext(ctx, p.q, p.args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var c int64
			var pr domain.Person
			if err := rows.Scan(append([]any{&id, &c}, personDst(&pr)...)...); err != nil {
				rows.Close()
				return nil, err
			}
			if pr.ID == "" {
				continue // removed account
			}
			seen[id] += c
			info[id] = pr
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]domain.Person, 0, len(info))
	for _, p := range info {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if seen[out[i].ID] != seen[out[j].ID] {
			return seen[out[i].ID] > seen[out[j].ID]
		}
		return out[i].Email < out[j].Email
	})
	return out[:min(len(out), limit)], nil
}
