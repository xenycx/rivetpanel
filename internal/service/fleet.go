package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/lazyre"
)

var tagRe = lazyre.New(`^[a-z0-9][a-z0-9._-]{0,23}$`)

// SetTags replaces a bot's tags (full admin). Tags are lowercase words of up
// to 24 characters; at most 8 per bot.
func (s *BotService) SetTags(ctx context.Context, actor domain.User, botID string, tags []string) ([]string, error) {
	if _, err := s.loadPerm(ctx, actor, botID, domain.PermFullAdmin, false); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		if !tagRe.MatchString(t) {
			return nil, domain.Invalid("tags are up to 24 lowercase letters, digits, dots, dashes or underscores")
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > 8 {
		return nil, domain.Invalid("at most 8 tags per bot")
	}
	sort.Strings(out)
	return out, s.Store.SetBotTags(ctx, botID, out)
}

// SetFavorite stars a bot for the actor (any access).
func (s *BotService) SetFavorite(ctx context.Context, actor domain.User, botID string, on bool) error {
	if _, err := s.loadPerm(ctx, actor, botID, permAny, false); err != nil {
		return err
	}
	return s.Store.SetFavorite(ctx, actor.ID, botID, on, s.now())
}

// TagsAndFavorites returns fleet metadata for the actor's list view.
func (s *BotService) TagsAndFavorites(ctx context.Context, actor domain.User) (map[string][]string, map[string]bool, error) {
	return s.Store.TagsAndFavorites(ctx, actor.ID)
}

// BatchResult is the outcome of one bot in a batch action.
type BatchResult struct {
	BotID   string
	Name    string
	OK      bool
	Message string
}

// MaxBatch bounds how many bots one batch request may touch.
const MaxBatch = 50

// Batch applies start/stop/restart to several bots, each with its own
// permission check; one bot's failure does not stop the others.
func (s *BotService) Batch(ctx context.Context, actor domain.User, action string, ids []string) ([]BatchResult, error) {
	if len(ids) == 0 || len(ids) > MaxBatch {
		return nil, domain.Invalid("choose between 1 and 50 bots")
	}
	var op func(context.Context, domain.User, string) (domain.Bot, error)
	switch action {
	case "start":
		op = s.Start
	case "stop":
		op = s.Stop
	case "restart":
		op = s.Restart
	default:
		return nil, domain.Invalid("action must be start, stop or restart")
	}
	seen := map[string]bool{}
	out := make([]BatchResult, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		r := BatchResult{BotID: id, OK: true}
		b, err := op(ctx, actor, id)
		if err != nil {
			r.OK, r.Message = false, batchMessage(err)
		} else {
			r.Name = b.Name
		}
		out = append(out, r)
	}
	return out, nil
}

func batchMessage(err error) string {
	var ve *domain.ValidationError
	var be *domain.BusyError
	switch {
	case asValidation(err, &ve):
		return ve.Msg
	case asBusy(err, &be):
		return be.Error()
	case errors.As(err, new(*domain.CapacityError)):
		return err.Error()
	case isNotFound(err):
		return "not found"
	case isForbidden(err):
		return "you may not start or stop this bot"
	case isRunnerUnavailable(err):
		return "no runner is available"
	}
	return "the request failed"
}
