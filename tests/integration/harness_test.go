//go:build integration

// Package integration exercises the runner against a real Docker daemon.
//
// Run with a disposable daemon:
//
//	RIVET_TEST_DOCKER_HOST=unix:///path/docker.sock \
//	RIVET_TEST_IMAGE_PREFIX=mirror.gcr.io/library/ \   # optional registry mirror
//	RIVET_TEST_ROOTLESS=1 \                            # if the daemon is rootless
//	go test -tags integration ./tests/integration -timeout 40m
//
// Every container is created under a random node ID and labelled
// rivetpanel.managed; tests remove only containers carrying that node label.
package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

type stack struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc
	db     *sqlite.DB
	bots   *service.BotService
	ws     *filesystem.Manager
	dk     *docker.Adapter
	sdk    *client.Client
	rn     *runner.Runner
	nodeID string
	user   domain.User
	dir    string
	done   chan struct{}
	opts   runner.Options
	cat    *runtimes.Catalog
	closed bool
}

func dockerHost(t *testing.T) string {
	h := os.Getenv("RIVET_TEST_DOCKER_HOST")
	if h == "" {
		t.Skip("RIVET_TEST_DOCKER_HOST not set; skipping Docker integration tests")
	}
	return h
}

// catalog returns the embedded catalog with image references rewritten to the
// optional test registry prefix.
func catalog(t *testing.T) *runtimes.Catalog {
	prefix := os.Getenv("RIVET_TEST_IMAGE_PREFIX")
	m := fstest.MapFS{}
	entries, _ := rtdefaults.FS.ReadDir(".")
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, _ := rtdefaults.FS.ReadFile(e.Name())
		var out []string
		for _, line := range strings.Split(string(b), "\n") {
			for _, k := range []string{"image: ", "builder_image: "} {
				if strings.HasPrefix(line, k) {
					rest := strings.TrimPrefix(line, k)
					img, comment, _ := strings.Cut(rest, " ")
					line = k + prefix + img
					if comment != "" {
						line += " " + comment
					}
				}
			}
			out = append(out, line)
		}
		m[e.Name()] = &fstest.MapFile{Data: []byte(strings.Join(out, "\n"))}
	}
	c, err := runtimes.Load(m)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// stackOpts lets a second stack attach to state restored from a backup.
type stackOpts struct {
	dir      string       // base directory (default: a fresh temp dir)
	nodeID   string       // reuse an existing node id
	user     *domain.User // reuse an existing user
	restored bool         // database and keys already exist; do not seed
}

func newStack(t *testing.T, mods ...func(*runner.Options)) *stack {
	return newStackWith(t, stackOpts{}, mods...)
}

func newStackWith(t *testing.T, so stackOpts, mods ...func(*runner.Options)) *stack {
	t.Helper()
	host := dockerHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	dir := so.dir
	if dir == "" {
		dir = t.TempDir()
	}
	db, err := sqlite.Open(ctx, filepath.Join(dir, "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	nodeID := so.nodeID
	now := time.Now().UnixMilli()
	if nodeID == "" {
		nodeID = uuid.NewString()
		db.ExecContext(ctx, `INSERT INTO nodes (id,location_id,name,transport,created_at_ms,updated_at_ms) VALUES (?,?,?, 'local', ?, ?)`, nodeID, domain.LocalLocationID, "it-"+nodeID[:8], now, now)
	}
	u := domain.User{ID: uuid.NewString(), Email: "it@example.com", PasswordHash: "x", Role: domain.RoleUser, CreatedAtMS: now, UpdatedAtMS: now}
	if so.user != nil {
		u = *so.user
	} else if err := db.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.LoadDir(filepath.Join(dir, "keys"), "k1", !so.restored)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := filesystem.NewManager(filepath.Join(dir, "bots"))
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog(t)
	dk, err := docker.New(host)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	installID, err := db.InstallationID(ctx, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	user := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	owner := user
	if os.Getenv("RIVET_TEST_ROOTLESS") == "1" {
		// Container root is the unprivileged host user under a rootless daemon,
		// so workspaces stay owned by the host user.
		user = "0:0"
	}
	s := &stack{t: t, ctx: ctx, cancel: cancel, db: db, ws: ws, dk: dk, sdk: sdk, nodeID: nodeID, user: u, dir: dir, cat: cat,
		opts: runner.Options{NodeID: nodeID, InstallID: installID, User: user, WorkspaceOwner: owner, Network: "none", ResyncInterval: 2 * time.Second, StopTimeout: 3 * time.Second,
			BaseBackoff: 500 * time.Millisecond, MaxBackoff: 4 * time.Second, BuildTimeout: 8 * time.Minute}}
	for _, m := range mods {
		m(&s.opts)
	}
	s.bots = &service.BotService{Store: db, Catalog: cat, Keys: keys, Workspaces: ws, LocalNode: nodeID,
		Limits: service.Limits{MinMemoryBytes: 32 << 20, MaxMemoryBytes: 4 << 30, MinNanoCPUs: 50_000_000, MaxNanoCPUs: 4e9}}
	s.startRunner()
	t.Cleanup(s.shutdown)
	return s
}

// shutdown stops the runner, removes this stack's containers and closes
// everything. It is idempotent so tests may call it early.
func (s *stack) shutdown() {
	if s.closed {
		return
	}
	s.closed = true
	s.stopRunner()
	s.removeAllContainers()
	s.sdk.Close()
	s.dk.Close()
	s.ws.Close()
	s.db.Close()
}

func (s *stack) startRunner() {
	rn, err := runner.New(runner.Deps{Store: s.db, Docker: s.dk, Env: s.bots, Workspaces: s.ws, Catalog: s.cat,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))}, s.opts)
	if err != nil {
		s.t.Fatal(err)
	}
	s.rn = rn
	s.bots.Notifier, s.bots.Purger = rn, rn
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() { rn.Run(ctx); close(s.done) }()
	s.waitFor("runner ready", 30*time.Second, func() bool { return rn.Check(ctx) == nil })
}

// stopRunner stops the reconcile loop without touching containers, simulating
// a panel crash or restart.
func (s *stack) stopRunner() {
	s.cancel()
	<-s.done
}

func (s *stack) removeAllContainers() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	list, _ := s.sdk.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(
		filters.Arg("label", runner.LabelNode+"="+s.nodeID))})
	for _, c := range list {
		s.sdk.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
	}
}

func (s *stack) waitFor(what string, d time.Duration, f func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	b := s.lastBot()
	s.t.Fatalf("timed out waiting for %s (last bot: %+v)", what, b)
}

var lastID string

func (s *stack) lastBot() any {
	if lastID == "" {
		return nil
	}
	b, err := s.db.GetBot(context.Background(), lastID)
	if err != nil {
		return err
	}
	return fmt.Sprintf("desired=%s observed=%s gen=%d/%d err=%s", b.DesiredState, b.ObservedState, b.ObservedGeneration, b.Generation, deref(b.LastError))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *stack) createBot(rt string, mods ...func(*service.CreateBotInput)) domain.Bot {
	s.t.Helper()
	in := service.CreateBotInput{Name: "it-" + rt, Runtime: rt}
	for _, m := range mods {
		m(&in)
	}
	b, err := s.bots.Create(s.ctx, s.user, in)
	if err != nil {
		s.t.Fatal(err)
	}
	lastID = b.ID
	return b
}

func (s *stack) write(b domain.Bot, name, content string) {
	s.t.Helper()
	w, err := s.ws.Open(b.ID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write(name, strings.NewReader(content), 1<<20); err != nil {
		s.t.Fatal(err)
	}
}

func (s *stack) get(id string) domain.Bot {
	b, err := s.db.GetBot(context.Background(), id)
	if err != nil {
		s.t.Fatal(err)
	}
	return b
}

func (s *stack) waitObserved(id, state string, d time.Duration) domain.Bot {
	s.t.Helper()
	lastID = id
	s.waitFor("observed "+state, d, func() bool { return s.get(id).ObservedState == state })
	return s.get(id)
}

func (s *stack) logs(containerID string) string {
	s.t.Helper()
	rc, err := s.sdk.ContainerLogs(s.ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		s.t.Fatal(err)
	}
	defer rc.Close()
	var out, errb bytes.Buffer
	stdcopy.StdCopy(&out, &errb, rc) // Tty=false: frames are multiplexed
	return out.String() + errb.String()
}

func (s *stack) waitLog(containerID, want string, d time.Duration) string {
	s.t.Helper()
	var got string
	s.waitFor("log line "+want, d, func() bool { got = s.logs(containerID); return strings.Contains(got, want) })
	return got
}

func (s *stack) containers(botID string) []runner.ContainerInfo {
	l, err := s.dk.ListManaged(s.ctx, botID)
	if err != nil {
		s.t.Fatal(err)
	}
	return l
}

var _ = io.Discard
