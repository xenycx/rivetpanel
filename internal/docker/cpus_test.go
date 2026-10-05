package docker

import "testing"

// A CPU limit above the host's CPU count is lowered to it: Docker refuses
// such a container ("range of CPUs is from 0.01 to 2.00").
func TestFitCPUsToHost(t *testing.T) {
	a := &Adapter{}
	a.ncpu.Store(2) // cached: no daemon is asked
	for in, want := range map[int64]int64{2_300_000_000: 2e9, 2e9: 2e9, 5e8: 5e8} {
		if got := a.fitCPUs(t.Context(), in); got != want {
			t.Errorf("fitCPUs(%d) = %d, want %d", in, got, want)
		}
	}
}
