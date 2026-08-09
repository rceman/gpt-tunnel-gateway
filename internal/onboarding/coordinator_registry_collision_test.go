package onboarding

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCoordinatorRejectsManagedRegistryCollisions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(config.ManagedProjectEntry, Request) config.ManagedProjectEntry
	}{
		{name: "id", mutate: func(entry config.ManagedProjectEntry, request Request) config.ManagedProjectEntry { return entry }},
		{name: "root", mutate: func(entry config.ManagedProjectEntry, request Request) config.ManagedProjectEntry {
			entry.Root = request.Root
			return entry
		}},
		{name: "session", mutate: func(entry config.ManagedProjectEntry, request Request) config.ManagedProjectEntry {
			entry.AirelaySessionKey = *request.Airelay.SessionKey
			return entry
		}},
		{name: "repository", mutate: func(entry config.ManagedProjectEntry, request Request) config.ManagedProjectEntry {
			entry.RepositoryURL = request.RepositoryURL
			return entry
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoordinatorFixture(t)
			otherBare, otherRoot, _ := testutil.RepoWithBareRemote(t)
			managed := config.EmptyManagedProjectRegistry()
			before, err := managed.Digest()
			if err != nil {
				t.Fatal(err)
			}
			entry := config.ManagedProjectEntry{Root: otherRoot, RepositoryURL: otherBare, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "other_master"}
			if test.name == "id" {
				entry = config.ManagedProjectEntry{Root: fixture.request.Root, RepositoryURL: fixture.request.RepositoryURL, Remote: fixture.request.Remote, DefaultBranch: fixture.request.DefaultBranch, AirelaySessionKey: *fixture.request.Airelay.SessionKey}
			}
			entry = test.mutate(entry, fixture.request)
			managed.Revision = 1
			managed.Projects[map[bool]string{true: fixture.request.ProjectID, false: "other"}[test.name == "id"]] = entry
			if _, err := config.WriteManagedProjectRegistry(fixture.coordinator.StateDir, before, managed); err != nil {
				t.Fatal(err)
			}
			prepareCoordinatorJournalWithCurrentRegistry(t, fixture)
			if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
				t.Fatalf("collision Execute error = %v, want recovery required", err)
			}
		})
	}
}

func TestCoordinatorRejectsManagedMirrorCollision(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	_, otherRoot, _ := testutil.RepoWithBareRemote(t)
	fixture.coordinator.Hub.Config.Projects = map[string]config.ProjectConfig{
		"static": {
			Root:              otherRoot,
			Mirror:            config.ManagedProjectMirrorPath(fixture.coordinator.StateDir, fixture.request.ProjectID),
			Remote:            "origin",
			DefaultBranch:     "main",
			AirelaySessionKey: "static_master",
		},
	}
	prepareCoordinatorJournal(t, fixture)
	if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil || !strings.Contains(err.Error(), "duplicate project mirror") {
		t.Fatalf("mirror collision Execute error = %v, want effective registry mirror collision", err)
	}
}

func TestCoordinatorRejectsProjectCodeCollisionInHub(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	otherRequest := fixture.request
	otherRequest.ProjectID = "other"
	otherRequest.InitialPlan.ProjectID = "other"
	project, plan, identifiers, _, err := buildDurableObjects(otherRequest)
	if err != nil {
		t.Fatal(err)
	}
	identifiers.ProjectCode = fixture.request.ProjectCode
	path := canonicalOnboardingPaths(otherRequest.ProjectID)[2]
	if _, err := fixture.coordinator.Hub.Transact(context.Background(), fixture.base, "seed project code collision", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, identifiers); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = project
	_ = plan
	if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
		t.Fatalf("project code collision Execute error = %v, want recovery required", err)
	}
}
