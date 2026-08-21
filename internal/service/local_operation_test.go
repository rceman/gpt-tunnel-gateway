package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestLocalOperationIsProjectScopedIdempotentAndRestartReadable(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	s := New(config.Config{StateDir: stateDir, Projects: map[string]config.ProjectConfig{
		"example": {Root: root, Mirror: t.TempDir(), Remote: "example-remote", DefaultBranch: "main", AirelaySessionKey: "example-master", ProjectCode: "EXM"},
	}})
	in := LocalOperationCreateInput{ProjectID: "example", Kind: "task/create", CorrelationID: "request-1", EntityID: "EXM-TSK1"}
	first, err := s.LocalOperationCreate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.LocalOperationCreate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "EXM-OPR1" || second.ID != first.ID {
		t.Fatalf("idempotency IDs first=%s second=%s", first.ID, second.ID)
	}
	updated, err := s.LocalOperationUpdate(context.Background(), LocalOperationUpdateInput{ProjectID: "example", ID: first.ID, Status: "completed", Result: "accepted"})
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(config.Config{StateDir: stateDir, Projects: map[string]config.ProjectConfig{
		"example": {Root: root, Mirror: t.TempDir(), Remote: "example-remote", DefaultBranch: "main", AirelaySessionKey: "example-master", ProjectCode: "EXM"},
	}})
	read, err := restarted.LocalOperationRead(context.Background(), LocalOperationReadInput{ProjectID: "example", ID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if read.Status != "completed" || read.Result != updated.Result || read.CorrelationID != "request-1" {
		t.Fatalf("restart read=%#v", read)
	}
}

func TestLocalOperationRejectsCrossProjectAndMalformedIDs(t *testing.T) {
	stateDir := t.TempDir()
	s := New(config.Config{StateDir: stateDir, Projects: map[string]config.ProjectConfig{
		"example": {Root: t.TempDir(), Mirror: t.TempDir(), Remote: "example-remote", DefaultBranch: "main", AirelaySessionKey: "example-master", ProjectCode: "EXM"},
		"other":   {Root: t.TempDir(), Mirror: t.TempDir(), Remote: "other-remote", DefaultBranch: "main", AirelaySessionKey: "other-master", ProjectCode: "OTH"},
	}})
	operation, err := s.LocalOperationCreate(context.Background(), LocalOperationCreateInput{ProjectID: "example", Kind: "status", CorrelationID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LocalOperationRead(context.Background(), LocalOperationReadInput{ProjectID: "other", ID: operation.ID}); err == nil {
		t.Fatal("cross-project operation read unexpectedly succeeded")
	}
	if _, err := s.LocalOperationRead(context.Background(), LocalOperationReadInput{ProjectID: "example", ID: "EXM-OPR0"}); err == nil {
		t.Fatal("malformed operation ID unexpectedly succeeded")
	}
}
