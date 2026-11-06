package service

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// LocalAddonQuerier runs extension database queries for apps on this host.
// Callers have already authorized the person (the bot's environment
// permission); it only finds the add-on's password and runs the query.
type LocalAddonQuerier struct {
	Runner *runner.AddonQuerier
	Bots   *BotService
}

// QueryAddon implements AddonQuerier.
func (l *LocalAddonQuerier) QueryAddon(ctx context.Context, b domain.Bot, kind string, q AddonQuery) (AddonQueryResult, error) {
	env, err := l.Bots.DecryptEnv(ctx, b.ID)
	if err != nil {
		return AddonQueryResult{}, err
	}
	return l.Runner.Query(ctx, b, kind, env[domain.AddonPasswordVar(kind)], q)
}
