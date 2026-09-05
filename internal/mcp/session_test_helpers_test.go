package mcp

import (
	"sync"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

var testSessionStores sync.Map

func mcpSQLiteSessionStore(t *testing.T, stateDir string) durableSession.Store {
	t.Helper()
	if value, ok := testSessionStores.Load(stateDir); ok {
		return value.(durableSession.Store)
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	store := durableSession.NewStoreWithDurability(db)
	actual, loaded := testSessionStores.LoadOrStore(stateDir, store)
	if loaded {
		_ = db.Close()
		return actual.(durableSession.Store)
	}
	t.Cleanup(func() {
		if value, ok := testSessionStores.LoadAndDelete(stateDir); ok {
			_ = value.(durableSession.Store).Durability.Close()
		}
	})
	return store
}
