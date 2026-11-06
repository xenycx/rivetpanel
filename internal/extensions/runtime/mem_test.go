package runtime

import (
	"context"
	goruntime "runtime"
	"testing"
	"time"
)

func heap() uint64 {
	goruntime.GC()
	goruntime.GC()
	var m goruntime.MemStats
	goruntime.ReadMemStats(&m)
	return m.HeapInuse
}

func TestMemoryFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	wasm := testWasm(t)
	base := heap()
	m, err := Compile(context.Background(), "mem", wasm, Limits{MemoryMB: 32, Timeout: 10 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled := heap()
	if _, err := call(t, m, "/echo"); err != nil {
		t.Fatal(err)
	}
	one := heap()
	t.Logf("wasm %d KiB; compiled +%d MiB; first instance +%d MiB", len(wasm)>>10, (compiled-base)>>20, (one-compiled)>>20)
	m.Close(context.Background())
	t.Logf("after close: +%d MiB over base", (heap()-base)>>20)
}

func TestModeProbe(t *testing.T) {
	t.Logf("engine: %s", Mode())
	if Mode() != ModeCompiler && Mode() != ModeInterpreter {
		t.Fatal(Mode())
	}
}
