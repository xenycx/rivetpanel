package metrics

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// CgroupCounters are a container's cumulative counters read from the cgroup
// v2 filesystem. Reading a few small files is far cheaper than a Docker
// statistics call, which is why the 15-second collector prefers it.
type CgroupCounters struct {
	CPUUsec      uint64
	MemBytes     int64 // memory.current minus inactive file cache, like `docker stats`
	MemLimit     int64 // 0 = no limit
	ReadBytes    uint64
	WriteBytes   uint64
	PIDs         int64
	NetRx, NetTx uint64
	HasNet       bool
}

// CgroupReader reads containers' cgroup v2 files below Root (the host's
// /sys/fs/cgroup, mounted read-only at RIVET_HOST_CGROUP in the container
// deployment).
type CgroupReader struct {
	Root string // default /sys/fs/cgroup

	mu   sync.Mutex
	dirs map[string]string // container ID -> cgroup directory
}

func (c *CgroupReader) root() string {
	if c.Root == "" {
		return "/sys/fs/cgroup"
	}
	return c.Root
}

// Available reports whether Root is a cgroup v2 hierarchy.
func (c *CgroupReader) Available() bool {
	_, err := os.Stat(filepath.Join(c.root(), "cgroup.controllers"))
	return err == nil
}

// ErrNoCgroup means the container's cgroup directory was not found (cgroup
// v1, another cgroup driver, or a container on another host).
var ErrNoCgroup = errors.New("container cgroup not found")

func (c *CgroupReader) dir(id string) (string, error) {
	c.mu.Lock()
	d, ok := c.dirs[id]
	c.mu.Unlock()
	if ok {
		if _, err := os.Stat(filepath.Join(d, "cpu.stat")); err == nil {
			return d, nil
		}
	}
	root := c.root()
	for _, cand := range []string{
		filepath.Join(root, "system.slice", "docker-"+id+".scope"), // systemd cgroup driver
		filepath.Join(root, "docker", id),                          // cgroupfs driver
		filepath.Join(root, "docker-"+id+".scope"),
	} {
		if _, err := os.Stat(filepath.Join(cand, "cpu.stat")); err == nil {
			c.mu.Lock()
			if c.dirs == nil {
				c.dirs = map[string]string{}
			}
			c.dirs[id] = cand
			c.mu.Unlock()
			return cand, nil
		}
	}
	return "", ErrNoCgroup
}

// Forget drops cached directories of containers not in keep.
func (c *CgroupReader) Forget(keep map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.dirs {
		if !keep[id] {
			delete(c.dirs, id)
		}
	}
}

// keyValues parses "key value" lines.
func keyValues(path string) map[string]uint64 {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			out[k] = n
		}
	}
	return out
}

func readInt(path string) (int64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(b))
	if s == "max" {
		return 0, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// Read returns a container's counters. The network counters are not part of
// the cgroup; they are read from netDevPath when it is not empty (the
// /proc/<pid>/net/dev of the container's main process).
func (c *CgroupReader) Read(id, netDevPath string) (CgroupCounters, error) {
	d, err := c.dir(id)
	if err != nil {
		return CgroupCounters{}, err
	}
	var out CgroupCounters
	cpu := keyValues(filepath.Join(d, "cpu.stat"))
	if cpu == nil {
		return out, ErrNoCgroup
	}
	out.CPUUsec = cpu["usage_usec"]
	if cur, ok := readInt(filepath.Join(d, "memory.current")); ok {
		ms := keyValues(filepath.Join(d, "memory.stat"))
		if inactive := int64(ms["inactive_file"]); inactive < cur {
			cur -= inactive
		}
		out.MemBytes = max(cur, 0)
	}
	out.MemLimit, _ = readInt(filepath.Join(d, "memory.max"))
	out.PIDs, _ = readInt(filepath.Join(d, "pids.current"))
	out.ReadBytes, out.WriteBytes = ioStat(filepath.Join(d, "io.stat"))
	if netDevPath != "" {
		out.NetRx, out.NetTx, out.HasNet = netDev(netDevPath)
	}
	return out, nil
}

// ioStat sums rbytes and wbytes over devices in an io.stat file
// ("8:0 rbytes=1 wbytes=2 rios=3 wios=4 dbytes=0 dios=0").
func ioStat(path string) (read, write uint64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		for _, kv := range strings.Fields(sc.Text()) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}
			switch k {
			case "rbytes":
				read += n
			case "wbytes":
				write += n
			}
		}
	}
	return read, write
}
