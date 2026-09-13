package session

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func CutoverLegacyJSON(_ context.Context, _ string, db *sqlitestore.Databases) error {
	if db == nil || db.Local == nil {
		return fmt.Errorf("local session store is unavailable")
	}
	return nil
}
