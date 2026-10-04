package api

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/pkgmgr"
	"github.com/xenycx/rivetpanel/internal/service"
)

// manifests is where the package manager reads and writes a server's
// manifest: the panel's own disk, or the server's remote node.
type manifests interface {
	exists(name string) bool
	// read returns the file and its revision; a missing file is fs.ErrNotExist.
	read(ctx context.Context, p string) ([]byte, string, error)
	// write replaces the file only if it is still at rev ("" = must not
	// exist yet); otherwise it fails with domain.ErrConflict.
	write(ctx context.Context, p string, data []byte, rev string) error
	close()
}

type localManifests struct{ w *filesystem.Workspace }

func (l localManifests) exists(n string) bool { ok, _ := l.w.Exists(n); return ok }
func (l localManifests) read(_ context.Context, p string) ([]byte, string, error) {
	data, err := l.w.Read(p, pkgmgr.MaxManifestBytes)
	return data, "", err
}
func (l localManifests) write(_ context.Context, p string, data []byte, _ string) error {
	return l.w.Write(p, bytes.NewReader(data), pkgmgr.MaxManifestBytes)
}
func (l localManifests) close() { l.w.Close() }

// remoteManifests edits a manifest on the server's node through its agent.
// Writes carry the revision that was read (If-Match), so a concurrent change
// on the node is a conflict instead of a lost update. The panel's own disk
// is never touched.
type remoteManifests struct {
	nf         service.NodeFiles
	node, bot  string
	rootListed map[string]bool
}

func (r remoteManifests) exists(n string) bool { return r.rootListed[n] }
func (r remoteManifests) read(ctx context.Context, p string) ([]byte, string, error) {
	return r.nf.ReadFileRevision(ctx, r.node, r.bot, p, pkgmgr.MaxManifestBytes)
}
func (r remoteManifests) write(ctx context.Context, p string, data []byte, rev string) error {
	if int64(len(data)) > pkgmgr.MaxManifestBytes {
		return filesystem.ErrTooLarge
	}
	_, err := r.nf.WriteFile(ctx, r.node, r.bot, p, data, rev, rev == "")
	return err
}
func (remoteManifests) close() {}

// pkgCtx authorizes the caller (edit-files permission) and resolves the
// manifest handling for the bot's runtime.
func (s *panel) pkgCtx(c fiber.Ctx) (manifests, *pkgmgr.Ecosystem, error) {
	if s.files == nil {
		return nil, nil, fiber.ErrNotFound
	}
	b, err := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermEditFiles)
	if err != nil {
		return nil, nil, err
	}
	if !isSafeMethod(c.Method()) {
		if err := s.bots.FilesBlocked(b.ID); err != nil {
			return nil, nil, err
		}
	}
	var m manifests
	nf, remote, err := s.bots.RemoteFiles(b, "The package manager")
	switch {
	case err != nil:
		return nil, nil, err
	case remote:
		root, err := nf.ListDir(c.Context(), b.NodeID, b.ID, ".")
		if err != nil {
			return nil, nil, mapFSError(err)
		}
		listed := map[string]bool{}
		for _, e := range root {
			if !e.IsDir && !e.Symlink {
				listed[e.Name] = true
			}
		}
		m = remoteManifests{nf: nf, node: b.NodeID, bot: b.ID, rootListed: listed}
	default:
		w, err := s.files.Open(b.ID)
		if err != nil {
			return nil, nil, mapFSError(err)
		}
		m = localManifests{w}
	}
	eco, err := pkgmgr.ForRuntime(b.Runtime, m.exists)
	if err != nil {
		m.close()
		return nil, nil, err
	}
	return m, eco, nil
}

func mapPkgError(err error) error {
	var pe *pkgmgr.ParseError
	switch {
	case errors.Is(err, pkgmgr.ErrUnsupported):
		return domain.Invalid("the visual package manager is not available for this runtime")
	case errors.As(err, &pe):
		return domain.Invalid(pe.Error())
	}
	return err
}

type depsResponse struct {
	Supported bool                `json:"supported"`
	Ecosystem string              `json:"ecosystem,omitempty"`
	File      string              `json:"file,omitempty"`
	Exists    bool                `json:"exists"`
	Groups    []string            `json:"groups,omitempty"`
	Deps      []pkgmgr.Dependency `json:"deps"`
}

func (s *panel) listPackages(c fiber.Ctx) error {
	w, eco, err := s.pkgCtx(c)
	if errors.Is(err, pkgmgr.ErrUnsupported) {
		return c.JSON(depsResponse{Supported: false, Deps: []pkgmgr.Dependency{}})
	}
	if err != nil {
		return err
	}
	defer w.close()
	res := depsResponse{Supported: true, Ecosystem: eco.ID, File: eco.File, Groups: eco.Groups, Deps: []pkgmgr.Dependency{}}
	data, _, err := w.read(c.Context(), eco.File)
	if errors.Is(err, fs.ErrNotExist) {
		return c.JSON(res)
	}
	if err != nil {
		return mapFSError(err)
	}
	res.Exists = true
	if res.Deps, err = eco.Parse(data); err != nil {
		return mapPkgError(err)
	}
	if res.Deps == nil {
		res.Deps = []pkgmgr.Dependency{}
	}
	return c.JSON(res)
}

// newManifests are created when a project has none yet (where that is safe).
var newManifests = map[string]string{
	"package.json":     "{\n  \"name\": \"bot\",\n  \"version\": \"1.0.0\",\n  \"private\": true\n}\n",
	"requirements.txt": "",
}

func (s *panel) editPackages(c fiber.Ctx) error {
	var in struct {
		Ops []pkgmgr.Op `json:"ops"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	w, eco, err := s.pkgCtx(c)
	if err != nil {
		return mapPkgError(err)
	}
	defer w.close()
	data, rev, err := w.read(c.Context(), eco.File)
	if errors.Is(err, fs.ErrNotExist) {
		tpl, ok := newManifests[eco.File]
		if !ok {
			return domain.Invalid(eco.File + " does not exist yet; create it in the file manager first")
		}
		data, err = []byte(tpl), nil
	}
	if err != nil {
		return mapFSError(err)
	}
	out, err := eco.Apply(data, in.Ops)
	if err != nil {
		var pe *pkgmgr.ParseError
		if errors.As(err, &pe) {
			return mapPkgError(err)
		}
		return domain.Invalid(err.Error()) // op validation / conflicts are client errors
	}
	if err := w.write(c.Context(), eco.File, out, rev); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return fiber.NewError(fiber.StatusConflict, eco.File+" changed while it was being edited; reload and try again")
		}
		return mapFSError(err)
	}
	deps, err := eco.Parse(out)
	if err != nil {
		return mapPkgError(err)
	}
	if deps == nil {
		deps = []pkgmgr.Dependency{}
	}
	return c.JSON(depsResponse{Supported: true, Ecosystem: eco.ID, File: eco.File, Exists: true, Groups: eco.Groups, Deps: deps})
}

func (s *panel) searchPackages(c fiber.Ctx) error {
	w, eco, err := s.pkgCtx(c)
	if err != nil {
		return mapPkgError(err)
	}
	w.close()
	res, err := s.registry.Search(c.Context(), eco.ID, strings.Clone(c.Query("q")))
	if err != nil {
		return registryError(err)
	}
	return c.JSON(fiber.Map{"results": res})
}

func (s *panel) latestPackage(c fiber.Ctx) error {
	w, eco, err := s.pkgCtx(c)
	if err != nil {
		return mapPkgError(err)
	}
	w.close()
	v, err := s.registry.Latest(c.Context(), eco.ID, strings.Clone(c.Query("name")))
	if err != nil {
		return registryError(err)
	}
	return c.JSON(fiber.Map{"version": v})
}

// registryError separates the caller's mistakes (400) from upstream trouble (502).
func registryError(err error) error {
	m := err.Error()
	if strings.HasPrefix(m, "registry lookup failed") || strings.Contains(m, "status ") || strings.Contains(m, "deadline") || strings.Contains(m, "dial") {
		return fiber.NewError(fiber.StatusBadGateway, "the package registry could not be reached")
	}
	return domain.Invalid(m)
}
