package sqlite

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrNotFound
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		return domain.ErrConflict
	}
	return err
}
