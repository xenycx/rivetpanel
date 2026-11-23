package metrics

import (
	"context"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// RemoteStreamInterval is how often a remote node's live stream polls its
// agent.
const RemoteStreamInterval = 5 * time.Second

// ErrNoStream means the node has no live source (no fast sampler for the
// panel's node, or no agent connection for a remote one).
var ErrNoStream = domain.Invalid("live samples are not available for this node")

// Stream returns live host points for a node until stop is called or ctx
// ends: the shared fast sampler for the panel's node (every 2 seconds, no
// extra /proc reads per viewer), the node's agent every 5 seconds for a
// remote node. Nothing is stored.
func (m *Metrics) Stream(ctx context.Context, node string) (<-chan HostPoint, func(), error) {
	if _, err := m.Store.GetNode(ctx, node); err != nil {
		return nil, nil, err
	}
	if node == m.LocalNode {
		if m.Fast == nil {
			return nil, nil, ErrNoStream
		}
		ch, stop := m.Fast.Subscribe()
		return ch, stop, nil
	}
	if m.Remote == nil || m.Remote.Host == nil {
		return nil, nil, ErrNoStream
	}
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan HostPoint, 4)
	go func() {
		defer close(ch)
		t := time.NewTicker(RemoteStreamInterval)
		defer t.Stop()
		for {
			rctx, rcancel := context.WithTimeout(ctx, RemoteStreamInterval)
			p, err := m.Remote.Host(rctx, node)
			rcancel()
			if err == nil {
				if p.T == 0 {
					p.T = m.now().UnixMilli()
				}
				select {
				case ch <- p:
				default:
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return ch, cancel, nil
}
