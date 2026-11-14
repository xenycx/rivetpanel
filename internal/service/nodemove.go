package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// NodeRoutes reaches the data of a server on a named node, whatever node the
// server is recorded on (noderoute.Router).
type NodeRoutes interface {
	ArchiveAddonData(ctx context.Context, nodeID, botID string, dst io.Writer) error
	ReplaceAddonData(ctx context.Context, nodeID, botID string, src io.Reader) error
	PurgeOn(ctx context.Context, nodeID, botID string) error
}

// LocalAddonData archives and replaces add-on data on the panel's own node
// (addons.OwnedData).
type LocalAddonData interface {
	Archive(botID string, dst io.Writer) (int64, error)
	Replace(botID string, src io.Reader) (int64, error)
}

// NodeMoveService moves an existing bot to another node: its files, its
// add-on (database) data and its configuration go along, its address,
// variables, backups, history and access stay the same.
//
// The bot is stopped for the copy. Until the panel records the new node the
// old node keeps everything and the bot runs there as before; a failure
// removes the partial copy from the target. Only after the switch is the
// old node's copy removed.
type NodeMoveService struct {
	Backups     *BackupService // archive limits, spool directory, stop/resume
	Nodes       NodeRoutes
	LocalAddons LocalAddonData // optional: nil without a local runner
}

// MoveNodeInput selects the target node.
type MoveNodeInput struct {
	NodeID string
}

// MoveNode validates a move and starts it in the background; the returned
// operation reports its progress. Moving needs full control of the bot and
// node management.
func (m *NodeMoveService) MoveNode(ctx context.Context, actor domain.User, botID string, in MoveNodeInput) (string, error) {
	s := m.Backups.Bots
	b, err := s.Authorize(ctx, actor, botID, domain.PermFullAdmin)
	if err != nil {
		return "", err
	}
	if err := requirePerm(actor, domain.PermNodesManage); err != nil {
		return "", domain.Invalid("moving an app to another node needs the node management permission")
	}
	switch {
	case b.Kind == domain.KindGame:
		return "", domain.Invalid("game servers keep their port allocations on their node and cannot be moved yet; back up the world and create the server on the other node")
	case b.Kind == domain.KindWeb || b.Kind == domain.KindContainer:
		return "", domain.Invalid("sites and container apps always run on this panel's own host")
	}
	if in.NodeID == "" || in.NodeID == b.NodeID {
		return "", domain.Invalid("choose a different node")
	}
	node, err := s.placeable(ctx, in.NodeID)
	if err != nil {
		return "", err
	}
	if !s.NodeOnline(node.ID) {
		return "", domain.Invalid(fmt.Sprintf("node %q is offline; the move needs its agent connected", node.Name))
	}
	from, err := s.Store.GetNode(ctx, b.NodeID)
	if err != nil {
		return "", err
	}
	if !s.NodeOnline(b.NodeID) {
		return "", domain.Invalid(fmt.Sprintf("node %q, where the files are, is offline; the move needs its agent connected", from.Name))
	}
	if s.Notifier == nil || m.Nodes == nil {
		return "", domain.ErrRunnerUnavailable
	}
	if rt, ok := s.Catalog.Get(b.Runtime); ok {
		if err := s.LimitsFor(ctx, node.ID).validateResources(rt, b.MemoryBytes, b.NanoCPUs, b.PidsLimit); err != nil {
			return "", domain.Invalid(fmt.Sprintf("%s cannot run this app as configured: %s; lower its limits first", node.Name, err.Error()))
		}
	}
	if len(b.Addons) > 0 {
		if err := s.addonsAvailable(node.ID); err != nil {
			return "", err
		}
		if !s.remote(b.NodeID) || !s.remote(node.ID) {
			if m.LocalAddons == nil {
				return "", domain.Invalid("database add-ons cannot be copied on this panel (no Docker runner)")
			}
		}
		for _, a := range b.Addons {
			if k, ok := addons.Get(a.Kind); ok && s.remote(node.ID) {
				if err := addonSupportedOnNode(k); err != nil {
					return "", err
				}
			}
		}
	}
	if err := diskPreflight(m.Backups.Files, m.Backups.MinFreeDisk); err != nil {
		return "", err
	}
	base := m.Backups.baseCtx
	if base == nil {
		base = context.Background()
	}
	claim, cctx, err := s.Coord.Claim(base, botID, "A move to another node", true)
	if err != nil {
		return "", err
	}
	label := from.Name + " → " + node.Name
	op := m.Backups.Ops.Begin(ctx, OpStart{BotID: botID, Kind: domain.OpMove, Trigger: "manual", ActorID: &actor.ID, SourceRef: &node.ID, SourceLabel: &label})
	m.Backups.wg.Add(1)
	go func() {
		defer m.Backups.wg.Done()
		defer claim.Release()
		m.run(cctx, b, from, node, op)
	}()
	return op, nil
}

// run performs a validated move. It owns the bot's exclusive claim.
func (m *NodeMoveService) run(ctx context.Context, b domain.Bot, from, to domain.Node, op string) {
	bs, s := m.Backups, m.Backups.Bots
	log := bs.Log.With("bot", b.ID, "from", from.ID, "to", to.ID)
	fail := func(code, msg string, err error) {
		if err != nil {
			log.Warn("move failed", "stage", code, "err", err)
			var ae *filesystem.ErrArchive
			var aa *addons.ErrArchive
			var ve *domain.ValidationError
			switch {
			case errors.As(err, &ae):
				msg += ": " + ae.Msg
			case errors.As(err, &aa):
				msg += ": " + aa.Msg
			case errors.As(err, &ve):
				msg += ": " + ve.Msg
			}
		}
		fctx, cancel := bg(ctx)
		defer cancel()
		bs.Ops.Finish(fctx, op, domain.OpFailed, code, msg, nil)
	}

	wasRunning := b.DesiredState == domain.DesiredRunning
	resume, err := bs.stopForBackup(ctx, b.ID, op)
	if err != nil {
		fail("stop_failed", "The app could not be stopped for the move", err)
		return
	}
	cur, err := bs.store().GetBot(ctx, b.ID)
	if err != nil || cur.NodeID != from.ID {
		resume()
		fail("changed", "The app changed while it was being stopped; nothing was moved", err)
		return
	}

	dir, err := os.MkdirTemp(bs.Dir, ".move-"+b.ID+"-")
	if err != nil {
		resume()
		fail("spool_failed", "The panel could not prepare the copy", err)
		return
	}
	defer os.RemoveAll(dir)

	// 1. Copy everything off the old node. Nothing on either node changes.
	bs.Ops.Stage(ctx, op, "Copying files from "+from.Name)
	files, err := m.spool(dir, "files.tar.gz", func(w io.Writer) error { return m.archiveFiles(ctx, from.ID, b.ID, w) })
	if err != nil {
		resume()
		fail("copy_failed", "The files could not be copied from "+from.Name, err)
		return
	}
	defer files.Close()
	var data *os.File
	if len(cur.Addons) > 0 {
		bs.Ops.Stage(ctx, op, "Copying database data from "+from.Name)
		data, err = m.spool(dir, "addons.tar.gz", func(w io.Writer) error { return m.archiveAddons(ctx, from.ID, b.ID, w) })
		if err != nil {
			resume()
			fail("copy_failed", "The database data could not be copied from "+from.Name, err)
			return
		}
		defer data.Close()
	}

	// 2. Write the copy on the new node. A failure removes it again.
	undo := func() {
		fctx, cancel := bg(ctx)
		defer cancel()
		if err := m.Nodes.PurgeOn(fctx, to.ID, b.ID); err != nil {
			log.Warn("remove partial copy", "err", err)
		}
	}
	bs.Ops.Stage(ctx, op, "Writing files on "+to.Name)
	if err := m.restoreFiles(ctx, to.ID, b.ID, files); err != nil {
		undo()
		resume()
		fail("write_failed", "The files could not be written on "+to.Name, err)
		return
	}
	if data != nil {
		bs.Ops.Stage(ctx, op, "Writing database data on "+to.Name)
		if err := m.restoreAddons(ctx, to.ID, b.ID, data); err != nil {
			undo()
			resume()
			fail("write_failed", "The database data could not be written on "+to.Name, err)
			return
		}
	}

	// 3. Switch. Only a still stopped, unchanged bot on the old node moves.
	bs.Ops.Stage(ctx, op, "Switching to "+to.Name)
	if err := bs.store().MoveBotNode(ctx, b.ID, from.ID, to.ID, cur.Generation, bs.now().UnixMilli()); err != nil {
		undo()
		resume()
		fail("changed", "The app changed during the move; it stays on "+from.Name, err)
		return
	}
	moved, err := bs.store().GetBot(ctx, b.ID)
	if err == nil {
		s.Bus.Publish(events.Status{BotID: b.ID, DesiredState: moved.DesiredState, ObservedState: moved.ObservedState,
			Generation: moved.Generation, ObservedGeneration: moved.ObservedGeneration})
	}
	s.Notifier.Notify(b.ID)

	// 4. Remove the old copy. The move already happened; a failure here only
	// leaves files behind, and an offline agent drops the containers when it
	// reconnects.
	bs.Ops.Stage(ctx, op, "Removing the old copy on "+from.Name)
	note := ""
	{
		fctx, cancel := bg(ctx)
		if err := m.Nodes.PurgeOn(fctx, from.ID, b.ID); err != nil {
			log.Warn("remove old copy", "err", err)
			note = " The old copy on " + from.Name + " could not be removed; delete its folder there by hand."
		}
		cancel()
	}

	// 5. Start it again where it now lives, if it was running.
	if wasRunning && err == nil {
		bs.Ops.Stage(ctx, op, "Starting on "+to.Name)
		fctx, cancel := bg(ctx)
		if nb, changed, err := bs.store().SetDesired(fctx, b.ID, domain.DesiredRunning, true, bs.now().UnixMilli()); err != nil {
			note += " It could not be started again: start it from its page."
		} else if changed {
			s.Bus.Publish(events.Status{BotID: b.ID, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
				Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
			s.Notifier.Notify(b.ID)
		}
		cancel()
	}
	fctx, cancel := bg(ctx)
	defer cancel()
	bs.Ops.Finish(fctx, op, domain.OpSucceeded, "", "Moved to "+to.Name+"."+note,
		map[string]any{"from_node": from.ID, "to_node": to.ID, "files_bytes": sizeOfFile(files)})
}

// spool writes an archive to a file in dir and returns it rewound.
func (m *NodeMoveService) spool(dir, name string, write func(io.Writer) error) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := write(f); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func sizeOfFile(f *os.File) int64 {
	if st, err := f.Stat(); err == nil {
		return st.Size()
	}
	return 0
}

func (m *NodeMoveService) archiveFiles(ctx context.Context, nodeID, botID string, w io.Writer) error {
	bs := m.Backups
	bs.init()
	if bs.Bots.remote(nodeID) {
		return bs.Remote.Backup(ctx, nodeID, botID, nil, bs.Limits, w)
	}
	ws, err := bs.Files.Open(botID)
	if err != nil {
		return err
	}
	defer ws.Close()
	_, _, err = ws.WriteTarGz(w, nil, bs.Limits)
	return err
}

func (m *NodeMoveService) restoreFiles(ctx context.Context, nodeID, botID string, src *os.File) error {
	bs := m.Backups
	if bs.Bots.remote(nodeID) {
		if err := bs.Bots.NodeFiles.CreateWorkspace(ctx, nodeID, botID); err != nil {
			return err
		}
		tx, err := bs.Remote.BeginRestore(ctx, nodeID, botID, src, bs.Limits)
		if err != nil {
			return err
		}
		return bs.Remote.CompleteRestore(ctx, nodeID, botID, tx, true)
	}
	if err := bs.Files.Create(botID); err != nil {
		if _, perr := bs.Files.Path(botID); perr != nil {
			return err
		}
	}
	ws, err := bs.Files.Open(botID)
	if err != nil {
		return err
	}
	defer ws.Close()
	_, commit, err := ws.RestoreTarGzCommit(src, bs.Limits)
	if err != nil {
		return err
	}
	return commit.Finish()
}

func (m *NodeMoveService) archiveAddons(ctx context.Context, nodeID, botID string, w io.Writer) error {
	if m.Backups.Bots.remote(nodeID) {
		return m.Nodes.ArchiveAddonData(ctx, nodeID, botID, w)
	}
	_, err := m.LocalAddons.Archive(botID, w)
	return err
}

func (m *NodeMoveService) restoreAddons(ctx context.Context, nodeID, botID string, src *os.File) error {
	if m.Backups.Bots.remote(nodeID) {
		return m.Nodes.ReplaceAddonData(ctx, nodeID, botID, src)
	}
	_, err := m.LocalAddons.Replace(botID, src)
	return err
}
