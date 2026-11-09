package service

import (
	"context"
	"errors"
	"io/fs"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// sqliteListMax bounds the SQLite files the Data tab offers.
const sqliteListMax = 50

// LocalAddonQuerier runs extension database queries for apps on this host.
// Callers have already authorized the person (the bot's environment
// permission); it only finds the add-on's password, or the SQLite file, and
// runs the query.
type LocalAddonQuerier struct {
	Runner *runner.AddonQuerier
	Bots   *BotService
	Files  *filesystem.Manager // nil: SQLite files are not offered
}

// QueryAddon implements AddonQuerier.
func (l *LocalAddonQuerier) QueryAddon(ctx context.Context, b domain.Bot, kind string, q AddonQuery) (AddonQueryResult, error) {
	if kind == "sqlite" {
		return l.querySQLite(ctx, b, q)
	}
	env, err := l.Bots.DecryptEnv(ctx, b.ID)
	if err != nil {
		return AddonQueryResult{}, err
	}
	return l.Runner.Query(ctx, b, kind, env[domain.AddonPasswordVar(kind)], q)
}

// SQLiteFiles implements AddonQuerier: the SQLite databases in the app's
// files (its workspace, and a container app's volumes).
func (l *LocalAddonQuerier) SQLiteFiles(ctx context.Context, b domain.Bot) ([]filesystem.SQLiteFile, error) {
	if l.Files == nil {
		return nil, nil
	}
	w, err := l.Files.Open(b.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer w.Close()
	return w.FindSQLite(sqliteListMax)
}

func (l *LocalAddonQuerier) querySQLite(ctx context.Context, b domain.Bot, q AddonQuery) (AddonQueryResult, error) {
	if l.Files == nil {
		return AddonQueryResult{}, domain.Invalid("SQLite files are not available on this panel")
	}
	w, err := l.Files.Open(b.ID)
	if err != nil {
		return AddonQueryResult{}, err
	}
	rel, uid, gid, err := w.SQLiteOwner(q.File)
	w.Close()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return AddonQueryResult{}, domain.Invalid("that SQLite file no longer exists")
	case errors.Is(err, filesystem.ErrNotSQLite), errors.Is(err, filesystem.ErrInvalidPath):
		return AddonQueryResult{}, domain.Invalid("choose a SQLite database file of this app")
	case err != nil:
		return AddonQueryResult{}, err
	}
	host, err := l.Files.Path(b.ID)
	if err != nil {
		return AddonQueryResult{}, err
	}
	return l.Runner.QuerySQLite(ctx, b, host, rel, uid, gid, q)
}
