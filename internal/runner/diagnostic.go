package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	operator "github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runtimes"
)

// Diagnostic executes administrator-approved direct argv in a disposable,
// offline builder container. It never mounts the live bot workspace.
type Diagnostic struct {
	Docker                                       Docker
	Files                                        *filesystem.Manager
	Catalog                                      *runtimes.Catalog
	ScratchRoot, User, InstallID, NodeID         string
	Timeout                                      time.Duration
	MemoryBytes, NanoCPUs, PidsLimit, TmpfsBytes int64
}

func (d *Diagnostic) RunDiagnostic(ctx context.Context, botID, runtimeID string, argv []string) (domain.DiagnosticResult, error) {
	start := time.Now()
	var out domain.DiagnosticResult
	if d == nil || d.Docker == nil || d.Files == nil || d.Catalog == nil {
		return out, errors.New("diagnostic runner is unavailable")
	}
	rt, ok := d.Catalog.Get(runtimeID)
	if !ok {
		return out, errors.New("runtime is unavailable")
	}
	if err := validateDiagnostic(rt, argv); err != nil {
		return out, err
	}
	root := d.ScratchRoot
	if root == "" {
		root = os.TempDir()
	}
	// Docker refuses relative bind-mount sources, and a development panel runs
	// with a relative data directory.
	root, err := filepath.Abs(root)
	if err != nil {
		return out, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return out, err
	}
	scratch, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(scratch)
	if err = copyDiagnosticSnapshot(d.Files, botID, scratch); err != nil {
		return out, err
	}
	image, err := d.Docker.ResolveImage(ctx, rt.BuilderRef())
	if err != nil {
		return out, err
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if timeout > 20*time.Minute {
		timeout = 20 * time.Minute
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	mem := d.MemoryBytes
	if mem <= 0 {
		mem = 768 << 20
	}
	cpu := d.NanoCPUs
	if cpu <= 0 {
		cpu = 1e9
	}
	pids := d.PidsLimit
	if pids <= 0 {
		pids = 256
	}
	tmp := d.TmpfsBytes
	if tmp <= 0 {
		tmp = 128 << 20
	}
	user := d.User
	if user == "" {
		user = "1000:1000"
	}
	spec := ContainerSpec{Name: "rivetpanel-diag-" + uuid.NewString(), Role: RoleDiagnostic, BotID: botID, NodeID: d.NodeID, InstallID: d.InstallID, Image: image, Argv: append([]string(nil), argv...), Env: envList(rt.Env), WorkspaceHostPath: scratch, User: user, Network: "none", MemoryBytes: mem, NanoCPUs: cpu, PidsLimit: pids, TmpfsBytes: tmp}
	id, err := d.Docker.Create(rctx, spec)
	if err != nil {
		return out, err
	}
	defer func() {
		clean, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = d.Docker.Remove(clean, id)
	}()
	if err = d.Docker.Start(rctx, id); err != nil {
		return out, err
	}
	code, waitErr := d.Docker.Wait(rctx, id)
	out.ExitCode = code
	logs, logErr := d.Docker.Tail(context.Background(), id, 10000)
	out.Output = clipDiagnostic(logs, 1<<20)
	out.Duration = time.Since(start)
	if waitErr != nil {
		return out, waitErr
	}
	if logErr != nil {
		return out, logErr
	}
	return out, nil
}

func validateDiagnostic(rt runtimes.Runtime, argv []string) error {
	if len(argv) == 0 || len(argv) > 20 {
		return domain.Invalid("diagnostic argv is empty or too long")
	}
	// python3 is the same interpreter as python for policy purposes.
	head := argv[0]
	if head == "python3" {
		head = "python"
	}
	allowed := false
	for _, p := range rt.DiagnosticCommands {
		if len(argv) < len(p) || p[0] != head {
			continue
		}
		same := true
		for i := 1; i < len(p); i++ {
			if argv[i] != p[i] {
				same = false
				break
			}
		}
		if same {
			allowed = true
			break
		}
	}
	if !allowed {
		return domain.Invalid(notAllowedMessage(rt))
	}
	if interpreterEval(head, argv[1:]) {
		return domain.Invalid("interpreter evaluation flags are not allowed")
	}
	for i, a := range argv {
		if a == "" || len(a) > 500 || strings.ContainsAny(a, "\x00\r\n") {
			return domain.Invalid("diagnostic argument is invalid")
		}
		if strings.HasPrefix(a, "/") && !strings.HasPrefix(a, "/workspace/") && a != "/workspace" {
			return domain.Invalid("diagnostic paths must stay inside /workspace")
		}
		for _, seg := range strings.Split(strings.ReplaceAll(a, "\\", "/"), "/") {
			if seg == ".." {
				return domain.Invalid("diagnostic paths may not escape /workspace")
			}
		}
		if i > 0 && strings.ContainsAny(a, ";&|`$><") {
			return domain.Invalid("shell syntax is not accepted; commands use direct argv")
		}
	}
	return nil
}

// interpreterEval reports arguments that would run code given on the command
// line instead of a file in the workspace.
func interpreterEval(head string, args []string) bool {
	for _, a := range args {
		a = strings.ToLower(a)
		switch head {
		case "node":
			if a == "-e" || a == "--eval" || a == "-p" || a == "--print" || a == "-pe" || strings.HasPrefix(a, "--eval=") || strings.HasPrefix(a, "--print=") || strings.HasPrefix(a, "--input-type") {
				return true
			}
		case "ruby":
			if a == "-e" {
				return true
			}
		case "python":
			if a == "-m" {
				return false // what follows belongs to the module
			}
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
				return true
			}
		}
	}
	return false
}

// notAllowedMessage tells the caller exactly what is allowed, so a model that
// asked for a shell command can pick a real one instead of guessing again.
func notAllowedMessage(rt runtimes.Runtime) string {
	var prefixes []string
	for _, p := range rt.DiagnosticCommands {
		prefixes = append(prefixes, strings.Join(p, " "))
	}
	name := rt.DisplayName
	if name == "" {
		name = rt.ID
	}
	return fmt.Sprintf("that command is not allowed for the %s runtime. Allowed command prefixes (arguments such as file paths may follow): %s. It is not a shell, so pipes, ls, cat and grep do not work: use read_file, list_files, search_files, read_logs and build_output to inspect instead", name, strings.Join(prefixes, "; "))
}

func copyDiagnosticSnapshot(m *filesystem.Manager, botID, dst string) error {
	w, e := m.Open(botID)
	if e != nil {
		return e
	}
	defer w.Close()
	var files int
	var total int64
	var walk func(string) error
	walk = func(dir string) error {
		es, e := w.List(dir)
		if e != nil {
			return e
		}
		for _, x := range es {
			p := x.Name
			if dir != "." {
				p = path.Join(dir, x.Name)
			}
			if x.Symlink || operator.ProtectedPath(p) {
				continue
			}
			base := path.Base(p)
			if x.IsDir && (base == ".git" || base == ".rivetpanel") {
				continue
			}
			target := filepath.Join(dst, filepath.FromSlash(p))
			if x.IsDir {
				if e = os.MkdirAll(target, 0o750); e != nil {
					return e
				}
				if e = walk(p); e != nil {
					return e
				}
				continue
			}
			if x.Size > 1<<20 {
				continue
			}
			files++
			total += x.Size
			if files > 60000 || total > 512<<20 {
				return errors.New("safe diagnostic snapshot exceeds its file or byte limit")
			}
			b, e := w.Read(p, 1<<20)
			if e != nil {
				return e
			}
			if bytes.IndexByte(b, 0) >= 0 {
				continue
			}
			if e = os.MkdirAll(filepath.Dir(target), 0o750); e != nil {
				return e
			}
			if e = os.WriteFile(target, b, 0o640); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(".")
}

// CleanDiagnosticScratch removes snapshot copies left in root by diagnostic
// runs that were interrupted (the process stopped mid-run). Only the run-*
// directories RunDiagnostic creates are touched.
func CleanDiagnosticScratch(root string, log interface{ Warn(string, ...any) }) {
	if root == "" {
		return
	}
	es, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range es {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "run-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil && log != nil {
			log.Warn("could not remove a leftover diagnostic snapshot", "dir", e.Name(), "err", err)
		}
	}
}

func clipDiagnostic(v string, n int) string {
	if len(v) <= n {
		return v
	}
	return v[:n] + "…"
}

var _ fs.FileMode
var _ = fmt.Sprintf
