package service

import (
	"fmt"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// diskPreflight refuses work that writes large files (archives, staging,
// deployments) when the data filesystem is nearly full. It reduces failures
// halfway through; it is not a quota.
func diskPreflight(fs *filesystem.Manager, min int64) error {
	if fs == nil || min <= 0 {
		return nil
	}
	_, free, err := fs.Statfs()
	if err != nil {
		return nil // unknown: do not block work on a failed probe
	}
	if int64(free) < min {
		return domain.Invalid(fmt.Sprintf("the server has only %d MiB of free disk space; at least %d MiB is needed. Delete old backups or free space on the host",
			free>>20, min>>20))
	}
	return nil
}
