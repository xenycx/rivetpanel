package runner

import (
	"errors"
	"fmt"
	"os"
)

// OwnershipProbe is what ChooseOwnership needs to know about the process.
// Tests inject fakes; DefaultOwnershipProbe reads the real process.
type OwnershipProbe struct {
	Euid, Egid func() int
	// CanChown reports whether this process can give a file to uid:gid
	// (CAP_CHOWN, or uid:gid is its own). The panel probes its data root.
	CanChown func(uid, gid int) error
}

// DefaultOwnershipProbe uses the process IDs and the given chown probe.
func DefaultOwnershipProbe(canChown func(uid, gid int) error) OwnershipProbe {
	return OwnershipProbe{Euid: os.Geteuid, Egid: os.Getegid, CanChown: canChown}
}

// OwnershipChoice is the container user and workspace owner in effect.
type OwnershipChoice struct {
	User, Owner string // uid:gid inside containers / owning files on the host
	// Fallback is true when neither value was configured and the process
	// cannot give files to the default user, so both became the process's
	// own uid:gid.
	Fallback bool
	// Default is the user that was replaced (Fallback only).
	Default string
	// Reason explains why the probe failed (Fallback only).
	Reason string
	// Refused is true when the fallback was needed but is not allowed: in
	// production it must be opted in (see ApplySharedUIDPolicy). User and
	// Owner then keep the configured defaults.
	Refused bool
	// OptedIn is true when production runs on the fallback because the
	// operator allowed it explicitly.
	OptedIn bool
}

// ApplySharedUIDPolicy decides whether a fallback to the process's own
// uid:gid may be used. The shared uid also owns the database and the key
// folder, so a tenant process that escapes its container (or reads its
// workspace's parent folders) would act as the panel itself. Development
// keeps the automatic fallback; production refuses it unless allow is set
// (RIVET_ALLOW_SHARED_UID=1 for the panel, --allow-shared-uid or
// RIVET_AGENT_ALLOW_SHARED_UID=1 for rivet-agent).
func ApplySharedUIDPolicy(c OwnershipChoice, production, allow bool) OwnershipChoice {
	if !c.Fallback || !production {
		return c
	}
	if allow {
		c.OptedIn = true
		return c
	}
	c.Refused, c.Fallback = true, false
	c.User, c.Owner = c.Default, c.Default
	return c
}

// RefusalError explains a refused fallback (nil unless Refused). who is
// "the panel" or "the agent"; optIn names the opt-in switch.
func (c OwnershipChoice) RefusalError(who, optIn, configure string) error {
	if !c.Refused {
		return nil
	}
	return errors.New(who + " is neither root nor holds CAP_CHOWN, so it cannot give server files to the dedicated user " + c.Default +
		" (" + c.Reason + "). Running tenant containers as " + who + "'s own uid would let them share the uid that owns the database and keys, " +
		"so this is refused in production. Run " + who + " as root (the systemd unit does) or with CAP_CHOWN, or set " + configure +
		" explicitly; to accept the shared uid anyway, set " + optIn)
}

// ChooseOwnership decides which uid:gid bot containers run as and which owns
// their files. Explicit settings are never changed. Without them, a panel
// that runs as root or holds CAP_CHOWN keeps the dedicated unprivileged
// default; a panel that can do neither would leave every workspace
// unwritable, so it falls back to its own uid:gid (still nonroot inside the
// container) and says so.
func ChooseOwnership(user, owner string, userSet, ownerSet bool, p OwnershipProbe) OwnershipChoice {
	c := OwnershipChoice{User: user, Owner: owner}
	if c.Owner == "" {
		c.Owner = c.User
	}
	if userSet || ownerSet || p.Euid == nil || p.Egid == nil || p.CanChown == nil {
		return c
	}
	euid, egid := p.Euid(), p.Egid()
	if euid == 0 || egid == 0 {
		// Root can chown; a process in group 0 could not become the
		// container user (root group is refused), so keep the default.
		return c
	}
	uid, gid, err := ParseUser(c.Owner)
	if err != nil || (uid == euid && gid == egid) {
		return c
	}
	if err := p.CanChown(uid, gid); err == nil {
		return c
	} else {
		c.Reason = err.Error()
	}
	self := fmt.Sprintf("%d:%d", euid, egid)
	c.Default, c.User, c.Owner, c.Fallback = c.Owner, self, self, true
	return c
}
