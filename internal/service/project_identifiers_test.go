package service

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestProjectIdentifiersReadRequiresExistingStrictRecord(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	if _, err := s.ProjectIdentifiersRead(context.Background(), "example"); err == nil || !IsNotFound(err) {
		t.Fatalf("missing identifiers record was not reported as not found: %v", err)
	}
}

func TestProjectIdentifiersAdoptAndRead(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	record, operation, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.SchemaVersion != model.SchemaVersion || record.ProjectID != "example" || record.ProjectCode != "EXM" || record.NextTaskNumber != 1 || record.NextADRNumber != 1 {
		t.Fatalf("unexpected adopted identifiers: %#v", record)
	}
	if operation.Status != "adopted" || len(operation.Hub.Paths) != 1 || operation.Hub.Paths[0] != s.projectIdentifiersPath("example") {
		t.Fatalf("unexpected adoption operation: %#v", operation)
	}
	read, err := s.ProjectIdentifiersRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if read != record {
		t.Fatalf("read record differs from adopted record: %#v %#v", read, record)
	}
	if _, _, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "NEW",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}); err == nil || !strings.Contains(err.Error(), "already exist") {
		t.Fatalf("existing identifiers record was replaced: %v", err)
	}
	unchanged, err := s.ProjectIdentifiersRead(context.Background(), "example")
	if err != nil || unchanged.ProjectCode != "EXM" {
		t.Fatalf("existing project code changed after rejected adoption: %#v %v", unchanged, err)
	}
}

func TestProjectIdentifiersAdoptRejectsDuplicateCodes(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	secondRevision := registerIdentifierProject(t, s, "second", revision)
	first, firstOperation, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "DUP",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: secondRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectCode != "DUP" {
		t.Fatalf("unexpected first code: %#v", first)
	}
	if _, _, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "second",
		ProjectCode: "DUP",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: firstOperation.Hub.After,
		},
	}); err == nil || !strings.Contains(err.Error(), "already adopted") {
		t.Fatalf("duplicate project code was accepted: %v", err)
	}
}

func TestProjectIdentifiersAdoptConcurrentDuplicateCodesHasOneWinner(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	secondRevision := registerIdentifierProject(t, s, "second", revision)
	_ = secondRevision
	start := make(chan struct{})
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for _, projectID := range []string{"example", "second"} {
		projectID := projectID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
				ProjectID:   projectID,
				ProjectCode: "CON",
			})
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	winners := 0
	losers := 0
	for err := range errors {
		if err == nil {
			winners++
		} else {
			losers++
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("concurrent duplicate adoption had winners=%d losers=%d", winners, losers)
	}
}

func registerIdentifierProject(t *testing.T, s *Service, id, expected string) string {
	t.Helper()
	_, root, _ := testutil.RepoWithBareRemote(t)
	s.Config.Projects[id] = config.ProjectConfig{Root: root, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: id + "_master"}
	project := model.Project{SchemaVersion: model.SchemaVersion, ID: id, RepositoryURL: "git@example.invalid:" + id + ".git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("b", 40), Status: "active"}
	result, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{
		Project: project,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: expected,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.Hub.After
}
