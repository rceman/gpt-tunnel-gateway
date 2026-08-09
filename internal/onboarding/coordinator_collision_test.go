package onboarding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

func TestValidateWorktreeTargetAllowsLegacyProjectWithoutIdentifiers(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	legacy, _, _, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	legacy.ID = "gpt-github-gateway"
	legacy.RepositoryURL = "git@example.invalid:owner/gpt-github-gateway.git"
	legacyPath := "gpt-tunnel/v1/projects/gpt-github-gateway/project.json"
	worktree := t.TempDir()
	if err := hub.WriteJSON(worktree, legacyPath, legacy); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(legacyPath)))
	if err != nil {
		t.Fatal(err)
	}
	project, plan, identifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeTarget(worktree, fixture.request, project, plan, identifiers); err != nil {
		t.Fatalf("legacy project rejected: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(legacyPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("legacy project was modified during collision validation")
	}
	if _, err := os.Stat(filepath.Join(worktree, "gpt-tunnel", "v1", "projects", "gpt-github-gateway", "identifiers.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy identifiers were created or became visible: %v", err)
	}
}

func TestValidateWorktreeTargetChecksLegacyProjectRepositoryCollision(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	legacy, _, _, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	legacy.ID = "gpt-github-gateway"
	legacy.RepositoryURL = fixture.request.RepositoryURL
	worktree := t.TempDir()
	if err := hub.WriteJSON(worktree, "gpt-tunnel/v1/projects/gpt-github-gateway/project.json", legacy); err != nil {
		t.Fatal(err)
	}
	project, plan, identifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeTarget(worktree, fixture.request, project, plan, identifiers); err == nil || !strings.Contains(err.Error(), "project or project code collision") {
		t.Fatalf("legacy repository collision error = %v", err)
	}
}

func TestValidateWorktreeTargetChecksProjectCodeWhenIdentifiersExist(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	legacy, _, identifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	legacy.ID = "gpt-github-gateway"
	legacy.RepositoryURL = "git@example.invalid:owner/gpt-github-gateway.git"
	identifiers.ProjectID = legacy.ID
	worktree := t.TempDir()
	if err := hub.WriteJSON(worktree, "gpt-tunnel/v1/projects/gpt-github-gateway/project.json", legacy); err != nil {
		t.Fatal(err)
	}
	if err := hub.WriteJSON(worktree, "gpt-tunnel/v1/projects/gpt-github-gateway/identifiers.json", identifiers); err != nil {
		t.Fatal(err)
	}
	project, plan, candidateIdentifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeTarget(worktree, fixture.request, project, plan, candidateIdentifiers); err == nil || !strings.Contains(err.Error(), "project or project code collision") {
		t.Fatalf("project code collision error = %v", err)
	}
}

func TestScanWorktreeRecordsRejectsOrphanIdentifiersAndPathMismatch(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	project, _, identifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		write func(string) error
		want  string
	}{
		{name: "orphan identifiers", write: func(worktree string) error {
			return hub.WriteJSON(worktree, "gpt-tunnel/v1/projects/example/identifiers.json", identifiers)
		}, want: "missing project"},
		{name: "path embedded mismatch", write: func(worktree string) error {
			return hub.WriteJSON(worktree, "gpt-tunnel/v1/projects/wrong/project.json", project)
		}, want: "does not match embedded project ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worktree := t.TempDir()
			if err := test.write(worktree); err != nil {
				t.Fatal(err)
			}
			if _, err := scanWorktreeRecords(worktree); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("scan error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCoordinatorCorruptHubScanReturnsTypedRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	corruptRequest := fixture.request
	corruptRequest.ProjectID = "corrupt"
	corruptRequest.InitialPlan.ProjectID = "corrupt"
	corruptRequest.RepositoryURL = "git@example.invalid:owner/corrupt.git"
	project, _, _, _, err := buildDurableObjects(corruptRequest)
	if err != nil {
		t.Fatal(err)
	}
	path := "gpt-tunnel/v1/projects/foreign/project.json"
	corruptTransaction, err := fixture.coordinator.Hub.Transact(context.Background(), fixture.base, "seed mismatched corrupt project", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, project); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedHubRevision = corruptTransaction.After
	prepareCoordinatorJournal(t, fixture)
	result, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	var coordinatorErr *CoordinatorError
	if err == nil || !errors.As(err, &coordinatorErr) || coordinatorErr.Code != OnboardingRecoveryRequired {
		t.Fatalf("corrupt Hub error = %v, result=%+v, want typed recovery", err, result)
	}
}

func TestCoordinatorMalformedPreflightHubRecordAtExpectedRevisionReturnsTypedRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	path := "gpt-tunnel/v1/projects/foreign/project.json"
	seed, err := fixture.coordinator.Hub.Transact(context.Background(), fixture.base, "seed malformed preflight project", func(worktree string) ([]string, error) {
		if err := hub.WriteText(worktree, path, "{malformed\n"); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedHubRevision = seed.After
	prepareCoordinatorJournal(t, fixture)
	_, err = fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
}

func TestCoordinatorRejectsPartialTargetWithoutMutation(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	project, _, _, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	paths := canonicalOnboardingPaths(fixture.request.ProjectID)
	if _, err := fixture.coordinator.Hub.Transact(context.Background(), fixture.base, "seed partial onboarding object", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, paths[0], project); err != nil {
			return nil, err
		}
		return []string{paths[0]}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
		t.Fatalf("partial Execute error = %v, want recovery required", err)
	}
	receipt, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || receipt.State != StatePrepared {
		t.Fatalf("partial journal = %+v, err=%v, want prepared and unchanged", receipt, err)
	}
}

func TestCoordinatorRejectsRepositoryCollisionBeforeTransaction(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	otherRequest := fixture.request
	otherRequest.ProjectID = "other"
	otherRequest.InitialPlan.ProjectID = "other"
	otherProject, _, _, _, err := buildDurableObjects(otherRequest)
	if err != nil {
		t.Fatal(err)
	}
	otherPath := "gpt-tunnel/v1/projects/other/project.json"
	if _, err := fixture.coordinator.Hub.Transact(context.Background(), fixture.base, "seed repository collision", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, otherPath, otherProject); err != nil {
			return nil, err
		}
		return []string{otherPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
		t.Fatalf("collision Execute error = %v, want recovery required", err)
	}
	if _, err := fixture.coordinator.Hub.ReadFile(context.Background(), pathsForTest(fixture.request.ProjectID)[0]); err == nil {
		t.Fatal("repository collision must not create target project object")
	}
}
