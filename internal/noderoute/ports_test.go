package noderoute_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/agentnode"
)

// TestRemotePortProbe runs the protocol 8 port probe through the real hub
// and agent: the node binds the ports itself (here: a port this test holds
// is reported busy with a reason, a released one free), long lists are sent
// in bounded batches, and invalid requests are refused by the node.
func TestRemotePortProbe(t *testing.T) {
	r, nodeID, _, _ := remoteNodeWith(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	busy := held.Addr().(*net.TCPAddr).Port
	spare, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	free := spare.Addr().(*net.TCPAddr).Port
	spare.Close()

	res, err := r.ProbePorts(ctx, nodeID, "127.0.0.1", []int{busy, free})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Port != busy || res[0].Free || res[0].Reason != "already in use on the node" ||
		res[1].Port != free || !res[1].Free {
		t.Fatalf("probe = %+v", res)
	}

	for _, bad := range []struct {
		ip    string
		ports []int
	}{{"localhost", []int{free}}, {"127.0.0.1", []int{0}}, {"127.0.0.1", []int{70000}}} {
		if _, err := r.ProbePorts(ctx, nodeID, bad.ip, bad.ports); err == nil || !strings.Contains(err.Error(), "400") {
			t.Fatalf("probe %s %v: %v", bad.ip, bad.ports, err)
		}
	}
}

func TestRemotePortProbeBatches(t *testing.T) {
	var mu sync.Mutex
	seen := 0
	r, nodeID, _, _ := remoteNodeWith(t, func(d *agentnode.Deps) {
		d.ProbePort = func(ip string, port int) error {
			mu.Lock()
			defer mu.Unlock()
			seen++
			if port%2 == 0 {
				return &net.OpError{Op: "listen", Net: "tcp", Err: errInUse}
			}
			return nil
		}
	})
	ports := make([]int, 150)
	for i := range ports {
		ports[i] = 30000 + i
	}
	res, err := r.ProbePorts(context.Background(), nodeID, "0.0.0.0", ports)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 150 || seen != 150 {
		t.Fatalf("results %d, probed %d", len(res), seen)
	}
	for i, p := range res {
		if p.Port != ports[i] || p.Free != (p.Port%2 == 1) {
			t.Fatalf("result %d = %+v", i, p)
		}
	}
}

var errInUse = syscall.EADDRINUSE
