package agentnode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// publishingDocker is a Docker whose only answer is the ports other
// containers publish on the node.
type publishingDocker struct{ pubs []runner.PublishedPort }

func (publishingDocker) Inspect(context.Context, string) (runner.ContainerInfo, error) {
	return runner.ContainerInfo{}, errors.New("not used")
}
func (publishingDocker) Logs(context.Context, string, time.Time, int) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}
func (publishingDocker) AttachStdin(context.Context, string) (io.WriteCloser, error) {
	return nil, errors.New("not used")
}
func (publishingDocker) StreamStats(context.Context, string, func(domain.ResourceSample) bool) error {
	return errors.New("not used")
}
func (d publishingDocker) PublishedPorts(context.Context) ([]runner.PublishedPort, error) {
	return d.pubs, nil
}

// The node's port probe also reports ports that running containers publish
// (any panel or tool), which a bind test misses without Docker's userland
// proxy. The answer names the container; the wire format is unchanged.
func TestPortProbeReportsDockerPublishedPorts(t *testing.T) {
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	app := App(Deps{Files: files, ProbePort: func(string, int) error { return nil },
		Docker: publishingDocker{pubs: []runner.PublishedPort{{HostIP: "0.0.0.0", HostPort: 25565, Proto: "tcp", Container: "other-panel-runtime"}}}})
	in, _ := json.Marshal(agentproto.PortProbeRequest{IP: "0.0.0.0", Ports: []int{25565, 25566}})
	resp := nodeRequest(t, app, http.MethodPost, "/node/v1/ports/probe", in, "application/json")
	defer resp.Body.Close()
	var out agentproto.PortProbe
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("probe: %d %v", resp.StatusCode, err)
	}
	if len(out.Ports) != 2 || out.Ports[0].Free || out.Ports[0].Reason != "already used by container other-panel-runtime" || !out.Ports[1].Free {
		t.Fatalf("probe = %+v", out.Ports)
	}
}
