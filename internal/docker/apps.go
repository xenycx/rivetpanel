package docker

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"

	"github.com/xenycx/rivetpanel/internal/runner"
)

// EnsureAppsNetwork creates the shared bridge network site apps run on
// unless it exists. Unlike add-on networks it is routable: apps reach the
// internet, and the panel (on the host, or joined to it) reaches the apps.
func (a *Adapter) EnsureAppsNetwork(ctx context.Context, name string) error {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	if n, err := a.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		if n.Internal {
			return fmt.Errorf("network %s is internal; the sites listener could not reach apps on it", name)
		}
		return nil
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	_, err := a.cli.NetworkCreate(ctx, name, network.CreateOptions{Driver: "bridge",
		Labels: map[string]string{runner.LabelManaged: "true", runner.LabelNetwork: "apps"}})
	if errdefs.IsConflict(err) {
		return nil
	}
	return err
}

var _ runner.AppsNetworker = (*Adapter)(nil)

// JoinSelf connects the panel's own container to a network, so the sites
// listener can reach apps on it. Outside a container it does nothing (the
// host routes to bridge networks); ErrSelfUnknown means the daemon does not
// know this container.
func (a *Adapter) JoinSelf(ctx context.Context, name string) error {
	if !InContainer() {
		return nil
	}
	for _, id := range selfCandidates() {
		err := a.ConnectNetwork(ctx, name, id)
		if err == nil {
			return nil
		}
		if !errdefs.IsNotFound(err) {
			return err
		}
	}
	return ErrSelfUnknown
}

// ErrNoAddress means a container has no address on the network (stopped, or
// not attached).
var ErrNoAddress = errors.New("the container has no address on that network")

// ContainerIP returns a container's IPv4 address on a network.
func (a *Adapter) ContainerIP(ctx context.Context, id, name string) (string, error) {
	ctx, cancel := within(ctx, opTimeout)
	defer cancel()
	r, err := a.cli.ContainerInspect(ctx, id)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "", runner.ErrNoContainer
		}
		return "", err
	}
	if r.NetworkSettings == nil {
		return "", ErrNoAddress
	}
	if ep := r.NetworkSettings.Networks[name]; ep != nil && ep.IPAddress != "" {
		return ep.IPAddress, nil
	}
	return "", ErrNoAddress
}
