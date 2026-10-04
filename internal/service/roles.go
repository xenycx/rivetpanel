package service

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// RoleInput creates or changes a custom role.
type RoleInput struct {
	Name        string
	Description string
	Permissions []string
}

func validateRole(in RoleInput) (domain.Role, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		return domain.Role{}, domain.Invalid("a role name must be 1 to 64 characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return domain.Role{}, domain.Invalid("a role name cannot contain control characters")
		}
	}
	desc := strings.TrimSpace(in.Description)
	if len(desc) > 280 {
		return domain.Role{}, domain.Invalid("a role description must be at most 280 characters")
	}
	var perms []string
	for _, p := range in.Permissions {
		if !domain.ValidPermission(p) {
			return domain.Role{}, domain.Invalid("unknown permission " + strings.ToValidUTF8(p, "?"))
		}
		if !slices.Contains(perms, p) {
			perms = append(perms, p)
		}
	}
	return domain.Role{Name: name, Description: desc, Permissions: perms}, nil
}

// grantable refuses permissions the actor does not hold: delegated
// administrators can never hand out more than they have.
func grantable(actor domain.User, perms []string) error {
	if actor.IsAdmin() {
		return nil
	}
	for _, p := range perms {
		if !actor.Can(p) {
			return domain.Invalid("you cannot grant " + p + ": you do not hold it yourself")
		}
	}
	return nil
}

// manageable refuses a delegated account manager acting on an account it
// does not outrank: an administrator, or an account whose role holds an
// administration permission the actor lacks. Changing such an account's
// address, verified flag, role or enabled state could otherwise be used to
// take it over (an address change followed by a password reset).
func manageable(actor, target domain.User) error {
	if actor.IsAdmin() {
		return nil
	}
	if target.IsAdmin() {
		return domain.ErrForbidden
	}
	held := target.RolePermissions()
	for _, x := range domain.Permissions {
		if x.Group == "administration" && slices.Contains(held, x.Name) && !actor.Can(x.Name) {
			return domain.ErrForbidden
		}
	}
	return nil
}

// ListRoles returns the system and custom roles to anyone who can view or
// manage accounts or roles.
func (s *AuthService) ListRoles(ctx context.Context, actor domain.User) ([]domain.Role, error) {
	if !actor.Can(domain.PermUsersView) && !actor.Can(domain.PermUsersManage) && !actor.Can(domain.PermRolesManage) {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListRoles(ctx)
}

// CreateRole adds a custom role.
func (s *AuthService) CreateRole(ctx context.Context, actor domain.User, in RoleInput) (domain.Role, error) {
	if !actor.Can(domain.PermRolesManage) {
		return domain.Role{}, domain.ErrForbidden
	}
	r, err := validateRole(in)
	if err != nil {
		return domain.Role{}, err
	}
	if err := grantable(actor, r.Permissions); err != nil {
		return domain.Role{}, err
	}
	now := s.now().UnixMilli()
	r.ID, r.CreatedAtMS, r.UpdatedAtMS = uuid.NewString(), now, now
	if err := s.Store.CreateRole(ctx, r); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.Role{}, domain.Invalid("a role with that name already exists")
		}
		return domain.Role{}, err
	}
	return s.Store.GetRole(ctx, r.ID)
}

// editableRole loads a custom role the actor may change: never a system
// role, and for delegated administrators never their own role or a role
// holding permissions they lack.
func (s *AuthService) editableRole(ctx context.Context, actor domain.User, id string) (domain.Role, error) {
	if !actor.Can(domain.PermRolesManage) {
		return domain.Role{}, domain.ErrForbidden
	}
	cur, err := s.Store.GetRole(ctx, id)
	if err != nil {
		return domain.Role{}, err
	}
	if cur.System {
		return domain.Role{}, domain.Invalid("built-in roles cannot be changed; create a custom role instead")
	}
	if !actor.IsAdmin() {
		if actor.RoleID == cur.ID {
			return domain.Role{}, domain.Invalid("you cannot change the role you hold")
		}
		if err := grantable(actor, cur.Permissions); err != nil {
			return domain.Role{}, domain.ErrForbidden
		}
	}
	return cur, nil
}

// UpdateRole changes a custom role; accounts holding it are affected on
// their next request.
func (s *AuthService) UpdateRole(ctx context.Context, actor domain.User, id string, in RoleInput) (domain.Role, error) {
	cur, err := s.editableRole(ctx, actor, id)
	if err != nil {
		return domain.Role{}, err
	}
	r, err := validateRole(in)
	if err != nil {
		return domain.Role{}, err
	}
	if err := grantable(actor, r.Permissions); err != nil {
		return domain.Role{}, err
	}
	r.ID, r.UpdatedAtMS = cur.ID, s.now().UnixMilli()
	if err := s.Store.UpdateRole(ctx, r); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.Role{}, domain.Invalid("a role with that name already exists")
		}
		return domain.Role{}, err
	}
	return s.Store.GetRole(ctx, id)
}

// DeleteRole removes a custom role that no account holds.
func (s *AuthService) DeleteRole(ctx context.Context, actor domain.User, id string) (domain.Role, error) {
	cur, err := s.editableRole(ctx, actor, id)
	if err != nil {
		return domain.Role{}, err
	}
	return cur, s.Store.DeleteRole(ctx, id)
}

// AssignRole gives an account the built-in "admin" or "user" role or a
// custom role (by id). Only administrators make administrators or change an
// administrator's role; delegated account managers can only assign roles
// whose permissions they hold, not to themselves and not to accounts they do
// not outrank (see manageable). The store refuses to
// remove the last active administrator.
func (s *AuthService) AssignRole(ctx context.Context, actor domain.User, userID, roleID string) (domain.Role, error) {
	if !actor.Can(domain.PermUsersManage) {
		return domain.Role{}, domain.ErrForbidden
	}
	target, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return domain.Role{}, err
	}
	if !actor.IsAdmin() {
		if actor.ID == userID {
			return domain.Role{}, domain.Invalid("you cannot change your own role")
		}
		if roleID == domain.RoleAdmin {
			return domain.Role{}, domain.ErrForbidden
		}
		if err := manageable(actor, target); err != nil {
			return domain.Role{}, err
		}
	}
	role, err := s.Store.GetRole(ctx, roleID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Role{}, domain.Invalid("that role does not exist")
	}
	if err != nil {
		return domain.Role{}, err
	}
	if err := grantable(actor, role.Permissions); err != nil {
		return domain.Role{}, err
	}
	now := s.now().UnixMilli()
	if role.System {
		err = s.Store.SetUserRole(ctx, userID, role.ID, now)
	} else {
		err = s.Store.SetUserCustomRole(ctx, userID, role.ID, now)
	}
	return role, err
}
