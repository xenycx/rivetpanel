package lazyre

import (
	"sync"
	"testing"
)

func TestLazyCompile(t *testing.T) {
	r := New(`^a([0-9]{1,3})$`)
	if r.Compiled() {
		t.Fatal("compiled before first use")
	}
	if r.String() != `^a([0-9]{1,3})$` || r.Compiled() {
		t.Fatal("String must not compile")
	}
	if !r.MatchString("a12") || r.MatchString("b12") {
		t.Fatal("wrong match result")
	}
	if !r.Compiled() {
		t.Fatal("not compiled after use")
	}
	if m := r.FindStringSubmatch("a7"); len(m) != 2 || m[1] != "7" {
		t.Fatalf("submatch %q", m)
	}
	if got := New(`[-_.]+`).ReplaceAllString("a__b.c", "-"); got != "a-b-c" {
		t.Fatalf("replace %q", got)
	}
}

func TestConcurrentFirstUse(t *testing.T) {
	r := New(`^x+$`)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !r.MatchString("xxx") {
				t.Error("no match")
			}
		}()
	}
	wg.Wait()
}

func TestInvalidPanicsOnUse(t *testing.T) {
	r := New(`(`)
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for an invalid pattern")
		}
		// Keep the registry valid for CompileAll in other tests.
		registryMu.Lock()
		for i, x := range registry {
			if x == r {
				registry = append(registry[:i], registry[i+1:]...)
				break
			}
		}
		registryMu.Unlock()
	}()
	r.MatchString("x")
}

func TestCompileAll(t *testing.T) {
	r := New(`^y$`)
	if n := CompileAll(); n < 1 || !r.Compiled() {
		t.Fatalf("CompileAll compiled %d, target compiled %v", n, r.Compiled())
	}
}
