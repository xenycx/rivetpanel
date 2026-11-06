package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	wasmBytes []byte
	buildErr  error
)

// testWasm builds extensions/testplugin for wasip1 once per test run.
func testWasm(t testing.TB) []byte {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rivet-ext-test")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "plugin.wasm")
		_, file, _, _ := goruntime.Caller(0)
		root := filepath.Join(filepath.Dir(file), "..", "..", "..")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-o", out, "./extensions/testplugin")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = errors.New(string(b))
			return
		}
		wasmBytes, buildErr = os.ReadFile(out)
	})
	if buildErr != nil {
		t.Skip("cannot build the wasip1 test plugin: ", buildErr)
	}
	return wasmBytes
}

func echoHost(ctx context.Context, name string, req []byte) []byte {
	if name == "echo" {
		return []byte(`{"ok":` + string(req) + `}`)
	}
	return []byte(`{"error":{"code":"denied","message":"` + name + ` is not granted"}}`)
}

func call(t testing.TB, m *Module, path string) (map[string]any, error) {
	t.Helper()
	req, _ := json.Marshal(map[string]any{"kind": "route", "path": path})
	out, err := m.Invoke(context.Background(), req, echoHost)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status int
		Body   json.RawMessage
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("%s: %s", err, out)
	}
	var body map[string]any
	json.Unmarshal(resp.Body, &body)
	if body == nil {
		body = map[string]any{}
	}
	body["_status"] = float64(resp.Status)
	return body, nil
}

func compile(t testing.TB, l Limits) *Module {
	t.Helper()
	m, err := Compile(context.Background(), "test", testWasm(t), l, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	return m
}

func TestInvokeAndReuse(t *testing.T) {
	m := compile(t, Limits{MemoryMB: 64, Timeout: 10 * time.Second})
	for i := 1; i <= 3; i++ {
		body, err := call(t, m, "/echo")
		if err != nil {
			t.Fatal(err)
		}
		if body["counter"] != float64(i) {
			t.Fatalf("instance was not reused: %v", body)
		}
		if echo, _ := body["echo"].(map[string]any); echo["path"] != "/echo" {
			t.Fatalf("%v", body)
		}
	}
	if m.Warm() != 1 {
		t.Fatalf("warm = %d", m.Warm())
	}
}

func TestSandboxLimits(t *testing.T) {
	m := compile(t, Limits{MemoryMB: 32, Timeout: 2 * time.Second, MaxHostCall: 50})

	t.Run("denied capability reaches the guest as denied", func(t *testing.T) {
		body, err := call(t, m, "/denied")
		if err != nil || body["denied"] != true {
			t.Fatalf("%v %v", body, err)
		}
	})
	t.Run("infinite loop times out", func(t *testing.T) {
		start := time.Now()
		if _, err := call(t, m, "/loop"); !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v", err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("deadline not enforced promptly")
		}
	})
	t.Run("memory cap traps", func(t *testing.T) {
		if _, err := call(t, m, "/memory"); !errors.Is(err, ErrTrap) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no files, environment, arguments or network", func(t *testing.T) {
		body, err := call(t, m, "/file")
		if err != nil {
			t.Fatal(err)
		}
		if body["file_err"] == "" || body["net_err"] == "" {
			t.Fatalf("escaped the sandbox: %v", body)
		}
		if env, _ := body["env"].([]any); len(env) != 0 {
			t.Fatalf("environment leaked: %v", env)
		}
	})
	t.Run("host call budget", func(t *testing.T) {
		if _, err := call(t, m, "/hostcalls"); err == nil || !strings.Contains(err.Error(), "host calls") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("oversized response", func(t *testing.T) {
		if _, err := call(t, m, "/big"); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("panic becomes a 500", func(t *testing.T) {
		body, err := call(t, m, "/panic")
		if err != nil || body["_status"] != float64(500) {
			t.Fatalf("%v %v", body, err)
		}
	})
	t.Run("output flood is bounded", func(t *testing.T) {
		if _, err := call(t, m, "/flood"); err != nil && !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("still serves after all that", func(t *testing.T) {
		if _, err := call(t, m, "/echo"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRejectsForeignImports(t *testing.T) {
	// (module (import "env" "x" (func)) ) — imports from a module we do not provide.
	wasm := []byte{0, 'a', 's', 'm', 1, 0, 0, 0, 1, 4, 1, 0x60, 0, 0, 2, 9, 1, 3, 'e', 'n', 'v', 1, 'x', 0, 0}
	if _, err := Compile(context.Background(), "bad", wasm, Limits{}, nil); err == nil {
		t.Fatal("accepted")
	}
}

func BenchmarkWarmInvoke(b *testing.B) {
	m := compile(b, Limits{MemoryMB: 64, Timeout: 10 * time.Second})
	if _, err := call(b, m, "/echo"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := call(b, m, "/echo"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestColdAndWarmLatency(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	start := time.Now()
	m := compile(t, Limits{MemoryMB: 64, Timeout: 30 * time.Second})
	compiled := time.Since(start)
	start = time.Now()
	if _, err := call(t, m, "/echo"); err != nil {
		t.Fatal(err)
	}
	cold := time.Since(start)
	var worst time.Duration
	for i := 0; i < 20; i++ {
		s := time.Now()
		if _, err := call(t, m, "/echo"); err != nil {
			t.Fatal(err)
		}
		worst = max(worst, time.Since(s))
	}
	t.Logf("compile %v, first call %v, worst warm call %v", compiled, cold, worst)
	if worst > 300*time.Millisecond {
		t.Errorf("warm calls are too slow for Discord interactions: %v", worst)
	}
}
