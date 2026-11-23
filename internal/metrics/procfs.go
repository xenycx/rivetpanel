// Package metrics answers "what was this node doing, and which app or process
// caused it": per-app samples every 15 seconds, process snapshots with
// container attribution, CPU spike records, a fast in-memory host series for
// live charts, and columnar range queries for the charts and the assistant.
//
// Everything here only reads: /proc and the cgroup filesystem of the host,
// Docker's statistics, and the agents of remote nodes. Process command lines
// are cut short and have secrets replaced before they are stored or shown;
// process environments are never read.
package metrics

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/telemetry"
)

// userHZ is the kernel's clock tick for /proc/<pid>/stat times. Linux reports
// these in USER_HZ, which is 100 on every architecture the panel supports.
const userHZ = 100

// maxCmdRead bounds how much of a command line is read.
const maxCmdRead = 4096

// RawProcess is one process as read from /proc, before it is matched to an
// app and its command line is redacted.
type RawProcess struct {
	PID         int
	Comm        string
	Cmd         string // NUL separators replaced by spaces, at most maxCmdRead bytes
	CPUPct      float64
	RSSBytes    int64
	ContainerID string // 64 hex characters, "" for host processes
}

// ProcSampler reads processes from a proc filesystem (the host's, mounted
// read-only into the panel's container at RIVET_HOST_PROC).
type ProcSampler struct {
	Root string // default /proc
	// Gap is the time between the two reads a CPU percentage needs (default
	// one second).
	Gap time.Duration
}

func (p ProcSampler) root() string {
	if p.Root == "" {
		return "/proc"
	}
	return p.Root
}

// ProcSample is one sampling result.
type ProcSample struct {
	At         time.Time
	HostCPUPct float64 // whole host, 0-100
	Processes  []RawProcess
	// Total is the number of processes seen before limiting.
	Total int
	// Containers is the number of processes that belong to a container.
	Containers int
}

type procTimes struct {
	ticks uint64
	start uint64
	rss   int64
	comm  string
}

// readStat parses /proc/<pid>/stat. The command name is in parentheses and
// may itself contain spaces and parentheses, so fields are counted from the
// last closing parenthesis.
func readStat(b []byte) (procTimes, bool) {
	open := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return procTimes{}, false
	}
	f := strings.Fields(string(b[end+1:]))
	// f[0] is field 3 (state): utime is field 14, stime 15, starttime 22, rss 24.
	if len(f) < 22 {
		return procTimes{}, false
	}
	ut, err1 := strconv.ParseUint(f[11], 10, 64)
	st, err2 := strconv.ParseUint(f[12], 10, 64)
	start, _ := strconv.ParseUint(f[19], 10, 64)
	rss, _ := strconv.ParseInt(f[21], 10, 64)
	if err1 != nil || err2 != nil {
		return procTimes{}, false
	}
	return procTimes{ticks: ut + st, start: start, rss: max(rss, 0) * int64(os.Getpagesize()), comm: string(b[open+1 : end])}, true
}

func (p ProcSampler) readAll() map[int]procTimes {
	out := map[int]procTimes{}
	ents, err := os.ReadDir(p.root())
	if err != nil {
		return out
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(p.root(), e.Name(), "stat"))
		if err != nil {
			continue // exited meanwhile, or not readable
		}
		if t, ok := readStat(b); ok {
			out[pid] = t
		}
	}
	return out
}

// containerRe finds a Docker container ID in a /proc/<pid>/cgroup line
// (cgroup v2 "0::/system.slice/docker-<id>.scope", "0::/docker/<id>", or v1
// "4:cpu,cpuacct:/docker/<id>").
var containerRe = regexp.MustCompile(`(?:docker[-/]|/)([0-9a-f]{64})(?:\.scope)?(?:/|$)`)

// ContainerOf returns the container ID in a cgroup file's contents ("" =
// none).
func ContainerOf(cgroup []byte) string {
	for _, line := range strings.Split(string(cgroup), "\n") {
		if m := containerRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// containerOf reads a process's container ID.
func (p ProcSampler) containerOf(pid int) string {
	b, err := os.ReadFile(filepath.Join(p.root(), strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	return ContainerOf(b)
}

func (p ProcSampler) cmdline(pid int, comm string) string {
	f, err := os.Open(filepath.Join(p.root(), strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return "[" + comm + "]"
	}
	defer f.Close()
	b := make([]byte, maxCmdRead)
	n, _ := f.Read(b)
	b = bytes.TrimRight(b[:n], "\x00")
	if len(b) == 0 {
		return "[" + comm + "]" // a kernel thread
	}
	return string(bytes.ReplaceAll(b, []byte{0}, []byte{' '}))
}

// Filter selects which processes Sample returns.
type Filter struct {
	Limit int // busiest first; 0 = 25
	// Container keeps only processes of this container (full ID).
	Container string
}

// Sample reads every process twice, Gap apart, and returns the busiest ones
// (CPU first, then memory) with their container and command line. Only the
// returned processes have their command line and cgroup read, unless
// filtering by container needs every cgroup.
func (p ProcSampler) Sample(ctx context.Context, f Filter) (ProcSample, error) {
	gap := p.Gap
	if gap <= 0 {
		gap = time.Second
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 25
	}
	host := telemetry.ProcReader{Root: p.root()}
	busy1, total1, herr := host.CPU()
	first := p.readAll()
	if len(first) == 0 {
		return ProcSample{}, errors.New("no processes are visible in " + p.root())
	}
	t1 := time.Now()
	select {
	case <-ctx.Done():
		return ProcSample{}, ctx.Err()
	case <-time.After(gap):
	}
	second := p.readAll()
	busy2, total2, herr2 := host.CPU()
	elapsed := time.Since(t1).Seconds()
	out := ProcSample{At: time.Now(), Total: len(second)}
	if herr == nil && herr2 == nil && total2 > total1 {
		out.HostCPUPct = min(max(float64(int64(busy2)-int64(busy1))/float64(total2-total1)*100, 0), 100)
	}
	procs := make([]RawProcess, 0, len(second))
	for pid, t := range second {
		r := RawProcess{PID: pid, Comm: t.comm, RSSBytes: t.rss}
		if a, ok := first[pid]; ok && a.start == t.start && t.ticks >= a.ticks && elapsed > 0 {
			r.CPUPct = float64(t.ticks-a.ticks) / userHZ / elapsed * 100
		}
		procs = append(procs, r)
	}
	if f.Container != "" {
		kept := procs[:0]
		for _, r := range procs {
			if p.containerOf(r.PID) == f.Container {
				r.ContainerID = f.Container
				kept = append(kept, r)
			}
		}
		procs = kept
		out.Containers = len(procs)
	}
	sort.Slice(procs, func(i, j int) bool {
		if procs[i].CPUPct != procs[j].CPUPct {
			return procs[i].CPUPct > procs[j].CPUPct
		}
		if procs[i].RSSBytes != procs[j].RSSBytes {
			return procs[i].RSSBytes > procs[j].RSSBytes
		}
		return procs[i].PID < procs[j].PID
	})
	if len(procs) > limit {
		procs = procs[:limit]
	}
	for i := range procs {
		if f.Container == "" {
			procs[i].ContainerID = p.containerOf(procs[i].PID)
			if procs[i].ContainerID != "" {
				out.Containers++
			}
		}
		procs[i].Cmd = p.cmdline(procs[i].PID, procs[i].Comm)
	}
	out.Processes = procs
	return out, nil
}

// SeesContainers reports whether processes of containerID are visible in
// this proc root (the host's processes are, a container's own /proc is
// not). It scans cgroup files and stops at the first match.
func (p ProcSampler) SeesContainers(containerIDs map[string]bool) bool {
	if len(containerIDs) == 0 {
		return true
	}
	ents, err := os.ReadDir(p.root())
	if err != nil {
		return false
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if id := p.containerOf(pid); id != "" && containerIDs[id] {
			return true
		}
	}
	return false
}

// netDev sums the byte counters of every interface except loopback in a
// /proc/<pid>/net/dev file (a container's own network namespace).
func netDev(path string) (rx, tx uint64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	ok = true
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		if strings.TrimSpace(name) == "lo" {
			continue
		}
		fs := strings.Fields(rest)
		if len(fs) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fs[0], 10, 64)
		t, _ := strconv.ParseUint(fs[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx, ok
}
