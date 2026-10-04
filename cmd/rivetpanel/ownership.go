package main

import (
	"log/slog"

	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// resolveOwnership picks the container user and workspace owner (see
// runner.ChooseOwnership) and writes them back into cfg. When the panel had
// to fall back to its own uid:gid it warns and, when repair is set, hands
// workspaces created under another user back to that uid:gid where it can.
// In production the fallback needs RIVET_ALLOW_SHARED_UID=1; without it the
// choice is Refused, cfg is left alone and nothing is repaired (the caller
// refuses to start; see sharedUIDError).
func resolveOwnership(cfg *config.Config, wsm *filesystem.Manager, log *slog.Logger, repair bool) runner.OwnershipChoice {
	c := runner.ChooseOwnership(cfg.ContainerUser, cfg.WorkspaceOwner, cfg.ContainerUserSet, cfg.WorkspaceOwnerSet,
		runner.DefaultOwnershipProbe(wsm.ProbeOwnership))
	c = runner.ApplySharedUIDPolicy(c, cfg.Production, cfg.AllowSharedUID)
	if !c.Fallback {
		return c
	}
	cfg.ContainerUser, cfg.WorkspaceOwner = c.User, c.Owner
	if log == nil {
		return c
	}
	log.Warn("the panel is not root and lacks CAP_CHOWN, so bot containers run as the panel's own user; "+
		"they stay non-root inside the container but share this uid, which owns the database and keys, on the host. "+
		"For production run the panel as root (the systemd unit does) or set RIVET_CONTAINER_USER and RIVET_WORKSPACE_OWNER",
		"user", c.User, "default", c.Default, "probe", c.Reason, "allowed_by", map[bool]string{true: "RIVET_ALLOW_SHARED_UID=1", false: "RIVET_ENV=development"}[c.OptedIn])
	if !repair {
		return c
	}
	uid, gid, err := runner.ParseUser(c.Owner)
	if err != nil {
		return c
	}
	n, failed, err := wsm.RepairOwnership(uid, gid)
	switch {
	case err != nil:
		log.Warn("workspace ownership could not be checked", "err", err)
	case len(failed) > 0:
		log.Warn("some workspaces belong to another user and cannot be repaired without root; their bots will not start until an administrator runs chown -R "+c.Owner+" on them",
			"workspaces", failed, "data_root", cfg.DataRoot)
	}
	if n > 0 {
		log.Info("handed existing workspaces to the panel user", "count", n, "user", c.Owner)
	}
	return c
}

// sharedUIDError is the startup refusal of a production panel that would
// need the shared-uid fallback without RIVET_ALLOW_SHARED_UID=1.
func sharedUIDError(c runner.OwnershipChoice) error {
	return c.RefusalError("the panel", "RIVET_ALLOW_SHARED_UID=1",
		"RIVET_CONTAINER_USER and RIVET_WORKSPACE_OWNER (for example to the panel's own uid:gid, which accepts the same risk knowingly)")
}
