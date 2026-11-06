// Package runtime runs extension WebAssembly modules in wazero's interpreter
// with no filesystem, environment, arguments, clock-based sleeping beyond the
// call deadline, or network: the only way out is the "rivet" host module,
// whose calls the caller authorizes one by one.
//
// wazero's compiler is used where the host allows executable memory; it is
// faster and needs far less memory than the interpreter (about a tenth for a
// typical Go extension). Under the panel's systemd unit, which sets
// MemoryDenyWriteExecute, the compiler cannot map code and the interpreter is
// used instead (see Mode). Both are pure Go, so CGO_ENABLED=0 builds work.
package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Limits on one module and its invocations.
type Limits struct {
	MemoryMB    int           // linear memory cap
	Timeout     time.Duration // per invocation (wall clock: wazero has no fuel metering)
	MaxOutput   int           // response bytes
	MaxHostCall int           // host calls per invocation
	Concurrency int           // simultaneous invocations
	PoolSize    int           // warm instances kept
	IdleTTL     time.Duration // a warm instance unused this long is closed
}

// DefaultLimits fills unset fields.
func (l Limits) withDefaults() Limits {
	if l.MemoryMB <= 0 {
		l.MemoryMB = 32
	}
	if l.Timeout <= 0 {
		l.Timeout = 5 * time.Second
	}
	if l.MaxOutput <= 0 {
		l.MaxOutput = 1 << 20
	}
	if l.MaxHostCall <= 0 {
		l.MaxHostCall = 200
	}
	if l.Concurrency <= 0 {
		l.Concurrency = 4
	}
	if l.PoolSize <= 0 {
		l.PoolSize = 2
	}
	if l.IdleTTL <= 0 {
		l.IdleTTL = 5 * time.Minute
	}
	return l
}

// HostFunc answers one capability call with a JSON envelope
// ({"ok":...} or {"error":{"code","message"}}).
type HostFunc func(ctx context.Context, name string, req []byte) []byte

// Errors an invocation can end with.
var (
	ErrTimeout  = errors.New("extension did not answer in time")
	ErrTrap     = errors.New("extension crashed")
	ErrTooLarge = errors.New("extension response is too large")
	ErrBusy     = errors.New("extension is busy")
	ErrClosed   = errors.New("extension is not running")
)

// Engines.
const (
	ModeCompiler    = "compiler"
	ModeInterpreter = "interpreter"
)

var (
	modeOnce sync.Once
	mode     string
)

// minimalWasm is (module (func (export "f") (result i32) i32.const 1)), used to
// find out whether this process may map compiled code.
var minimalWasm = []byte{0, 'a', 's', 'm', 1, 0, 0, 0, 1, 5, 1, 0x60, 0, 1, 0x7f, 3, 2, 1, 0, 7, 5, 1, 1, 'f', 0, 0, 10, 6, 1, 4, 0, 0x41, 1, 0x0b}

// Mode reports which engine runs extensions: the compiler when this platform
// supports it and the process may create executable memory, else the
// interpreter.
func Mode() string {
	modeOnce.Do(func() {
		mode = ModeInterpreter
		if probeCompiler() {
			mode = ModeCompiler
		}
	})
	return mode
}

func probeCompiler() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
	defer rt.Close(ctx)
	mod, err := rt.Instantiate(ctx, minimalWasm)
	if err != nil {
		return false
	}
	res, err := mod.ExportedFunction("f").Call(ctx)
	return err == nil && len(res) == 1 && res[0] == 1
}

func baseConfig() wazero.RuntimeConfig {
	if Mode() == ModeCompiler {
		return wazero.NewRuntimeConfigCompiler()
	}
	return wazero.NewRuntimeConfigInterpreter()
}

// global bounds every extension together.
var global = make(chan struct{}, 16)

// Module is one compiled extension version with its warm instance pool.
type Module struct {
	name   string
	limits Limits
	rt     wazero.Runtime
	code   wazero.CompiledModule
	log    *slog.Logger

	sem    chan struct{}
	mu     sync.Mutex
	idle   []*instance
	closed bool
	stop   chan struct{}

	invocations atomic.Int64
}

type instance struct {
	mod      api.Module
	alloc    api.Function
	handle   api.Function
	lastUsed time.Time
}

type callKey struct{}

// callState is the per-invocation context the host module reads.
type callState struct {
	host    HostFunc
	pending []byte
	calls   int
	max     int
	err     error
}

// Compile prepares a module. It fails for anything that is not a reactor
// exporting rivet_alloc and rivet_handle, or that imports more than WASI and
// the rivet host module.
func Compile(ctx context.Context, name string, wasm []byte, l Limits, log *slog.Logger) (*Module, error) {
	l = l.withDefaults()
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cfg := baseConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(uint32(l.MemoryMB) * 16). // 64 KiB pages
		WithCoreFeatures(api.CoreFeaturesV2)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	ok := false
	defer func() {
		if !ok {
			rt.Close(ctx)
		}
	}()
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, err
	}
	if _, err := rt.NewHostModuleBuilder("rivet").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostCall), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).Export("host_call").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostResult), []api.ValueType{api.ValueTypeI32}, nil).Export("host_result").
		Instantiate(ctx); err != nil {
		return nil, err
	}
	code, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return nil, fmt.Errorf("plugin.wasm: %w", err)
	}
	for _, imp := range code.ImportedFunctions() {
		mod, _, _ := imp.Import()
		if mod != "rivet" && mod != wasi_snapshot_preview1.ModuleName {
			return nil, fmt.Errorf("plugin.wasm imports from %q; only wasi_snapshot_preview1 and rivet are available", mod)
		}
	}
	exp := code.ExportedFunctions()
	for _, need := range []string{"rivet_alloc", "rivet_handle", "_initialize"} {
		if _, ok := exp[need]; !ok {
			return nil, fmt.Errorf("plugin.wasm does not export %s (build with -buildmode=c-shared and the rivetpanel extension SDK)", need)
		}
	}
	m := &Module{name: name, limits: l, rt: rt, code: code, log: log, sem: make(chan struct{}, l.Concurrency), stop: make(chan struct{})}
	go m.janitor()
	ok = true
	return m, nil
}

// Name is the label the module was compiled with.
func (m *Module) Name() string { return m.name }

// Invoke runs one request. The instance is reused afterwards unless the call
// trapped or timed out, in which case it is thrown away.
func (m *Module) Invoke(ctx context.Context, req []byte, host HostFunc) ([]byte, error) {
	select {
	case m.sem <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	defer func() { <-m.sem }()
	select {
	case global <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-global }()

	ctx, cancel := context.WithTimeout(ctx, m.limits.Timeout)
	defer cancel()
	st := &callState{host: host, max: m.limits.MaxHostCall}
	ctx = context.WithValue(ctx, callKey{}, st)

	inst, err := m.take(ctx)
	if err != nil {
		return nil, m.classify(ctx, err)
	}
	m.invocations.Add(1)
	out, err := m.run(ctx, inst, req)
	if err != nil {
		inst.mod.Close(context.Background())
		if st.err != nil {
			return nil, st.err
		}
		return nil, m.classify(ctx, err)
	}
	m.give(inst)
	return out, nil
}

func (m *Module) run(ctx context.Context, inst *instance, req []byte) ([]byte, error) {
	res, err := inst.alloc.Call(ctx, uint64(len(req)))
	if err != nil {
		return nil, err
	}
	ptr := uint32(res[0])
	if len(req) > 0 && !inst.mod.Memory().Write(ptr, req) {
		return nil, fmt.Errorf("%w: request buffer out of range", ErrTrap)
	}
	res, err = inst.handle.Call(ctx, uint64(ptr), uint64(len(req)))
	if err != nil {
		return nil, err
	}
	optr, olen := uint32(res[0]>>32), uint32(res[0])
	if int(olen) > m.limits.MaxOutput {
		return nil, ErrTooLarge
	}
	out, ok := inst.mod.Memory().Read(optr, olen)
	if !ok {
		return nil, fmt.Errorf("%w: response out of range", ErrTrap)
	}
	return bytes.Clone(out), nil
}

func (m *Module) classify(ctx context.Context, err error) error {
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrClosed) {
		return err
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrTimeout
		}
		return ctx.Err()
	}
	m.log.Warn("extension trapped", "extension", m.name, "err", err)
	return fmt.Errorf("%w: %v", ErrTrap, err)
}

func (m *Module) take(ctx context.Context) (*instance, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if n := len(m.idle); n > 0 {
		inst := m.idle[n-1]
		m.idle = m.idle[:n-1]
		m.mu.Unlock()
		return inst, nil
	}
	m.mu.Unlock()
	w := &boundedWriter{log: m.log, name: m.name, left: 64 << 10}
	mod, err := m.rt.InstantiateModule(ctx, m.code, wazero.NewModuleConfig().
		WithName(""). // anonymous, so several instances can coexist
		WithStartFunctions("_initialize").
		WithStdout(w).WithStderr(w).
		WithSysWalltime().WithSysNanotime(). // real clocks; no sleep beyond the deadline is possible
		WithRandSource(randReader{}))
	if err != nil {
		return nil, err
	}
	return &instance{mod: mod, alloc: mod.ExportedFunction("rivet_alloc"), handle: mod.ExportedFunction("rivet_handle")}, nil
}

func (m *Module) give(inst *instance) {
	inst.lastUsed = time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || len(m.idle) >= m.limits.PoolSize {
		inst.mod.Close(context.Background())
		return
	}
	m.idle = append(m.idle, inst)
}

func (m *Module) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		m.mu.Lock()
		keep := m.idle[:0]
		for _, inst := range m.idle {
			if time.Since(inst.lastUsed) > m.limits.IdleTTL {
				inst.mod.Close(context.Background())
			} else {
				keep = append(keep, inst)
			}
		}
		m.idle = keep
		m.mu.Unlock()
	}
}

// Invocations counts calls since the module was compiled.
func (m *Module) Invocations() int64 { return m.invocations.Load() }

// Warm reports how many instances are idle (for diagnostics and tests).
func (m *Module) Warm() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.idle)
}

// Close stops the module; running invocations end with an error.
func (m *Module) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.idle = nil
	close(m.stop)
	m.mu.Unlock()
	return m.rt.Close(ctx)
}

// hostCall is rivet.host_call(namePtr, nameLen, reqPtr, reqLen) -> replyLen.
func hostCall(ctx context.Context, mod api.Module, stack []uint64) {
	st, _ := ctx.Value(callKey{}).(*callState)
	reply := func(b []byte) {
		if st != nil {
			st.pending = b
		}
		stack[0] = uint64(uint32(len(b)))
	}
	if st == nil {
		reply([]byte(`{"error":{"code":"unavailable","message":"no invocation in progress"}}`))
		return
	}
	st.calls++
	if st.calls > st.max {
		st.err = fmt.Errorf("extension made more than %d host calls in one invocation", st.max)
		panic(st.err) // aborts the guest; the instance is discarded
	}
	mem := mod.Memory()
	name, ok1 := mem.Read(uint32(stack[0]), uint32(stack[1]))
	req, ok2 := mem.Read(uint32(stack[2]), uint32(stack[3]))
	if !ok1 || !ok2 || len(name) > 64 {
		reply([]byte(`{"error":{"code":"invalid","message":"bad host call arguments"}}`))
		return
	}
	reply(st.host(ctx, string(name), bytes.Clone(req)))
}

// hostResult is rivet.host_result(dst): copies the last reply into the guest.
func hostResult(ctx context.Context, mod api.Module, stack []uint64) {
	st, _ := ctx.Value(callKey{}).(*callState)
	if st == nil || st.pending == nil {
		return
	}
	if !mod.Memory().Write(uint32(stack[0]), st.pending) {
		panic(fmt.Errorf("host_result destination out of range"))
	}
	st.pending = nil
}

// boundedWriter sends guest stdout/stderr to the panel log, up to a budget
// per instance, so a print loop cannot flood the journal.
type boundedWriter struct {
	log  *slog.Logger
	name string
	mu   sync.Mutex
	left int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.left <= 0 {
		return len(p), nil
	}
	s := p
	if len(s) > w.left {
		s = s[:w.left]
	}
	w.left -= len(s)
	w.log.Info("extension output", "extension", w.name, "text", string(bytes.TrimRight(s, "\n")))
	if w.left <= 0 {
		w.log.Warn("extension output limit reached; further output is dropped", "extension", w.name)
	}
	return len(p), nil
}

// randReader gives the guest crypto/rand-quality randomness.
type randReader struct{}

func (randReader) Read(p []byte) (int, error) { return cryptoRead(p) }
