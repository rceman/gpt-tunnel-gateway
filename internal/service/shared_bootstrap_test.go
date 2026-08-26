package service

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestSharedBootstrapMarkerPreventsHubReimportAfterRestart(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	entity, err := s.Durability.ReadSharedEntity(context.Background(), "project_configuration", "example")
	if err != nil {
		t.Fatal(err)
	}
	modifiedPayload := append(append([]byte(nil), entity.Payload...), '\n')
	if err := s.Durability.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID: entity.ID, Revision: entity.Revision, Payload: modifiedPayload, UpdatedAt: entity.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub")
	if err := s.BootstrapSharedFromHub(context.Background()); err != nil {
		t.Fatalf("completed bootstrap attempted Hub re-import: %v", err)
	}
	after, err := s.Durability.ReadSharedEntity(context.Background(), "project_configuration", "example")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.Payload, modifiedPayload) {
		t.Fatalf("completed bootstrap rewrote Shared projection: got=%q want=%q", after.Payload, modifiedPayload)
	}
}
