package sqlitestore

import (
	"errors"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func IsSharedRevisionConflict(err error) bool {
	return errors.Is(err, upstream.ErrRowsAffectedMismatch)
}
