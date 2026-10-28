package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// DeleteAccount removes an account for good, with everything it owns: its
// bots, game servers and sites (containers, files and add-on data go the way
// a normal deletion takes them), its own workspaces, sessions, keys, tokens,
// notifications, tickets and quota. Repository links other accounts' bots
// made with its GitHub token are removed. The audit log keeps its history.
//
// Only built-in administrators may delete accounts, never their own and
// never another administrator's (change the role first). An account that
// owns a team workspace holding other accounts' bots or sites is refused
// before anything changes. The account is disabled first, which signs it
// out everywhere and stops it creating anything meanwhile; if a later step
// fails it stays disabled and the deletion can be repeated. sites may be
// nil when site hosting is off.
func (s *BotService) DeleteAccount(ctx context.Context, actor domain.User, userID string, sites *SiteService) (domain.User, error) {
	if !actor.IsAdmin() {
		return domain.User{}, domain.ErrForbidden
	}
	if actor.ID == userID {
		return domain.User{}, domain.Invalid("you cannot delete your own account")
	}
	target, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if target.IsAdmin() {
		return domain.User{}, domain.Invalid("change this account's role to user before deleting it")
	}
	bots, siteIDs, shared, err := s.Store.AccountHoldings(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if len(shared) > 0 {
		return domain.User{}, domain.Invalid(fmt.Sprintf("the workspace %q this account owns holds other accounts' bots or sites; move them out or remove them first", shared[0]))
	}
	if err := s.Store.SetUserDisabled(ctx, userID, true, s.now()); err != nil {
		return domain.User{}, err
	}
	// Sites first: deleting a site with an app also removes its app unit.
	if sites.Enabled() {
		for _, id := range siteIDs {
			if err := sites.Delete(ctx, actor, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return domain.User{}, fmt.Errorf("delete site: %w", err)
			}
		}
	}
	for _, id := range bots {
		if err := s.delete(ctx, actor, id, true); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return domain.User{}, fmt.Errorf("delete bot: %w", err)
		}
	}
	return target, s.Store.DeleteUserAccount(ctx, userID)
}
