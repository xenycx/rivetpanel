package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// SQLiteImage provides the client for SQLite queries: Python's sqlite3
// module. It is the Python runtime's image, so most hosts already have it.
const SQLiteImage = "python:3.11-alpine"

// sqliteFiles is where the app's files appear in the query container.
const sqliteFiles = "/files"

// sqliteScript runs one request against a SQLite file. Everything it needs
// arrives through the environment. Reads open the file read-only on a
// read-only mount, with query_only set; a changed file is impossible, not
// merely refused. Changes run in one transaction that is rolled back on any
// error. ATTACH is refused, so a query reaches only the chosen file.
const sqliteScript = `
import json, os, sqlite3, sys, time, urllib.parse
env = os.environ
path = '` + sqliteFiles + `/' + env['RIVET_FILE']
write = env.get('RIVET_MODE') == 'write'
ROWS, BUDGET, CELL = 500, 180000, 10000
deadline = time.monotonic() + 10

def fail(msg):
    sys.stderr.write(str(msg) + '\n')
    sys.exit(1)

def guard(action, *rest):
    return sqlite3.SQLITE_DENY if action in (sqlite3.SQLITE_ATTACH, sqlite3.SQLITE_DETACH) else sqlite3.SQLITE_OK

def connect(opts):
    c = sqlite3.connect('file:' + urllib.parse.quote(path) + '?' + opts, uri=True, timeout=5, isolation_level=None)
    c.execute('PRAGMA trusted_schema=OFF')
    if not write:
        c.execute('PRAGMA query_only=ON')
    c.execute('SELECT count(*) FROM sqlite_master').fetchall()
    return c

def statements(sql):
    # Splits on semicolons that end a statement (not those in strings,
    # identifiers or comments).
    parts = sql.split(';')
    out, buf = [], ''
    for i, part in enumerate(parts):
        buf += part
        last = i == len(parts) - 1
        if last or sqlite3.complete_statement(buf + ';'):
            if buf.strip():
                out.append(buf if last else buf + ';')
            buf = ''
        else:
            buf += ';'
    return out

def cell(v):
    if v is None:
        return 'NULL'
    if isinstance(v, bytes):
        s = "x'" + v[:64].hex() + "'"
        return s + (' … (%d bytes)' % len(v) if len(v) > 64 else '')
    s = str(v)
    return s if len(s) <= CELL else s[:CELL] + '…'

if not os.path.isfile(path):
    fail('the file no longer exists')
try:
    if write:
        con = connect('mode=rw')
    else:
        try:
            con = connect('mode=ro')
        except sqlite3.OperationalError:
            # A WAL database whose -wal and -shm files are gone (the app is
            # stopped) cannot be opened read-only on a read-only folder
            # otherwise; with no writer there is nothing to coordinate with.
            con = connect('mode=ro&immutable=1')
    con.set_authorizer(guard)
    con.set_progress_handler(lambda: 1 if time.monotonic() > deadline else 0, 10000)
    stmts = statements(env['RIVET_Q'])
    if not stmts:
        fail('enter a SQL statement')
    con.execute('BEGIN IMMEDIATE' if write else 'BEGIN')
    before = con.total_changes
    res = {}
    for s in stmts:
        cur = con.execute(s)
        if cur.description:
            cols = [d[0] for d in cur.description]
            rows, size, more = [], 0, False
            while True:
                r = cur.fetchone()
                if r is None:
                    break
                if len(rows) == ROWS or size > BUDGET:
                    more = True
                    break
                row = [cell(v) for v in r]
                size += sum(len(x.encode()) + 4 for x in row)
                rows.append(row)
            res = {'columns': cols, 'rows': rows, 'truncated': more}
        cur.close()
    changed = con.total_changes - before
    con.execute('COMMIT' if write else 'ROLLBACK')
    if not res:
        res = {'text': ('%d row%s changed.' % (changed, '' if changed == 1 else 's')) if write else 'The statement ran and returned no rows.'}
    print('@@RIVET@@' + json.dumps(res, ensure_ascii=False))
except sqlite3.Error as e:
    msg = str(e)
    if msg == 'interrupted':
        msg = 'the query took longer than 10 seconds and was stopped'
    elif msg == 'not authorized':
        msg = 'ATTACH and DETACH are not available here'
    fail(msg)
`

// QuerySQLite runs q against the SQLite file rel (workspace-relative) of bot
// b, whose files are at hostDir. The client runs as uid:gid (the file's
// owner, the user the app writes it as), with no network, a read-only root
// and small limits; the app's files are mounted read-only unless the person
// confirmed a change. The query text travels only through the environment.
func (a *AddonQuerier) QuerySQLite(ctx context.Context, b domain.Bot, hostDir, rel string, uid, gid int, q AddonQuery) (AddonQueryResult, error) {
	var res AddonQueryResult
	if a == nil || a.Docker == nil {
		return res, domain.Invalid("database queries need the local Docker runner")
	}
	if hostDir == "" || !filepath.IsLocal(rel) || strings.ContainsRune(rel, 0) {
		return res, domain.Invalid("choose a SQLite file")
	}
	if strings.TrimSpace(q.Query) == "" || len(q.Query) > 20000 {
		return res, domain.Invalid("enter a SQL query (at most 20000 characters)")
	}
	write := q.Mode == "write"
	user := a.User
	if uid > 0 && gid >= 0 {
		user = fmt.Sprintf("%d:%d", uid, gid)
	}
	if user == "" {
		user = "65532:65532"
	}
	mode := "read"
	if write {
		mode = "write"
	}
	start := time.Now()
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	image, err := a.Docker.ResolveImage(rctx, SQLiteImage)
	if err != nil {
		return res, err
	}
	spec := ContainerSpec{Name: "rivetpanel-query-" + uuid.NewString(), Role: RoleDiagnostic, BotID: b.ID, NodeID: b.NodeID, InstallID: a.InstallID,
		Image: image, Entrypoint: []string{"python3", "-I", "-c", sqliteScript},
		Env:  []string{"HOME=/tmp", "PYTHONDONTWRITEBYTECODE=1", "PYTHONIOENCODING=utf-8", "RIVET_FILE=" + rel, "RIVET_MODE=" + mode, "RIVET_Q=" + q.Query},
		User: user, Network: "none", Mounts: []Mount{{Source: hostDir, Target: sqliteFiles, ReadOnly: !write}},
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
	res.Millis = time.Since(start).Milliseconds()
	if waitErr != nil {
		if rctx.Err() != nil {
			return res, domain.Invalid("the query did not finish in time")
		}
		return res, waitErr
	}
	if code != 0 {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = fmt.Sprintf("the client exited with status %d", code)
		}
		switch {
		case !write && strings.Contains(msg, "attempt to write a readonly database"):
			msg = "Run is read-only and cannot change the file; use Run as change… to confirm a change"
		case strings.Contains(msg, "attempt to write a readonly database") || strings.Contains(msg, "unable to open database file"):
			msg += " (the query runs as " + user + ", the file's owner, who needs access to the file and its folder)"
		}
		return res, domain.Invalid(clipQuery(msg, 2000))
	}
	millis := res.Millis
	if err := parseSQLite(out, &res); err != nil {
		return res, err
	}
	res.Millis = millis
	return res, nil
}

func parseSQLite(out string, res *AddonQueryResult) error {
	i := strings.LastIndex(out, "@@RIVET@@")
	if i < 0 {
		return domain.Invalid(clipQuery(strings.TrimSpace(out), 2000))
	}
	var r AddonQueryResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out[i+len("@@RIVET@@"):])), &r); err != nil {
		return errors.New("unexpected answer from the SQLite client")
	}
	*res = r
	return nil
}
