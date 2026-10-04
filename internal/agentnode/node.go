// Package agentnode serves the rivet-agent side of the panel connection: the
// requests the panel sends to a node (reconcile, files, console, statistics,
// game status). It is only reachable through the authenticated connection the
// agent opened to its panel.
package agentnode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/api"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/gamequery"
	"github.com/xenycx/rivetpanel/internal/nodetx"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// Runner is the part of the runner the panel drives.
type Runner interface {
	Notify(botID string)
	Purge(ctx context.Context, botID string) error
	Kill(ctx context.Context, botID string) error
	Status() runner.Status
}

// Docker is the container access the console and statistics need.
type Docker interface {
	Inspect(ctx context.Context, id string) (runner.ContainerInfo, error)
	Logs(ctx context.Context, id string, since time.Time, tail int) (io.ReadCloser, error)
	AttachStdin(ctx context.Context, id string) (io.WriteCloser, error)
	StreamStats(ctx context.Context, id string, fn func(domain.ResourceSample) bool) error
}

// Addons observes add-on containers (the runner). Optional: without it the
// add-on routes answer 503.
type Addons interface {
	AddonStates(ctx context.Context, botID string) (map[string]runner.AddonStatus, error)
	AddonLogs(ctx context.Context, botID, kind string, lines int) (string, error)
}

// AddonData deletes add-on data directories on this node (addons.DataRoot).
type AddonData interface {
	Remove(botID, kind string) error
}

// Diagnostics runs isolated AI diagnostics on this node (runner.Diagnostic):
// argv is validated against the node's runtime catalog, and the run uses a
// copy of the workspace, never the live directory. Optional: without it the
// route answers 503.
type Diagnostics interface {
	RunDiagnostic(ctx context.Context, botID, runtimeID string, argv []string) (domain.DiagnosticResult, error)
}

// maxConcurrentDiagnostics bounds the diagnostic containers one node runs at
// once, whatever the panel asks.
const maxConcurrentDiagnostics = 2

// Deps are the node's local services.
type Deps struct {
	Runner      Runner
	Addons      Addons
	AddonData   AddonData // optional: without it data removal answers 503
	Diagnostics Diagnostics
	// Telemetry reads the node's resources; nil reports only the disk of
	// the server directories (Files.Statfs).
	Telemetry func() agentproto.Telemetry
	// ProbePort tries to bind ip:port on this node; nil = BindProbe. Tests
	// replace it.
	ProbePort func(ip string, port int) error
	Docker    Docker
	Files     *filesystem.Manager
	MaxUpload int64
	// InstallID returns the panel installation the containers belong to.
	InstallID func() string
	Log       *slog.Logger
	// TransactionTTL bounds how long a staged or swapped workspace
	// replacement waits for the panel's decision before it is abandoned or
	// rolled back. 0 = nodetx.DefaultTTL.
	TransactionTTL time.Duration
}

// App builds the node API.
func App(d Deps) *fiber.App {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	// Restore archives can be larger than the interactive upload limit. Their
	// handler still applies BackupLimits while streaming and never buffers the
	// whole request in memory.
	bodyLimit := max(d.MaxUpload, int64(64<<30)+(1<<20))
	if maxInt := int64(^uint(0) >> 1); bodyLimit > maxInt {
		bodyLimit = maxInt
	}
	app := fiber.New(fiber.Config{StreamRequestBody: true, BodyLimit: int(bodyLimit), ErrorHandler: api.ErrorHandler(log)})
	v1 := app.Group("/node/v1")
	txs := nodetx.New(log, d.TransactionTTL)
	completeTx := func(c fiber.Ctx) error {
		var in agentproto.TransactionComplete
		if err := json.Unmarshal(c.Body(), &in); err != nil {
			return fiber.ErrBadRequest
		}
		if err := txs.Complete(strings.Clone(c.Params("id")), strings.Clone(c.Params("tx")), in.Commit); err != nil {
			return txError(err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
	v1.Post("/notify", func(c fiber.Ctx) error {
		var in agentproto.Notify
		if err := json.Unmarshal(c.Body(), &in); err != nil || in.BotID == "" {
			return fiber.ErrBadRequest
		}
		d.Runner.Notify(in.BotID)
		return c.SendStatus(fiber.StatusNoContent)
	})
	v1.Post("/purge", func(c fiber.Ctx) error {
		var in agentproto.Notify
		if err := json.Unmarshal(c.Body(), &in); err != nil || in.BotID == "" {
			return fiber.ErrBadRequest
		}
		if err := d.Runner.Purge(c.Context(), in.BotID); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	v1.Post("/kill", func(c fiber.Ctx) error {
		var in agentproto.Notify
		if err := json.Unmarshal(c.Body(), &in); err != nil || in.BotID == "" {
			return fiber.ErrBadRequest
		}
		if err := d.Runner.Kill(c.Context(), in.BotID); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	v1.Get("/status", func(c fiber.Ctx) error {
		st := d.Runner.Status()
		out := agentproto.Status{Ready: st.Ready == nil, Running: st.Tracked}
		if st.Ready != nil {
			out.Problem = st.Ready.Error()
		}
		return c.JSON(out)
	})
	v1.Post("/workspaces/:id", func(c fiber.Ctx) error {
		id := strings.Clone(c.Params("id"))
		if err := d.Files.Create(id); err != nil {
			if errors.Is(err, filesystem.ErrInvalidID) {
				return fiber.NewError(fiber.StatusBadRequest, "invalid server id")
			}
			if _, perr := d.Files.Path(id); perr != nil {
				return err
			}
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	// Add-on containers of a server on this node: their states and the last
	// lines of one add-on's output (bounded), for the panel's Add-ons tab.
	v1.Get("/bots/:id/addons", func(c fiber.Ctx) error {
		if d.Addons == nil {
			return fiber.ErrServiceUnavailable
		}
		id := strings.Clone(c.Params("id"))
		if !validID(id) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid server id")
		}
		st, err := d.Addons.AddonStates(c.Context(), id)
		if err != nil {
			return err
		}
		out := agentproto.AddonStates{Addons: make(map[string]agentproto.AddonStatus, len(st))}
		for k, v := range st {
			out.Addons[k] = agentproto.AddonStatus{State: v.State, Health: v.Health, ExitCode: v.ExitCode}
		}
		return c.JSON(out)
	})
	v1.Get("/bots/:id/addons/:kind/logs", func(c fiber.Ctx) error {
		if d.Addons == nil {
			return fiber.ErrServiceUnavailable
		}
		id, kind := strings.Clone(c.Params("id")), strings.Clone(c.Params("kind"))
		if !validID(id) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid server id")
		}
		if _, ok := addons.Get(kind); !ok {
			return fiber.NewError(fiber.StatusBadRequest, "unknown add-on")
		}
		lines, err := strconv.Atoi(c.Query("lines", "200"))
		if err != nil || lines < 1 || lines > agentproto.MaxAddonLogLines {
			return fiber.NewError(fiber.StatusBadRequest, "lines must be between 1 and 500")
		}
		out, err := d.Addons.AddonLogs(c.Context(), id, kind, lines)
		if err != nil {
			return err
		}
		if len(out) > agentproto.MaxAddonLogBytes {
			out = out[len(out)-agentproto.MaxAddonLogBytes:]
		}
		return c.JSON(agentproto.AddonLogs{Output: out})
	})
	// Deletes one add-on's data on this node: the panel removed the add-on,
	// or is attaching it again with a new password that old data would not
	// accept. The add-on's container is stopped (its server is stopped).
	v1.Delete("/bots/:id/addons/:kind/data", func(c fiber.Ctx) error {
		if d.AddonData == nil {
			return fiber.ErrServiceUnavailable
		}
		id, kind := strings.Clone(c.Params("id")), strings.Clone(c.Params("kind"))
		if !validID(id) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid server id")
		}
		if _, ok := addons.Get(kind); !ok {
			return fiber.NewError(fiber.StatusBadRequest, "unknown add-on")
		}
		if err := d.AddonData.Remove(id, kind); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	api.NodeFiles(v1, d.Files, d.MaxUpload)

	// Node resources, for the panel's free-disk check before it stages
	// files here and for its node charts.
	v1.Get("/telemetry", func(c fiber.Ctx) error {
		var t agentproto.Telemetry
		if d.Telemetry != nil {
			t = d.Telemetry()
		}
		if total, free, err := d.Files.Statfs(); err == nil {
			t.DiskTotalBytes, t.DiskFreeBytes = total, free
		}
		if t.SampledAtMS == 0 {
			t.SampledAtMS = time.Now().UnixMilli()
		}
		return c.JSON(t)
	})

	// An isolated AI diagnostic for a server on this node. The panel has
	// authorized the user, applied its approvals and run budget, and redacts
	// the output; this side validates the command and enforces the sandbox.
	diagSlots := make(chan struct{}, maxConcurrentDiagnostics)
	v1.Post("/bots/:id/diagnostics", func(c fiber.Ctx) error {
		if d.Diagnostics == nil {
			return fiber.ErrServiceUnavailable
		}
		id := strings.Clone(c.Params("id"))
		if !validID(id) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid server id")
		}
		var in agentproto.DiagnosticRequest
		if err := json.Unmarshal(c.Body(), &in); err != nil || in.Runtime == "" || len(in.Runtime) > 100 || len(in.Argv) == 0 || len(in.Argv) > 20 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid diagnostic request")
		}
		if _, err := d.Files.Path(id); err != nil {
			return fiber.ErrNotFound
		}
		select {
		case diagSlots <- struct{}{}:
			defer func() { <-diagSlots }()
		default:
			return fiber.NewError(fiber.StatusTooManyRequests, "this node is already running its maximum number of diagnostics; try again shortly")
		}
		res, err := d.Diagnostics.RunDiagnostic(c.Context(), id, in.Runtime, slices.Clone(in.Argv))
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			return fiber.NewError(fiber.StatusBadRequest, ve.Error())
		}
		out := agentproto.DiagnosticResult{ExitCode: res.ExitCode, Output: res.Output, DurationMS: res.Duration.Milliseconds()}
		if err != nil {
			if res.Output == "" && res.Duration == 0 {
				return err // it never started
			}
			out.Error = err.Error()
		}
		return c.JSON(out)
	})

	// Backups live on the panel, but a remote server's workspace lives here.
	// Stream its archive to the panel and keep restore swaps reversible until
	// the panel confirms that its corresponding database update succeeded.
	v1.Post("/bots/:id/backups/archive", func(c fiber.Ctx) error {
		var in agentproto.BackupRequest
		if err := json.Unmarshal(c.Body(), &in); err != nil {
			return fiber.ErrBadRequest
		}
		limits, err := archiveLimits(in.MaxEntries, in.MaxBytes, in.MaxFile)
		if err != nil {
			return err
		}
		w, err := d.Files.Open(strings.Clone(c.Params("id")))
		if err != nil {
			return fiber.ErrNotFound
		}
		c.Set(fiber.HeaderContentType, "application/gzip")
		pr, pw := io.Pipe()
		go func() {
			defer w.Close()
			_, _, err := w.WriteTarGz(pw, in.Extra, limits)
			pw.CloseWithError(err)
		}()
		return c.SendStream(pr)
	})
	v1.Post("/bots/:id/backups/restore", func(c fiber.Ctx) error {
		botID := strings.Clone(c.Params("id"))
		limits, err := queryLimits(c)
		if err != nil {
			return err
		}
		w, err := d.Files.Open(botID)
		if err != nil {
			return fiber.ErrNotFound
		}
		body := requestBody(c)
		tx, err := txs.BeginRestore(botID, func() (*filesystem.Commit, io.Closer, error) {
			_, commit, err := w.RestoreTarGzCommit(body, limits)
			return commit, w, err
		})
		if err != nil {
			return txError(err)
		}
		return c.Status(http.StatusCreated).JSON(agentproto.RestoreStarted{Transaction: tx})
	})
	// The original restore completion path stays as an alias; the panel
	// completes every workspace transaction through /transactions/:tx.
	v1.Post("/bots/:id/backups/restore/:tx", completeTx)
	v1.Post("/bots/:id/transactions/:tx", completeTx)

	// A GitHub deployment: the panel streams the repository tarball it
	// downloaded. The agent validates and stages all of it, then waits for the
	// panel to re-check that the deployment is still wanted (Apply) before the
	// first workspace entry changes. The swap stays reversible until the panel
	// has recorded the commit and completes the transaction.
	v1.Post("/bots/:id/deploy", func(c fiber.Ctx) error {
		botID := strings.Clone(c.Params("id"))
		limits, err := queryLimits(c)
		if err != nil {
			return err
		}
		rootDir := strings.Clone(c.Query("root_dir"))
		if len(rootDir) > 200 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid root directory")
		}
		w, err := d.Files.Open(botID)
		if err != nil {
			return fiber.ErrNotFound
		}
		body := requestBody(c)
		tx, err := txs.BeginDeploy(botID, w, func(gate func() error) (int, *filesystem.Commit, error) {
			return w.DeployTarGzCommit(body, rootDir, limits, func() error {
				// The tar stream is complete; consume the gzip trailer and
				// padding so the request is fully read before answering.
				_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
				return gate()
			})
		})
		if err != nil {
			return txError(err)
		}
		return c.Status(http.StatusCreated).JSON(agentproto.TransactionStarted{Transaction: tx})
	})
	// GitHub publishing for a remote server: select the files to push here,
	// with the shared filter and secret-file rules, so the panel never reads
	// (or is offered) excluded files. Contents follow through files/content.
	v1.Post("/bots/:id/push-set", func(c fiber.Ctx) error {
		var in agentproto.PushSetRequest
		if err := json.Unmarshal(c.Body(), &in); err != nil {
			return fiber.ErrBadRequest
		}
		lim, err := pushLimits(in)
		if err != nil {
			return err
		}
		w, err := d.Files.Open(strings.Clone(c.Params("id")))
		if err != nil {
			return fiber.ErrNotFound
		}
		defer w.Close()
		set, err := w.PushSet(lim)
		if err != nil {
			return txError(err)
		}
		if set.Files == nil {
			set.Files = []filesystem.PushFile{}
		}
		return c.JSON(agentproto.PushSet{Files: set.Files, Bytes: set.Bytes, Skipped: set.Skipped, Ignored: set.Ignored})
	})
	// An AI change set for a remote server: every file is checked against
	// the revision the panel's snapshot was based on, staged and swapped
	// through the journal, and kept reversible until the panel has recorded
	// the change and completes the transaction.
	v1.Post("/bots/:id/patch", func(c fiber.Ctx) error {
		botID := strings.Clone(c.Params("id"))
		var in agentproto.PatchRequest
		if err := json.NewDecoder(io.LimitReader(requestBody(c), maxPatchBody)).Decode(&in); err != nil {
			return fiber.ErrBadRequest
		}
		if in.MaxFile <= 0 || in.MaxFile > agentproto.MaxPatchFile || in.MaxTotal <= 0 || in.MaxTotal > agentproto.MaxPatchTotal {
			return fiber.NewError(fiber.StatusBadRequest, "invalid patch limits")
		}
		files := make([]filesystem.PatchFile, len(in.Files))
		for i, f := range in.Files {
			files[i] = filesystem.PatchFile{Path: f.Path, BeforeRevision: f.BeforeRevision, Mode: fs.FileMode(f.Mode).Perm()}
			if !f.Delete {
				files[i].After = f.Content
				if files[i].After == nil {
					files[i].After = []byte{}
				}
			}
		}
		w, err := d.Files.Open(botID)
		if err != nil {
			return fiber.ErrNotFound
		}
		revs := map[string]string{}
		tx, err := txs.BeginPatch(botID, func() (*filesystem.Commit, io.Closer, error) {
			commit, err := w.ApplyPatch(slices.Clone(files), in.MaxFile, in.MaxTotal)
			if err != nil {
				return nil, w, err
			}
			for _, f := range in.Files {
				if f.Delete {
					continue
				}
				rev, err := w.Revision(f.Path)
				if err != nil {
					_ = commit.Rollback()
					return nil, w, err
				}
				revs[f.Path] = rev
			}
			return commit, w, nil
		})
		if err != nil {
			return patchError(err)
		}
		return c.Status(http.StatusCreated).JSON(agentproto.PatchStarted{Transaction: tx, Revisions: revs})
	})
	v1.Post("/bots/:id/transactions/:tx/apply", func(c fiber.Ctx) error {
		n, err := txs.Apply(strings.Clone(c.Params("id")), strings.Clone(c.Params("tx")))
		if err != nil {
			return txError(err)
		}
		return c.JSON(agentproto.DeployApplied{Files: n})
	})

	// managed refuses containers this agent did not create for the panel.
	managed := func(ctx context.Context, cid string) error {
		info, err := d.Docker.Inspect(ctx, cid)
		if err != nil {
			return fiber.ErrNotFound
		}
		if info.Labels[runner.LabelManaged] != "true" {
			return fiber.ErrNotFound
		}
		if want := d.InstallID(); want != "" && info.Labels[runner.LabelInstall] != want {
			return fiber.ErrNotFound
		}
		return nil
	}
	v1.Get("/containers/:cid/logs", func(c fiber.Ctx) error {
		cid := strings.Clone(c.Params("cid"))
		if err := managed(c.Context(), cid); err != nil {
			return err
		}
		var since time.Time
		if s := c.Query("since"); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return fiber.ErrBadRequest
			}
			since = t
		}
		tail, _ := strconv.Atoi(c.Query("tail"))
		ctx, cancel := context.WithCancel(context.Background())
		rc, err := d.Docker.Logs(ctx, cid, since, tail)
		if err != nil {
			cancel()
			if errors.Is(err, runner.ErrNoContainer) {
				return fiber.ErrNotFound
			}
			return err
		}
		c.Set(fiber.HeaderContentType, "application/octet-stream")
		return c.SendStreamWriter(func(w *bufio.Writer) {
			defer cancel()
			defer rc.Close()
			buf := make([]byte, 32<<10)
			for {
				n, err := rc.Read(buf)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						return
					}
					if w.Flush() != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		})
	})
	v1.Post("/containers/:cid/stdin", func(c fiber.Ctx) error {
		cid := strings.Clone(c.Params("cid"))
		if err := managed(c.Context(), cid); err != nil {
			return err
		}
		w, err := d.Docker.AttachStdin(c.Context(), cid)
		if err != nil {
			if errors.Is(err, runner.ErrNoContainer) {
				return fiber.ErrNotFound
			}
			return err
		}
		defer w.Close()
		var body io.Reader = c.Request().BodyStream()
		if body == nil {
			body = strings.NewReader(string(c.Body()))
		}
		if _, err := io.Copy(w, io.LimitReader(body, 16<<20)); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	v1.Get("/containers/:cid/stats", func(c fiber.Ctx) error {
		cid := strings.Clone(c.Params("cid"))
		if err := managed(c.Context(), cid); err != nil {
			return err
		}
		c.Set(fiber.HeaderContentType, "application/x-ndjson")
		return c.SendStreamWriter(func(w *bufio.Writer) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			enc := json.NewEncoder(w)
			_ = d.Docker.StreamStats(ctx, cid, func(s domain.ResourceSample) bool {
				if enc.Encode(s) != nil || w.Flush() != nil {
					return false
				}
				return true
			})
		})
	})
	portsRoutes(v1, d.ProbePort)
	v1.Get("/query/minecraft", func(c fiber.Ctx) error {
		port, err := strconv.Atoi(c.Query("port"))
		if err != nil || port < 1 || port > 65535 {
			return fiber.ErrBadRequest
		}
		ctx, cancel := context.WithTimeout(c.Context(), 4*time.Second)
		defer cancel()
		st, err := gamequery.MinecraftJava(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return c.JSON(gamequery.Status{})
		}
		return c.JSON(st)
	})
	return app
}

func archiveLimits(entries int, bytes, file int64) (filesystem.BackupLimits, error) {
	if entries <= 0 || entries > 1_000_000 || bytes < 1<<20 || bytes > 64<<30 || file <= 0 || file > 64<<30 {
		return filesystem.BackupLimits{}, fiber.NewError(fiber.StatusBadRequest, "invalid archive limits")
	}
	return filesystem.BackupLimits{MaxEntries: entries, MaxBytes: bytes, MaxFile: file}, nil
}

// pushLimits accepts limits up to the shared defaults; a panel cannot ask a
// node for a larger selection than local publishing allows.
func pushLimits(in agentproto.PushSetRequest) (filesystem.PushLimits, error) {
	def := filesystem.DefaultPushLimits
	if in.MaxFiles <= 0 || in.MaxFiles > def.MaxFiles || in.MaxBytes <= 0 || in.MaxBytes > def.MaxBytes || in.MaxFile <= 0 || in.MaxFile > def.MaxFile {
		return filesystem.PushLimits{}, fiber.NewError(fiber.StatusBadRequest, "invalid push limits")
	}
	return filesystem.PushLimits{MaxFiles: in.MaxFiles, MaxBytes: in.MaxBytes, MaxFile: in.MaxFile}, nil
}

func queryLimits(c fiber.Ctx) (filesystem.BackupLimits, error) {
	maxEntries, err := strconv.Atoi(c.Query("max_entries"))
	if err != nil {
		return filesystem.BackupLimits{}, fiber.ErrBadRequest
	}
	maxBytes, err := strconv.ParseInt(c.Query("max_bytes"), 10, 64)
	if err != nil {
		return filesystem.BackupLimits{}, fiber.ErrBadRequest
	}
	maxFile, err := strconv.ParseInt(c.Query("max_file"), 10, 64)
	if err != nil {
		return filesystem.BackupLimits{}, fiber.ErrBadRequest
	}
	return archiveLimits(maxEntries, maxBytes, maxFile)
}

// txError turns transaction and archive failures into answers the panel can
// show: archive problems are the repository's or backup's fault, not the
// node's.
func txError(err error) error {
	var ae *filesystem.ErrArchive
	switch {
	case errors.Is(err, nodetx.ErrBusy):
		return fiber.NewError(fiber.StatusConflict, err.Error())
	case errors.Is(err, nodetx.ErrConflict):
		return fiber.NewError(fiber.StatusConflict, err.Error())
	case errors.Is(err, nodetx.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, err.Error())
	case errors.Is(err, nodetx.ErrAborted):
		return fiber.NewError(fiber.StatusConflict, err.Error())
	case errors.As(err, &ae):
		return fiber.NewError(fiber.StatusUnprocessableEntity, ae.Msg)
	}
	return err
}

// maxPatchBody bounds a patch request: base64 contents up to MaxPatchTotal
// plus paths and revisions.
const maxPatchBody = agentproto.MaxPatchTotal*4/3 + 1<<20

// patchError separates a stale snapshot (409), an oversized or invalid patch
// (413, 400) from node failures.
func patchError(err error) error {
	switch {
	case errors.Is(err, filesystem.ErrPatchConflict):
		return fiber.NewError(fiber.StatusConflict, err.Error())
	case errors.Is(err, filesystem.ErrTooLarge):
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "the change is too large")
	case errors.Is(err, filesystem.ErrInvalidPath), errors.Is(err, fs.ErrNotExist):
		return fiber.NewError(fiber.StatusBadRequest, "invalid path")
	case strings.HasPrefix(err.Error(), "AI patch must contain"), strings.HasPrefix(err.Error(), "duplicate patch path"):
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return txError(err)
}

func requestBody(c fiber.Ctx) io.Reader {
	if r := c.Request().BodyStream(); r != nil {
		return r
	}
	return strings.NewReader(string(c.Body()))
}

// Workspaces gives the agent's runner the node's server directories,
// creating a new server's directory on first use (the panel does not create
// directories on remote nodes).
type Workspaces struct{ *filesystem.Manager }

// Path returns the server directory, creating it when it does not exist yet.
func (w Workspaces) Path(botID string) (string, error) {
	p, err := w.Manager.Path(botID)
	if err == nil {
		return p, nil
	}
	if cerr := w.Manager.Create(botID); cerr != nil {
		return "", err
	}
	return w.Manager.Path(botID)
}

// validID reports whether id is a canonical server id.
func validID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id
}
