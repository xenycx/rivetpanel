package runner

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// AddonQuery is one request against a bot's database add-on.
type AddonQuery struct {
	Mode  string   `json:"mode"`       // read | write
	Query string   `json:"query"`      // SQL (postgres, mariadb)
	Op    string   `json:"op"`         // mongodb: databases | collections | find | count | aggregate | insert | update | delete
	DB    string   `json:"database"`   // mongodb database
	Coll  string   `json:"collection"` // mongodb collection
	Args  []string `json:"args"`       // redis command and arguments
	JSON  string   `json:"json"`       // mongodb filter, pipeline or document
}

// AddonQueryResult is a bounded answer.
type AddonQueryResult struct {
	Columns   []string   `json:"columns"`
	Rows      [][]string `json:"rows"`
	Truncated bool       `json:"truncated"`
	Text      string     `json:"text,omitempty"`
	Millis    int64      `json:"ms"`
}

const (
	addonQueryRows   = 500
	addonQueryBytes  = 1 << 20
	addonQueryMemory = 128 << 20
)

// AddonQuerier runs queries in a disposable client container started from
// the add-on's own image (which ships its client) on the bot's private
// add-on network. The client gets no workspace, a read-only root, no
// capabilities and small limits; the add-on's password reaches it only
// through its environment, and the query text only through the environment
// or as exec arguments (never through a shell command line).
//
// Read mode is a guard against accidents, not a security boundary: anyone
// allowed to query can also reveal the add-on's password.
type AddonQuerier struct {
	Docker    Docker
	User      string
	InstallID string
	Timeout   time.Duration
}

var (
	mongoName  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	redisRead  = map[string]bool{"GET": true, "MGET": true, "EXISTS": true, "TYPE": true, "TTL": true, "PTTL": true, "STRLEN": true, "GETRANGE": true, "HGET": true, "HMGET": true, "HGETALL": true, "HKEYS": true, "HVALS": true, "HLEN": true, "HEXISTS": true, "HSCAN": true, "LRANGE": true, "LLEN": true, "LINDEX": true, "SMEMBERS": true, "SCARD": true, "SISMEMBER": true, "SSCAN": true, "ZRANGE": true, "ZRANGEBYSCORE": true, "ZREVRANGE": true, "ZCARD": true, "ZSCORE": true, "ZSCAN": true, "XRANGE": true, "XREVRANGE": true, "XLEN": true, "SCAN": true, "DBSIZE": true, "INFO": true, "MEMORY": true, "OBJECT": true}
	redisNever = map[string]bool{"CONFIG": true, "SHUTDOWN": true, "DEBUG": true, "MODULE": true, "REPLICAOF": true, "SLAVEOF": true, "MIGRATE": true, "ACL": true, "CLIENT": true, "MONITOR": true, "SUBSCRIBE": true, "PSUBSCRIBE": true, "SYNC": true, "PSYNC": true, "FAILOVER": true, "CLUSTER": true, "BGREWRITEAOF": true, "BGSAVE": true, "SAVE": true}
)

// mongoScript reads its whole request from the environment.
const mongoScript = `
const env = process.env;
const op = env.RIVET_OP, coll = env.RIVET_COLL;
const d = db.getSiblingDB(env.RIVET_DB || 'test');
const lim = 501;
const parse = (fallback) => EJSON.parse(env.RIVET_JSON || fallback);
let out;
if (op === 'databases') out = db.adminCommand({ listDatabases: 1 }).databases.map(x => ({ name: x.name, sizeOnDisk: x.sizeOnDisk }));
else if (op === 'collections') out = d.getCollectionNames().sort().map(n => ({ name: n }));
else if (op === 'find') out = d.getCollection(coll).find(parse('{}')).limit(lim).maxTimeMS(10000).toArray();
else if (op === 'count') out = [{ count: d.getCollection(coll).countDocuments(parse('{}'), { maxTimeMS: 10000 }) }];
else if (op === 'aggregate') {
  const p = parse('[]');
  if (!Array.isArray(p) || JSON.stringify(p).match(/"\$(out|merge)"/)) throw new Error('the pipeline must be an array without $out or $merge');
  out = d.getCollection(coll).aggregate(p.concat([{ $limit: lim }]), { maxTimeMS: 10000 }).toArray();
} else if (op === 'insert') out = [d.getCollection(coll).insertOne(parse('{}'))];
else if (op === 'update') { const u = parse('{}'); out = [d.getCollection(coll).updateMany(u.filter || {}, u.update || {})]; }
else if (op === 'delete') out = [d.getCollection(coll).deleteMany(parse('{}'))];
else throw new Error('unknown operation');
print('@@RIVET@@' + EJSON.stringify(out, { relaxed: true }));
`

// Query runs q against the add-on kind of bot b.
func (a *AddonQuerier) Query(ctx context.Context, b domain.Bot, kind, password string, q AddonQuery) (AddonQueryResult, error) {
	var res AddonQueryResult
	if a == nil || a.Docker == nil {
		return res, domain.Invalid("database queries need the local Docker runner")
	}
	k, ok := addons.Get(kind)
	if !ok {
		return res, domain.ErrNotFound
	}
	has := false
	for _, x := range b.Addons {
		has = has || x.Kind == kind
	}
	if !has {
		return res, domain.Invalid("this app has no " + k.DisplayName + " add-on")
	}
	write := q.Mode == "write"
	var entry []string
	env := []string{"HOME=/tmp"}
	switch kind {
	case "postgres":
		if strings.TrimSpace(q.Query) == "" || len(q.Query) > 20000 {
			return res, domain.Invalid("enter a SQL query (at most 20000 characters)")
		}
		opts := "-c statement_timeout=10000 -c lock_timeout=3000"
		if !write {
			opts += " -c default_transaction_read_only=on"
		}
		env = append(env, "PGPASSWORD="+password, "PGOPTIONS="+opts, "PGCONNECT_TIMEOUT=5", "RIVET_Q="+q.Query)
		entry = []string{"sh", "-c", `exec psql -X -q -v ON_ERROR_STOP=1 --csv -P footer=off -h postgres -U bot -d bot -c "$RIVET_Q"`}
	case "mariadb":
		if strings.TrimSpace(q.Query) == "" || len(q.Query) > 20000 {
			return res, domain.Invalid("enter a SQL query (at most 20000 characters)")
		}
		pre := "SET SESSION max_statement_time=10; "
		if !write {
			pre += "SET SESSION TRANSACTION READ ONLY; "
		}
		env = append(env, "MYSQL_PWD="+password, "RIVET_Q="+pre+q.Query)
		entry = []string{"sh", "-c", `exec mariadb --connect-timeout=5 -h mariadb -u bot bot --batch -e "$RIVET_Q"`}
	case "redis":
		if len(q.Args) == 0 || len(q.Args) > 32 {
			return res, domain.Invalid("enter a Redis command")
		}
		cmd := strings.ToUpper(q.Args[0])
		switch {
		case redisNever[cmd]:
			return res, domain.Invalid(cmd + " is not available here")
		case !write && !redisRead[cmd]:
			return res, domain.Invalid(cmd + " changes data or is not a read command; run it as a change to confirm it")
		}
		for _, x := range q.Args {
			if len(x) > 4096 {
				return res, domain.Invalid("arguments are at most 4096 characters")
			}
		}
		entry = append([]string{"redis-cli", "-h", "redis", "--no-auth-warning"}, q.Args...)
	case "mongodb":
		readOps := map[string]bool{"databases": true, "collections": true, "find": true, "count": true, "aggregate": true}
		writeOps := map[string]bool{"insert": true, "update": true, "delete": true}
		switch {
		case readOps[q.Op]:
		case writeOps[q.Op] && write:
		case writeOps[q.Op]:
			return res, domain.Invalid(q.Op + " changes data; run it as a change to confirm it")
		default:
			return res, domain.Invalid("unknown operation " + q.Op)
		}
		if q.Op != "databases" && !mongoName.MatchString(q.DB) {
			return res, domain.Invalid("choose a database")
		}
		if q.Op != "databases" && q.Op != "collections" && !mongoName.MatchString(q.Coll) {
			return res, domain.Invalid("choose a collection")
		}
		if len(q.JSON) > 20000 {
			return res, domain.Invalid("the filter is at most 20000 characters")
		}
		env = append(env, "RIVET_PW="+password, "RIVET_OP="+q.Op, "RIVET_DB="+q.DB, "RIVET_COLL="+q.Coll, "RIVET_JSON="+q.JSON, "RIVET_SCRIPT="+mongoScript)
		entry = []string{"sh", "-c", `exec mongosh --quiet --host mongodb --username bot --password "$RIVET_PW" --authenticationDatabase admin --eval "$RIVET_SCRIPT"`}
	default:
		return res, domain.Invalid(k.DisplayName + " cannot be queried here")
	}

	start := time.Now()
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	image, err := a.Docker.ResolveImage(rctx, k.Image)
	if err != nil {
		return res, err
	}
	user := a.User
	if user == "" {
		user = "65532:65532"
	}
	spec := ContainerSpec{Name: "rivetpanel-query-" + uuid.NewString(), Role: RoleDiagnostic, BotID: b.ID, NodeID: b.NodeID, InstallID: a.InstallID,
		Image: image, Entrypoint: entry, Env: env, User: user, Network: AddonNetwork(b.ID),
		MemoryBytes: addonQueryMemory, NanoCPUs: 500_000_000, PidsLimit: 64, TmpfsBytes: 16 << 20}
	id, err := a.Docker.Create(rctx, spec)
	if err != nil {
		return res, err
	}
	defer func() {
		clean, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = a.Docker.Remove(clean, id)
	}()
	if err := a.Docker.Start(rctx, id); err != nil {
		return res, err
	}
	code, waitErr := a.Docker.Wait(rctx, id)
	out, _ := a.Docker.Tail(context.Background(), id, 20000)
	if password != "" {
		out = strings.ReplaceAll(out, password, "•••")
	}
	res.Millis = time.Since(start).Milliseconds()
	if waitErr != nil {
		if rctx.Err() != nil {
			return res, domain.Invalid("the query did not finish in time")
		}
		return res, waitErr
	}
	if len(out) > addonQueryBytes {
		out, res.Truncated = out[:addonQueryBytes], true
	}
	if code != 0 {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = fmt.Sprintf("the client exited with status %d", code)
		}
		if strings.Contains(msg, "could not translate host name") || strings.Contains(msg, "Unknown server host") || strings.Contains(msg, "ECONNREFUSED") || strings.Contains(msg, "Could not connect") {
			msg += " (is the app started? Its add-ons run while it runs)"
		}
		return res, domain.Invalid(clipQuery(msg, 2000))
	}
	switch kind {
	case "postgres":
		err = parseDelimited(out, ',', &res)
	case "mariadb":
		err = parseDelimited(out, '\t', &res)
	case "redis":
		res.Text = out
	case "mongodb":
		err = parseMongo(out, &res)
	}
	return res, err
}

func parseDelimited(out string, sep rune, res *AddonQueryResult) error {
	out = strings.TrimRight(out, "\n")
	if strings.TrimSpace(out) == "" {
		res.Text = "The statement ran and returned no rows."
		return nil
	}
	r := csv.NewReader(strings.NewReader(out))
	r.Comma, r.FieldsPerRecord, r.LazyQuotes = sep, -1, true
	if sep == '\t' {
		r.LazyQuotes, r.ReuseRecord = true, false
	}
	recs, err := r.ReadAll()
	if err != nil || len(recs) == 0 {
		res.Text = out // not a result set (for example a command tag)
		return nil
	}
	res.Columns = recs[0]
	for _, row := range recs[1:] {
		if len(res.Rows) == addonQueryRows {
			res.Truncated = true
			break
		}
		res.Rows = append(res.Rows, row)
	}
	return nil
}

func parseMongo(out string, res *AddonQueryResult) error {
	i := strings.LastIndex(out, "@@RIVET@@")
	if i < 0 {
		return domain.Invalid(clipQuery(strings.TrimSpace(out), 2000))
	}
	var docs []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(out[i+len("@@RIVET@@"):])), &docs); err != nil {
		return errors.New("unexpected answer from mongosh")
	}
	res.Columns = []string{"document"}
	for _, d := range docs {
		if len(res.Rows) == addonQueryRows {
			res.Truncated = true
			break
		}
		var buf bytes.Buffer
		if json.Indent(&buf, d, "", "  ") != nil {
			buf.Reset()
			buf.Write(d)
		}
		res.Rows = append(res.Rows, []string{buf.String()})
	}
	return nil
}

func clipQuery(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
