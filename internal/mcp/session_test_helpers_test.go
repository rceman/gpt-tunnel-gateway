package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func mcpServiceWithSQLite(t *testing.T, c config.Config) (*service.Service, *sqlitestore.Databases) {
	t.Helper()
	if c.StateDir == "" {
		c.StateDir = t.TempDir()
	}
	db, err := sqlitestore.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return service.NewWithDurabilityDeferredWorkers(c, db), db
}

func mcpSeedProjectConfiguration(t *testing.T, svc *service.Service, projectID string) {
	t.Helper()
	now := time.Now().UTC()
	configuration := model.DefaultProjectConfiguration(projectID, now)
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Durability.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload,
		UpdatedAt: configuration.UpdatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
}

/*
The session store must share the owning service's Local database. Opening a
second Databases handle for the same StateDir would violate the single-owner
boundary and make the fixture itself fail before exercising Session code.
*/
func mcpSQLiteSessionStore(t *testing.T, svc *service.Service) durableSession.Store {
	t.Helper()
	if svc == nil || svc.Durability == nil || svc.Durability.Local == nil {
		t.Fatal("test service has no disposable Local durability")
	}
	return durableSession.NewStoreWithDurability(svc.Durability)
}
