package noderoute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"syscall"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// Single-file access for features that edit a remote server's workspace on
// the panel's behalf (the package manager, the AI assistant). Every call uses
// the agent's existing file routes; path containment is enforced by the
// node's filesystem layer, and the panel bounds every size it reads or
// sends. Callers authorize the user first.

func filesPath(botID, rest string) string {
	return "/node/v1/bots/" + url.PathEscape(botID) + rest
}

// fileError maps the agent's file answers onto the errors local workspaces
// return, so callers handle both the same way.
// errAgentConflict is a delete the node refused with 409.
var errAgentConflict = errors.New("agent delete: conflict")

func fileError(what string, resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("agent %s: %w", what, fs.ErrNotExist)
	case http.StatusRequestEntityTooLarge:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return filesystem.ErrTooLarge
	case http.StatusConflict:
		if what == "delete" {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			return errAgentConflict
		}
	case http.StatusPreconditionFailed:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("agent %s: the file changed: %w", what, domain.ErrConflict)
	}
	return agentError(what, resp)
}

// ListDir lists one directory of a remote workspace.
func (r *Router) ListDir(ctx context.Context, nodeID, botID, dir string) ([]filesystem.Entry, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return nil, ErrNoRunner
	}
	q := url.Values{}
	q.Set("path", dir)
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodGet, filesPath(botID, "/files?"+q.Encode()), nil, "")
	if err != nil {
		return nil, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fileError("file list", resp)
	}
	var out struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out); err != nil {
		return nil, err
	}
	es := make([]filesystem.Entry, 0, len(out.Entries))
	for _, e := range out.Entries {
		if e.Name == "" || strings.ContainsAny(e.Name, "/\x00") || e.Name == "." || e.Name == ".." {
			continue // a well-behaved node never lists these
		}
		es = append(es, filesystem.Entry{Name: e.Name, IsDir: e.Type == "dir", Symlink: e.Type == "symlink", Size: e.Size})
	}
	return es, nil
}

// ReadFileRevision reads one remote file (at most max bytes) and returns it
// with its revision, the same value Workspace.Revision reports on the node.
// A missing file is fs.ErrNotExist.
func (r *Router) ReadFileRevision(ctx context.Context, nodeID, botID, p string, max int64) ([]byte, string, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return nil, "", ErrNoRunner
	}
	q := url.Values{}
	q.Set("path", p)
	q.Set("download", "1")
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodGet, filesPath(botID, "/files/content?"+q.Encode()), nil, "")
	if err != nil {
		return nil, "", offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fileError("file read", resp)
	}
	if resp.ContentLength > max {
		return nil, "", filesystem.ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > max {
		return nil, "", filesystem.ErrTooLarge
	}
	return data, strings.Trim(resp.Header.Get("ETag"), `" `), nil
}

// WriteFile atomically replaces one remote file. ifMatch, when set, is the
// revision the caller read; createOnly refuses an existing file. A failed
// precondition is domain.ErrConflict and nothing is written. It returns the
// new revision.
func (r *Router) WriteFile(ctx context.Context, nodeID, botID, p string, data []byte, ifMatch string, createOnly bool) (string, error) {
	return r.WriteFileFrom(ctx, nodeID, botID, p, bytes.NewReader(data), int64(len(data)), ifMatch, createOnly)
}

// diskCheckMinFile is the smallest single-file write that runs the free-disk
// preflight first; smaller edits skip the extra round trip (the node's write
// still fails cleanly on a full disk).
const diskCheckMinFile = 1 << 20

// WriteFileFrom is WriteFile for a body of exactly size bytes streamed from
// src (an installed mod, for example), so large files are never held in
// memory. The request carries its length; the node writes to a temporary
// file and only renames it into place once the whole body has arrived.
func (r *Router) WriteFileFrom(ctx context.Context, nodeID, botID, p string, src io.Reader, size int64, ifMatch string, createOnly bool) (string, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return "", ErrNoRunner
	}
	if size < 0 {
		return "", fmt.Errorf("invalid file size")
	}
	if size >= diskCheckMinFile {
		if err := r.EnsureDiskFree(ctx, nodeID, size); err != nil {
			return "", err
		}
	}
	q := url.Values{}
	q.Set("path", p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, filesPath(botID, "/files/content?"+q.Encode()), io.LimitReader(src, size))
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if ifMatch != "" {
		req.Header.Set("If-Match", `"`+ifMatch+`"`)
	}
	if createOnly {
		req.Header.Set("If-None-Match", "*")
	}
	resp, err := r.Hub.Forward(nodeID, req)
	if err != nil {
		return "", offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return "", fileError("file write", resp)
	}
	return strings.Trim(resp.Header.Get("ETag"), `" `), nil
}

// BeginPatch sends an AI change set to the node, which checks every file's
// revision, then stages and swaps it through its journal. The change stays
// reversible until CompleteTransaction; an unconfirmed patch expires to
// rollback on the node. A stale revision is filesystem.ErrPatchConflict.
func (r *Router) BeginPatch(ctx context.Context, nodeID, botID string, files []filesystem.PatchFile, maxFile, maxTotal int64) (string, map[string]string, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return "", nil, ErrNoRunner
	}
	in := agentproto.PatchRequest{MaxFile: maxFile, MaxTotal: maxTotal, Files: make([]agentproto.PatchFile, len(files))}
	var need int64
	for i, f := range files {
		in.Files[i] = agentproto.PatchFile{Path: f.Path, BeforeRevision: f.BeforeRevision, Content: f.After, Delete: f.After == nil, Mode: uint32(f.Mode.Perm())}
		need += int64(len(f.After))
	}
	if err := r.EnsureDiskFree(ctx, nodeID, need); err != nil {
		return "", nil, err
	}
	b, err := json.Marshal(in)
	if err != nil {
		return "", nil, err
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost, filesPath(botID, "/patch"), bytes.NewReader(b), "application/json")
	if err != nil {
		return "", nil, offline(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
	case http.StatusConflict:
		// Either a stale snapshot or another pending replacement (busy).
		e := agentError("patch", resp)
		if msg, ok := strings.CutPrefix(e.Error(), filesystem.ErrPatchConflict.Error()+": "); ok {
			return "", nil, fmt.Errorf("%w: %s", filesystem.ErrPatchConflict, msg)
		}
		return "", nil, e
	default:
		return "", nil, fileError("patch", resp)
	}
	var out agentproto.PatchStarted
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", nil, err
	}
	if out.Transaction == "" {
		return "", nil, fmt.Errorf("agent returned no transaction")
	}
	return out.Transaction, out.Revisions, nil
}

// CreateWorkspace makes a new server's directory on the node through the
// agent's existing workspace route. It is idempotent: an existing directory
// is fine. The node's filesystem layer validates the server id.
func (r *Router) CreateWorkspace(ctx context.Context, nodeID, botID string) error {
	if !r.Remote(nodeID) || r.Hub == nil {
		return ErrNoRunner
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodPost, "/node/v1/workspaces/"+url.PathEscape(botID), nil, "")
	if err != nil {
		return offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return agentError("workspace", resp)
	}
	return nil
}

// ReadFileTo streams one remote file (at most max bytes) into dst and returns
// its revision and length, for transfers too large to hold in memory (SFTP
// downloads). A missing file is fs.ErrNotExist; a larger file is
// filesystem.ErrTooLarge, possibly after part of it was written to dst.
func (r *Router) ReadFileTo(ctx context.Context, nodeID, botID, p string, max int64, dst io.Writer) (string, int64, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return "", 0, ErrNoRunner
	}
	q := url.Values{}
	q.Set("path", p)
	q.Set("download", "1")
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodGet, filesPath(botID, "/files/content?"+q.Encode()), nil, "")
	if err != nil {
		return "", 0, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fileError("file read", resp)
	}
	if resp.ContentLength > max {
		return "", 0, filesystem.ErrTooLarge
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return "", n, err
	}
	if n > max {
		return "", n, filesystem.ErrTooLarge
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return "", n, io.ErrUnexpectedEOF
	}
	return strings.Trim(resp.Header.Get("ETag"), `" `), n, nil
}

// fileCommand sends one JSON file operation (mkdir, move) or a delete over
// the agent's existing file routes and expects 204.
func (r *Router) fileCommand(ctx context.Context, nodeID, botID, method, rest, what string, body any) error {
	if !r.Remote(nodeID) || r.Hub == nil {
		return ErrNoRunner
	}
	var src io.Reader
	ctype := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		src, ctype = bytes.NewReader(b), "application/json"
	}
	resp, err := r.Hub.Do(ctx, nodeID, method, filesPath(botID, rest), src, ctype)
	if err != nil {
		return offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fileError(what, resp)
	}
	return nil
}

// MakeDir creates a directory (and any missing parents) in a remote
// workspace through the agent's mkdir route.
func (r *Router) MakeDir(ctx context.Context, nodeID, botID, p string) error {
	return r.fileCommand(ctx, nodeID, botID, http.MethodPost, "/files/mkdir", "mkdir", map[string]string{"path": p})
}

// Move renames a file or directory inside one remote workspace (missing
// parents of the destination are created, an existing file is replaced).
func (r *Router) Move(ctx context.Context, nodeID, botID, from, to string) error {
	return r.fileCommand(ctx, nodeID, botID, http.MethodPost, "/files/move", "move", map[string]string{"from": from, "to": to})
}

// RemovePath deletes a file or a whole directory tree in a remote workspace.
func (r *Router) RemovePath(ctx context.Context, nodeID, botID, p string) error {
	q := url.Values{}
	q.Set("path", p)
	return r.fileCommand(ctx, nodeID, botID, http.MethodDelete, "/files?"+q.Encode(), "delete", nil)
}

// ErrDirNotEmpty is RemoveOne's answer for a directory that still has
// entries; it wraps syscall.ENOTEMPTY like a local rmdir.
var ErrDirNotEmpty = fmt.Errorf("directory is not empty: %w", syscall.ENOTEMPTY)

// RemoveOne deletes a file, a symlink or an empty directory in a remote
// workspace (protocol 7, recursive=false). The node's filesystem checks
// emptiness atomically (rmdir), so entries added concurrently are never
// deleted; a non-empty directory is ErrDirNotEmpty.
func (r *Router) RemoveOne(ctx context.Context, nodeID, botID, p string) error {
	q := url.Values{}
	q.Set("path", p)
	q.Set("recursive", "false")
	err := r.fileCommand(ctx, nodeID, botID, http.MethodDelete, "/files?"+q.Encode(), "delete", nil)
	if errors.Is(err, errAgentConflict) {
		return ErrDirNotEmpty
	}
	return err
}

// AddonStates reports the add-on containers of a server on a remote node,
// keyed by add-on kind.
func (r *Router) AddonStates(ctx context.Context, nodeID, botID string) (map[string]agentproto.AddonStatus, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return nil, ErrNoRunner
	}
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodGet, filesPath(botID, "/addons"), nil, "")
	if err != nil {
		return nil, offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, agentError("add-on states", resp)
	}
	var out agentproto.AddonStates
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Addons, nil
}

// AddonLogs returns the last lines (at most agentproto.MaxAddonLogLines) one
// add-on of a server on a remote node wrote.
func (r *Router) AddonLogs(ctx context.Context, nodeID, botID, kind string, lines int) (string, error) {
	if !r.Remote(nodeID) || r.Hub == nil {
		return "", ErrNoRunner
	}
	lines = min(max(lines, 1), agentproto.MaxAddonLogLines)
	q := url.Values{}
	q.Set("lines", fmt.Sprint(lines))
	resp, err := r.Hub.Do(ctx, nodeID, http.MethodGet, filesPath(botID, "/addons/"+url.PathEscape(kind)+"/logs?"+q.Encode()), nil, "")
	if err != nil {
		return "", offline(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", agentError("add-on logs", resp)
	}
	var out agentproto.AddonLogs
	// JSON escaping can grow the text; allow for it, then bound the result.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 6*agentproto.MaxAddonLogBytes+4096)).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Output) > agentproto.MaxAddonLogBytes {
		out.Output = out.Output[len(out.Output)-agentproto.MaxAddonLogBytes:]
	}
	return out.Output, nil
}

// RemoveAddonData deletes one add-on's data directory on a remote node
// (protocol 7).
func (r *Router) RemoveAddonData(ctx context.Context, nodeID, botID, kind string) error {
	return r.fileCommand(ctx, nodeID, botID, http.MethodDelete, "/addons/"+url.PathEscape(kind)+"/data", "add-on data", nil)
}
