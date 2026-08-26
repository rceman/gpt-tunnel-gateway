package service

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestSharedADRBootstrapAcceptsLegacyIDWithoutAdvancingSequence(t *testing.T) {
	number, counted, err := sharedADRBootstrapSequenceNumber("ADR-0001", "GTW")
	if err != nil {
		t.Fatalf("legacy ADR rejected: %v", err)
	}
	if counted || number != 0 {
		t.Fatalf("legacy ADR sequence result=(%d,%t), want (0,false)", number, counted)
	}
}

func TestSharedADRBootstrapCountsOnlyRecognizedProjectIDs(t *testing.T) {
	tests := []struct {
		id    string
		want  uint64
		count bool
	}{
		{id: "GTW-ADR9", want: 9, count: true},
		{id: "GTW-A7", want: 7, count: true},
	}
	for _, test := range tests {
		number, counted, err := sharedADRBootstrapSequenceNumber(test.id, "GTW")
		if err != nil || number != test.want || counted != test.count {
			t.Fatalf("%s result=(%d,%t,%v), want (%d,%t,nil)", test.id, number, counted, err, test.want, test.count)
		}
	}
}

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
