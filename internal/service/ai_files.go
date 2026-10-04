package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/nodetx"
)

// aiFiles is the file access the AI assistant's tools use: a workspace on the
// panel's disk (bots on this panel's node, site drafts) or a bot's workspace
// on its remote node. Both enforce the same path rules (the remote node's
// filesystem layer does it there) and the callers apply the same size limits,
// protected-path checks and redaction.
type aiFiles interface {
	List(p string) ([]filesystem.Entry, error)
	Read(p string, max int64) ([]byte, error)
	// RevisionOrMissing returns "" for a missing file.
	RevisionOrMissing(p string) (string, error)
	// ApplyPatch swaps a revision-checked change set into place; it stays
	// reversible until the returned commit is finished or rolled back.
	ApplyPatch(files []filesystem.PatchFile, maxFile, maxTotal int64) (aiCommit, error)
	Close() error
}

// aiCommit is an applied, not yet permanent AI change set.
type aiCommit interface {
	// Revision is a written file's new revision.
	Revision(p string) (string, error)
	Finish() error
	Rollback() error
}

// errPatchRolledBack means the change set could not be confirmed and the
// previous files are back in place: nothing was applied.
var errPatchRolledBack = errors.New("the server's node did not confirm the change, so it was rolled back")

type localAIFiles struct{ *filesystem.Workspace }

func (l localAIFiles) ApplyPatch(files []filesystem.PatchFile, maxFile, maxTotal int64) (aiCommit, error) {
	c, err := l.Workspace.ApplyPatch(files, maxFile, maxTotal)
	if err != nil {
		return nil, err
	}
	return localAICommit{c, l.Workspace}, nil
}

type localAICommit struct {
	*filesystem.Commit
	w *filesystem.Workspace
}

func (c localAICommit) Revision(p string) (string, error) { return c.w.Revision(p) }

// remoteAIFiles reaches a bot's workspace on its remote node. Nothing is read
// from or written to the panel's disk. Files read for a revision are kept
// for the duration of one tool call so a change is based on exactly the
// bytes whose revision it names.
type remoteAIFiles struct {
	ctx       context.Context
	nf        NodeFiles
	node, bot string
	cache     map[string]remoteAIRead
}

type remoteAIRead struct {
	data []byte
	rev  string
}

func newRemoteAIFiles(ctx context.Context, nf NodeFiles, node, bot string) *remoteAIFiles {
	return &remoteAIFiles{ctx: ctx, nf: nf, node: node, bot: bot, cache: map[string]remoteAIRead{}}
}

func (r *remoteAIFiles) List(p string) ([]filesystem.Entry, error) {
	return r.nf.ListDir(r.ctx, r.node, r.bot, p)
}

func (r *remoteAIFiles) Read(p string, max int64) ([]byte, error) {
	if c, ok := r.cache[p]; ok && int64(len(c.data)) <= max {
		return c.data, nil
	}
	data, _, err := r.nf.ReadFileRevision(r.ctx, r.node, r.bot, p, max)
	return data, err
}

func (r *remoteAIFiles) RevisionOrMissing(p string) (string, error) {
	data, rev, err := r.nf.ReadFileRevision(r.ctx, r.node, r.bot, p, maxAIFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if rev == "" {
		return "", fmt.Errorf("the node did not report a revision for %s", p)
	}
	r.cache[p] = remoteAIRead{data: data, rev: rev}
	return rev, nil
}

func (r *remoteAIFiles) ApplyPatch(files []filesystem.PatchFile, maxFile, maxTotal int64) (aiCommit, error) {
	tx, revs, err := r.nf.BeginPatch(r.ctx, r.node, r.bot, files, maxFile, maxTotal)
	if err != nil {
		return nil, err
	}
	return &remoteAICommit{r: r, tx: tx, revs: revs}, nil
}

func (r *remoteAIFiles) Close() error { return nil }

type remoteAICommit struct {
	r    *remoteAIFiles
	tx   string
	revs map[string]string
	done bool
}

func (c *remoteAICommit) Revision(p string) (string, error) {
	if rev, ok := c.revs[p]; ok && rev != "" {
		return rev, nil
	}
	return "", fmt.Errorf("the node did not report a revision for %s", p)
}

func (c *remoteAICommit) complete(commit bool) error {
	ctx := context.WithoutCancel(c.r.ctx)
	return c.r.nf.CompleteTransaction(ctx, c.r.node, c.r.bot, c.tx, commit)
}

// Finish commits the node's transaction. Completion is idempotent on the
// node, so a lost answer is retried once; if the commit still cannot be
// confirmed, the change is rolled back explicitly (the node would also roll
// it back when the transaction expires) and errPatchRolledBack is returned.
func (c *remoteAICommit) Finish() error {
	if c.done {
		return nil
	}
	c.done = true
	err := c.complete(true)
	if err == nil {
		return nil
	}
	if err = c.complete(true); err == nil {
		return nil
	}
	rerr := c.complete(false)
	switch {
	case rerr == nil:
		return fmt.Errorf("%w: %v", errPatchRolledBack, err)
	case strings.Contains(rerr.Error(), nodetx.ErrConflict.Error()):
		return nil // the node had already committed: only the answer was lost
	}
	return err
}

func (c *remoteAICommit) Rollback() error {
	if c.done {
		return nil
	}
	c.done = true
	return c.complete(false)
}
