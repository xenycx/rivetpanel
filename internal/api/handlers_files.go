package api

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

const (
	maxEditBytes     = 1 << 20 // the editor refuses larger files; use download
	maxPathLen       = 4096
	defaultMaxUpload = 32 << 20
)

// mapFSError translates filesystem failures to client-facing errors without
// leaking host paths.
func mapFSError(err error) error {
	var ae *filesystem.ErrArchive
	switch {
	case err == nil:
		return nil
	case errors.Is(err, filesystem.ErrTooLarge):
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "file is too large")
	case errors.Is(err, filesystem.ErrInvalidPath):
		return domain.Invalid("invalid path")
	case errors.As(err, &ae):
		return domain.Invalid(ae.Msg)
	case errors.Is(err, fs.ErrNotExist):
		return domain.ErrNotFound
	case errors.Is(err, fs.ErrExist), errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.ENOTDIR),
		errors.Is(err, syscall.EISDIR), errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.EXDEV),
		strings.Contains(err.Error(), "path escapes"):
		return domain.Invalid("operation is not possible on this path")
	}
	return err
}

// filesFor authorizes the actor for the bot and opens its workspace. Changes
// are refused (409) while a deployment or restore replaces the files.
func (s *panel) filesFor(c fiber.Ctx) (*filesystem.Workspace, error) {
	if s.trustedFiles {
		// rivet-agent: the panel authorized this request before forwarding it.
		// A new server's directory is created on first use.
		id := strings.Clone(c.Params("id"))
		if _, err := s.files.Path(id); err != nil {
			_ = s.files.Create(id)
		}
		w, err := s.files.Open(id)
		if err != nil {
			return nil, mapFSError(err)
		}
		return w, nil
	}
	if siteID := strings.Clone(c.Params("sid")); siteID != "" && s.sites != nil {
		w, err := s.sites.Draft(c.Context(), currentUser(c), siteID)
		if err != nil {
			return nil, err
		}
		return w, nil
	}
	if s.files == nil {
		return nil, fiber.ErrNotFound
	}
	b, err := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermEditFiles)
	if err != nil {
		return nil, err
	}
	if !isSafeMethod(c.Method()) {
		if err := s.bots.FilesBlocked(b.ID); err != nil {
			return nil, err
		}
	}
	w, err := s.files.Open(b.ID)
	if err != nil {
		return nil, mapFSError(err)
	}
	return w, nil
}

func (s *panel) publishSiteFiles(c fiber.Ctx) error {
	r, err := s.sites.PublishDraft(c.Context(), currentUser(c), strings.Clone(c.Params("sid")))
	if err != nil {
		return mapFSError(err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"release": r.ID, "files": r.Files, "bytes": r.Bytes})
}

func queryPath(c fiber.Ctx, def string) (string, error) {
	p := strings.Clone(c.Query("path", def))
	if p == "" || len(p) > maxPathLen {
		return "", domain.Invalid("path is required")
	}
	return p, nil
}

type entryDTO struct {
	Name string `json:"name"`
	Type string `json:"type"` // file | dir | symlink
	Size int64  `json:"size"`
}

func (s *panel) listFiles(c fiber.Ctx) error {
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	p, err := queryPath(c, ".")
	if err != nil {
		return err
	}
	es, err := w.List(p)
	if err != nil {
		return mapFSError(err)
	}
	out := make([]entryDTO, 0, len(es))
	for _, e := range es {
		t := "file"
		switch {
		case e.Symlink:
			t = "symlink"
		case e.IsDir:
			t = "dir"
		}
		out = append(out, entryDTO{e.Name, t, e.Size})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Type == "dir") != (out[j].Type == "dir") {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return c.JSON(fiber.Map{"path": p, "entries": out})
}

// readFile serves file bytes strictly as inert data: never HTML, never
// sniffed, never rendered in the panel's origin.
func (s *panel) readFile(c fiber.Ctx) error {
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	p, err := queryPath(c, "")
	if err != nil {
		w.Close()
		return err
	}
	f, size, err := w.OpenFile(p)
	w.Close() // the open file keeps its descriptor
	if err != nil {
		return mapFSError(err)
	}
	if rev, err := filesystem.RevisionOf(f); err == nil {
		c.Set(fiber.HeaderETag, `"`+rev+`"`)
	}
	download := c.Query("download") == "1"
	limit := int64(maxEditBytes)
	if download {
		limit = s.maxUpload
	}
	if size > limit {
		f.Close()
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "file is too large to open in the editor; download it instead")
	}
	c.Set(fiber.HeaderContentType, "application/octet-stream")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderContentDisposition, "attachment; filename*=UTF-8''"+url.PathEscape(path.Base(p)))
	return c.SendStream(f, int(size)) // closes f when done
}

func bodyReader(c fiber.Ctx) io.Reader {
	if bs := c.RequestCtx().Request.BodyStream(); bs != nil {
		return bs
	}
	return bytes.NewReader(c.Body())
}

func (s *panel) checkLength(c fiber.Ctx) error {
	if n := c.RequestCtx().Request.Header.ContentLength(); int64(n) > s.maxUpload {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "upload is too large")
	}
	return nil
}

// writeFile replaces a file atomically. Editors send If-Match with the
// revision they loaded; a mismatch (someone else changed the file) is 412 and
// nothing is written. If-None-Match: * creates only when absent.
func (s *panel) writeFile(c fiber.Ctx) error {
	if err := s.checkLength(c); err != nil {
		return err
	}
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	p, err := queryPath(c, "")
	if err != nil {
		return err
	}
	if err := s.preconditions(c, w, p); err != nil {
		return err
	}
	if err := w.Write(p, bodyReader(c), s.maxUpload); err != nil {
		return mapFSError(err)
	}
	if rev, err := w.Revision(p); err == nil {
		c.Set(fiber.HeaderETag, `"`+rev+`"`)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) preconditions(c fiber.Ctx, w *filesystem.Workspace, p string) error {
	match, none := strings.Trim(c.Get(fiber.HeaderIfMatch), `" `), strings.TrimSpace(c.Get(fiber.HeaderIfNoneMatch))
	if match == "" && none == "" {
		return nil
	}
	cur, err := w.Revision(p)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return mapFSError(err)
	}
	if none == "*" && exists {
		return fiber.NewError(fiber.StatusPreconditionFailed, "a file with this name already exists")
	}
	if match != "" && (!exists || strings.TrimPrefix(match, "W/") != cur) {
		return fiber.NewError(fiber.StatusPreconditionFailed, "the file changed since you opened it")
	}
	return nil
}

func (s *panel) mkdirFile(c fiber.Ctx) error {
	var in struct {
		Path string `json:"path"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Mkdir(in.Path); err != nil {
		return mapFSError(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) moveFile(c fiber.Ctx) error {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Rename(in.From, in.To); err != nil {
		return mapFSError(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) deleteFile(c fiber.Ctx) error {
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	p, err := queryPath(c, "")
	if err != nil {
		return err
	}
	// recursive=false deletes only a file, a symlink or an empty directory;
	// the emptiness check is the filesystem's own (rmdir), so nothing added
	// to the directory concurrently can be deleted with it.
	if c.Query("recursive") == "false" {
		if err := w.RemoveOne(p); err != nil {
			if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
				return fiber.NewError(fiber.StatusConflict, "directory is not empty")
			}
			return mapFSError(err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
	if err := w.Remove(p); err != nil {
		return mapFSError(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// extractZip unpacks an uploaded zip into a directory of the workspace.
func (s *panel) extractZip(c fiber.Ctx) error {
	if err := s.checkLength(c); err != nil {
		return err
	}
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	dir, err := queryPath(c, ".")
	if err != nil {
		return err
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := ".upload-" + hex.EncodeToString(rnd[:]) + ".zip"
	if err := w.Write(tmp, bodyReader(c), s.maxUpload); err != nil {
		return mapFSError(err)
	}
	defer w.Remove(tmp)
	f, size, err := w.OpenFile(tmp)
	if err != nil {
		return mapFSError(err)
	}
	defer f.Close()
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return domain.Invalid("not a valid zip archive")
	}
	n, err := w.ExtractZip(zr, dir, filesystem.DefaultExtractLimits)
	if err != nil {
		return mapFSError(err)
	}
	return c.JSON(fiber.Map{"extracted": n})
}

// compressFiles writes a zip of the given paths into the workspace.
func (s *panel) compressFiles(c fiber.Ctx) error {
	var in struct {
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if len(in.Paths) == 0 || len(in.Paths) > 100 {
		return domain.Invalid("choose 1 to 100 files or folders")
	}
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	dest := strings.TrimSpace(in.Dest)
	if dest == "" {
		dest = path.Join(path.Dir(path.Clean(in.Paths[0])), filesystem.ArchiveName(in.Paths, time.Now()))
	}
	if !strings.HasSuffix(strings.ToLower(dest), ".zip") {
		return domain.Invalid("the archive name must end in .zip")
	}
	if ok, _ := w.Exists(dest); ok {
		return domain.Invalid(dest + " already exists")
	}
	pr, pw := io.Pipe()
	go func() {
		_, err := w.WriteZip(pw, in.Paths, filesystem.DefaultZipLimits)
		pw.CloseWithError(err)
	}()
	if err := w.Write(dest, pr, filesystem.DefaultZipLimits.MaxBytes); err != nil {
		pr.CloseWithError(err)
		return mapFSError(err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"path": dest})
}

// decompressFile extracts an archive that is already in the workspace.
func (s *panel) decompressFile(c fiber.Ctx) error {
	var in struct {
		Path string `json:"path"`
		Dir  string `json:"dir"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	defer w.Close()
	dir := in.Dir
	if dir == "" {
		dir = path.Dir(path.Clean(in.Path))
	}
	n, err := w.ExtractArchive(in.Path, dir, filesystem.LargeExtractLimits)
	if err != nil {
		return mapFSError(err)
	}
	return c.JSON(fiber.Map{"extracted": n, "dir": dir})
}

// downloadZip streams a folder (or file) as a zip.
func (s *panel) downloadZip(c fiber.Ctx) error {
	w, err := s.filesFor(c)
	if err != nil {
		return err
	}
	p, err := queryPath(c, ".")
	if err != nil {
		w.Close()
		return err
	}
	if ok, err := w.Exists(p); err != nil || !ok {
		w.Close()
		return fiber.ErrNotFound
	}
	name := filesystem.ArchiveName([]string{p}, time.Now())
	c.Set(fiber.HeaderContentType, "application/zip")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderContentDisposition, "attachment; filename*=UTF-8''"+url.PathEscape(name))
	pr, pw := io.Pipe()
	go func() {
		defer w.Close()
		_, err := w.WriteZip(pw, []string{p}, filesystem.DefaultZipLimits)
		pw.CloseWithError(err)
	}()
	return c.SendStream(pr)
}

// NodeFiles registers the server file routes on r without authorization, for
// rivet-agent. The panel authorizes every request (identity, permission and
// the deployment/restore lock) before it forwards it over the authenticated
// agent connection, so these routes must never be exposed elsewhere.
func NodeFiles(r fiber.Router, files *filesystem.Manager, maxUpload int64) {
	if maxUpload <= 0 {
		maxUpload = defaultMaxUpload
	}
	s := &panel{files: files, maxUpload: maxUpload, trustedFiles: true}
	r.Get("/bots/:id/files", s.listFiles)
	r.Get("/bots/:id/files/content", s.readFile)
	r.Put("/bots/:id/files/content", s.writeFile)
	r.Delete("/bots/:id/files", s.deleteFile)
	r.Post("/bots/:id/files/mkdir", s.mkdirFile)
	r.Post("/bots/:id/files/move", s.moveFile)
	r.Post("/bots/:id/files/extract", s.extractZip)
	r.Post("/bots/:id/files/compress", s.compressFiles)
	r.Post("/bots/:id/files/decompress", s.decompressFile)
	r.Get("/bots/:id/files/zip", s.downloadZip)
}
