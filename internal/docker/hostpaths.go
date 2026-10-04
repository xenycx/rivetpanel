package docker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/errdefs"

	"github.com/xenycx/rivetpanel/internal/runner"
)

// PathMapping says that Container (a path as the panel process sees it) is
// Host on the machine that runs the Docker daemon.
type PathMapping struct {
	Container string
	Host      string
}

// PathMap translates bind-mount sources from the panel's view of the file
// system to the Docker host's. It is empty when the panel runs directly on the
// host, where both views are the same.
type PathMap []PathMapping

// Translate returns the Docker-host path for p. The longest matching
// container prefix wins; a path outside every mapping is returned unchanged.
func (m PathMap) Translate(p string) string {
	best := -1
	for i, e := range m {
		if under(p, e.Container) && (best < 0 || len(e.Container) > len(m[best].Container)) {
			best = i
		}
	}
	if best < 0 {
		return p
	}
	rel, _ := filepath.Rel(m[best].Container, p)
	return filepath.Join(m[best].Host, rel)
}

// Covers reports whether p lies on one of the mappings (a volume or bind
// mount the Docker daemon can reach).
func (m PathMap) Covers(p string) bool {
	for _, e := range m {
		if under(p, e.Container) {
			return true
		}
	}
	return false
}

// Identity reports whether every mapping keeps the path unchanged (the data
// directory is bind-mounted at the same absolute path inside and outside).
func (m PathMap) Identity() bool {
	for _, e := range m {
		if e.Container != e.Host {
			return false
		}
	}
	return true
}

func under(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}

// SetPathMap makes Create translate bind-mount sources (bot workspaces,
// add-on data, diagnostic scratch directories) with m.
func (a *Adapter) SetPathMap(m PathMap) { a.paths = m }

// translateSpec returns spec with host paths as the Docker daemon sees them.
func (a *Adapter) translateSpec(spec runner.ContainerSpec) runner.ContainerSpec {
	if len(a.paths) == 0 {
		return spec
	}
	if spec.WorkspaceHostPath != "" {
		spec.WorkspaceHostPath = a.paths.Translate(spec.WorkspaceHostPath)
	}
	if len(spec.Mounts) > 0 {
		ms := make([]runner.Mount, len(spec.Mounts))
		for i, m := range spec.Mounts {
			m.Source = a.paths.Translate(m.Source)
			ms[i] = m
		}
		spec.Mounts = ms
	}
	return spec
}

// InContainer reports whether this process runs inside a Docker or Podman
// container.
func InContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// ErrSelfUnknown is returned by DetectHostPaths when the panel runs in a
// container that the Docker daemon cannot identify (another daemon, a custom
// host name without the usual mount information).
var ErrSelfUnknown = errors.New("could not identify the panel's own container through the Docker daemon")

var containerIDRe = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// selfCandidates lists names the panel's own container may be inspected by:
// the full ID from /proc/self/mountinfo (Docker mounts /etc/hostname from
// /var/lib/docker/containers/<id>/), then the host name, which defaults to
// the short container ID.
func selfCandidates() []string {
	var out []string
	if f, err := os.Open("/proc/self/mountinfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if m := containerIDRe.FindStringSubmatch(sc.Text()); m != nil {
				out = append(out, m[1])
				break
			}
		}
		f.Close()
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		out = append(out, h)
	}
	return out
}

// DetectHostPaths finds how the panel's container mounts relate to the
// Docker host when the panel itself runs in a container, and checks that
// every path in need (the bot data root, the add-on and scratch directories)
// is on a host-directory bind mount: the daemon resolves bind sources on the
// host, so a directory inside the panel container's own file system cannot be
// mounted into bot containers, and Docker refuses private bind mounts from a
// named volume (inside its data root). Outside a container it returns nil, nil.
func (a *Adapter) DetectHostPaths(ctx context.Context, need ...string) (PathMap, error) {
	if !InContainer() {
		return nil, nil
	}
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	var info container.InspectResponse
	found := false
	for _, id := range selfCandidates() {
		r, err := a.cli.ContainerInspect(ctx, id)
		if err == nil {
			info, found = r, true
			break
		}
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("inspect the panel's own container through %s: %w", a.cli.DaemonHost(), err)
		}
	}
	if !found {
		return nil, ErrSelfUnknown
	}
	var m PathMap
	for _, mp := range info.Mounts {
		if mp.Destination == "" || mp.Source == "" {
			continue
		}
		if mp.Type != mount.TypeBind && mp.Type != mount.TypeVolume {
			continue
		}
		m = append(m, PathMapping{Container: filepath.Clean(mp.Destination), Host: filepath.Clean(mp.Source)})
	}
	sort.Slice(m, func(i, j int) bool { return m[i].Container < m[j].Container })
	var missing []string
	for _, p := range need {
		abs, err := filepath.Abs(p)
		if err != nil {
			return m, err
		}
		if !m.Covers(abs) {
			missing = append(missing, abs)
		}
	}
	if len(missing) > 0 {
		return m, fmt.Errorf("%s %s inside the panel container's own file system, so Docker cannot bind-mount it into bot containers; "+
			"bind-mount a host directory there (the published compose.yaml uses /var/lib/rivetpanel:/var/lib/rivetpanel)",
			strings.Join(missing, ", "), map[bool]string{true: "is", false: "are"}[len(missing) == 1])
	}
	// Docker refuses private bind mounts from inside its own data directory,
	// which is where named volumes live; bot mounts stay private, so the
	// data must be a host directory.
	if di, err := a.cli.Info(ctx); err == nil && di.DockerRootDir != "" {
		for _, p := range need {
			abs, _ := filepath.Abs(p)
			if h := m.Translate(abs); under(h, di.DockerRootDir) {
				return m, fmt.Errorf("%s is stored in a Docker volume (%s on the host); Docker does not bind-mount from its own data directory %s "+
					"into bot containers. Bind-mount a host directory instead, for example /var/lib/rivetpanel:/var/lib/rivetpanel",
					abs, h, di.DockerRootDir)
			}
		}
	}
	return m, nil
}

// CountLabeled returns how many running containers carry the label key
// (any value). It is used to notice containers left by another product.
func (a *Adapter) CountLabeled(ctx context.Context, key string) (int, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	list, err := a.cli.ContainerList(ctx, container.ListOptions{Filters: filters.NewArgs(filters.Arg("label", key))})
	if err != nil {
		return 0, err
	}
	return len(list), nil
}
