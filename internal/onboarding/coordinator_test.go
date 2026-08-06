package onboarding

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

type coordinatorFixture struct {
	coordinator *Coordinator
	request     Request
	operation   string
	bare        string
	base        string
}

func newCoordinatorFixture(t *testing.T) coordinatorFixture {
	t.Helper()
	bare, projectRoot, base := testutil.RepoWithBareRemote(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	request := receiptTestRequest(t)
	request.ProjectID = "example"
	request.InitialPlan.ProjectID = request.ProjectID
	request.Root = projectRoot
	request.Remote = "origin"
	request.RepositoryURL = "git@example.invalid:owner/example.git"
	request.DefaultBranch = "main"
	request.ProjectCode = "EXM"
	request.GatewayStateDir = stateDir
	request.ExpectedHubRevision = base
	request.Workflow = nil
	request.Airelay.SessionRequired = true
	sessionKey := "example_master"
	request.Airelay.SessionKey = &sessionKey
	operation := "22222222-2222-2222-2222-222222222222"
	store := hub.Store{Config: config.Config{
		StateDir:     stateDir,
		MaxReadBytes: 1 << 20,
		MaxListItems: 1000,
		Hub:          config.HubConfig{RepositoryURL: bare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
	}}
	coordinator := NewCoordinator(store)
	if err := coordinator.Hub.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure hub: %v", err)
	}
	return coordinatorFixture{coordinator: coordinator, request: request, operation: operation, bare: bare, base: base}
}

func prepareCoordinatorJournal(t *testing.T, fixture coordinatorFixture) {
	t.Helper()
	project, plan, identifiers, digests, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatalf("build durable objects: %v", err)
	}
	_ = project
	_ = plan
	_ = identifiers
	receipt := preparedReceiptForTest(t, fixture.request)
	receipt.OperationID = fixture.operation
	managed := config.EmptyManagedProjectRegistry()
	beforeDigest, err := managed.Digest()
	if err != nil {
		t.Fatalf("managed registry before digest: %v", err)
	}
	managed.Revision = 1
	managed.Projects[fixture.request.ProjectID] = config.ManagedProjectEntry{Root: fixture.request.Root, RepositoryURL: fixture.request.RepositoryURL, Remote: fixture.request.Remote, DefaultBranch: fixture.request.DefaultBranch, AirelaySessionKey: *fixture.request.Airelay.SessionKey}
	afterDigest, err := managed.Digest()
	if err != nil {
		t.Fatalf("managed registry after digest: %v", err)
	}
	receipt.RegistryDigests.ManagedBeforeSHA256 = beforeDigest
	receipt.RegistryDigests.ManagedAfterSHA256 = afterDigest
	receipt.RegistryDigests.ProjectSHA256 = digests.project
	receipt.RegistryDigests.PlanSHA256 = digests.plan
	receipt.RegistryDigests.IdentifiersSHA256 = digests.identifiers
	if _, err := WritePreparedJournal(fixture.coordinator.StateDir, fixture.request, receipt); err != nil {
		t.Fatalf("write prepared journal: %v", err)
	}
}

func trustedCoordinatorContext() context.Context {
	return service.WithPlannerWorkflowPolicyAuthority(context.Background())
}

func TestCoordinatorRequiresTrustedAuthorityBeforeJournalAccess(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	_, err := fixture.coordinator.Execute(context.Background(), fixture.request, fixture.operation)
	if err == nil || !strings.Contains(err.Error(), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("Execute error = %v, want AUTHORITY_UNAVAILABLE", err)
	}
}

func TestCoordinatorCommitsExactlyThreeObjectsAndRetriesIdempotently(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	first, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if !first.HubTransaction || first.Hub.Paths == nil || len(first.Hub.Paths) != 3 {
		t.Fatalf("first result = %+v, want one three-path transaction", first)
	}
	if first.Hub.Before != fixture.base || first.Hub.After == fixture.base {
		t.Fatalf("hub transaction = %+v, want base to a new revision", first.Hub)
	}
	for _, path := range canonicalOnboardingPaths(fixture.request.ProjectID) {
		if _, err := fixture.coordinator.Hub.ReadFile(context.Background(), path); err != nil {
			t.Fatalf("read committed %s: %v", path, err)
		}
	}
	second, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatalf("idempotent Execute: %v", err)
	}
	if second.HubTransaction || second.JournalRepairOnly || second.Hub.After == "" || len(second.Hub.Paths) != 3 {
		t.Fatalf("idempotent result = %+v, want recorded committed Hub evidence without a second transaction", second)
	}
	receipt, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || receipt.State != StateHubCommitted {
		t.Fatalf("committed journal = %+v, err=%v", receipt, err)
	}
}

func TestCoordinatorReconcilesExactObjectsWithoutSecondTransaction(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	project, plan, identifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Hub.Transact(context.Background(), fixture.base, "seed exact onboarding objects", func(worktree string) ([]string, error) {
		paths := canonicalOnboardingPaths(fixture.request.ProjectID)
		if err := hub.WriteJSON(worktree, paths[0], project); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[1], plan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[2], identifiers); err != nil {
			return nil, err
		}
		return paths, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatalf("reconcile Execute: %v", err)
	}
	if !result.JournalRepairOnly || result.HubTransaction {
		t.Fatalf("reconcile result = %+v, want journal-only repair", result)
	}
}

func TestCoordinatorReconciliationUsesCommonLastChangeNotRemoteTip(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	project, plan, identifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	paths := canonicalOnboardingPaths(fixture.request.ProjectID)
	targetTransaction, err := fixture.coordinator.Hub.Transact(context.Background(), fixture.base, "seed onboarding objects", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, paths[0], project); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[1], plan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[2], identifiers); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Hub.Transact(context.Background(), targetTransaction.After, "unrelated hub change", func(worktree string) ([]string, error) {
		if err := hub.WriteText(worktree, "unrelated.txt", "unrelated\n"); err != nil {
			return nil, err
		}
		return []string{"unrelated.txt"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatalf("reconcile after unrelated commit: %v", err)
	}
	if !result.JournalRepairOnly || result.Hub.After != targetTransaction.After {
		t.Fatalf("result = %+v, want common target last-change %s", result, targetTransaction.After)
	}
}

func TestCoordinatorReplayRejectsTamperedCommittedObjects(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	first, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	project, _, _, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	project.RepositoryURL = "git@example.invalid:owner/tampered.git"
	path := canonicalOnboardingPaths(fixture.request.ProjectID)[0]
	if _, err := fixture.coordinator.Hub.Transact(context.Background(), first.Hub.After, "tamper onboarding project", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, project); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
		t.Fatalf("tampered replay error = %v, want recovery required", err)
	}
}

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

func TestCoordinatorRejectsManagedRegistryDigestDrift(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	otherBare, otherRoot, _ := testutil.RepoWithBareRemote(t)
	managed := config.EmptyManagedProjectRegistry()
	before, err := managed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	managed.Revision = 1
	managed.Projects["other"] = config.ManagedProjectEntry{Root: otherRoot, RepositoryURL: otherBare, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "other_master"}
	if _, err := config.WriteManagedProjectRegistry(fixture.coordinator.StateDir, before, managed); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil || !strings.Contains(err.Error(), "managed registry") {
		t.Fatalf("registry drift Execute error = %v, want managed registry rejection", err)
	}
}

func prepareCoordinatorJournalWithCurrentRegistry(t *testing.T, fixture coordinatorFixture) {
	t.Helper()
	project, plan, identifiers, digests, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	current, err := config.LoadManagedProjects(fixture.coordinator.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := preparedReceiptForTest(t, fixture.request)
	receipt.OperationID = fixture.operation
	receipt.RegistryDigests.ManagedBeforeSHA256 = before
	receipt.RegistryDigests.ManagedAfterSHA256 = strings.Repeat("e", 64)
	receipt.RegistryDigests.ProjectSHA256 = digests.project
	receipt.RegistryDigests.PlanSHA256 = digests.plan
	receipt.RegistryDigests.IdentifiersSHA256 = digests.identifiers
	_ = project
	_ = plan
	_ = identifiers
	if _, err := WritePreparedJournal(fixture.coordinator.StateDir, fixture.request, receipt); err != nil {
		t.Fatal(err)
	}
}

func TestHubCommittedReceiptRejectsForgedIdentifierCounters(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	prepared, err := LoadPreparedJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	project, plan, identifiers, _, err := buildDurableObjects(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Repeat("f", 40)
	committed := committedReceipt(prepared, fixture.request, after, project, plan, identifiers, true)
	committed.CreatedIdentifiers.NextTaskNumber = 2
	if err := ValidateHubCommittedReceiptIntrinsic(committed); err == nil || !strings.Contains(err.Error(), "counters must both equal 1") {
		t.Fatalf("forged counters validation error = %v", err)
	}
}

func TestCoordinatorJournalFailureReconcilesOnExactRetry(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	original := writeOnboardingJournalAtomic
	writeOnboardingJournalAtomic = func(string, []byte, fs.FileMode) error { return errors.New("injected journal write failure") }
	defer func() { writeOnboardingJournalAtomic = original }()
	first, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
		t.Fatalf("first result err = %v, want recovery required", err)
	}
	if !first.HubTransaction || first.State != StateRecoveryRequired || first.Hub.After == "" {
		t.Fatalf("failed journal result must report committed Hub transaction for recovery: %+v", first)
	}
	writeOnboardingJournalAtomic = original
	second, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatalf("retry Execute: %v", err)
	}
	if !second.JournalRepairOnly || second.HubTransaction {
		t.Fatalf("retry result = %+v, want journal-only reconciliation", second)
	}
}

func TestCoordinatorSerializesIdenticalConcurrentCalls(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	results := make([]Result, 2)
	errorsOut := make([]error, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errorsOut[index] = fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
		}(index)
	}
	wg.Wait()
	transactions := 0
	for index := range results {
		if errorsOut[index] != nil {
			t.Fatalf("concurrent Execute[%d]: %v", index, errorsOut[index])
		}
		if results[index].HubTransaction {
			transactions++
		}
	}
	if transactions != 1 {
		t.Fatalf("concurrent transactions = %d, want exactly one", transactions)
	}
}

func TestCoordinatorRejectsConflictingOperationJournal(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	conflicting := fixture.request
	conflicting.ProjectCode = "XYZ"
	_, err := fixture.coordinator.Execute(trustedCoordinatorContext(), conflicting, fixture.operation)
	if err == nil || !strings.Contains(err.Error(), OnboardingOperationConflict) {
		t.Fatalf("conflicting Execute error = %v, want operation conflict", err)
	}
}

func TestManagedProjectsLockRetriesWithContextBound(t *testing.T) {
	stateDir := t.TempDir()
	held, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "managed-projects")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := acquireManagedProjectsLock(ctx, stateDir); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended managed lock error = %v, want context deadline", err)
	}
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

func pathsForTest(projectID string) []string { return canonicalOnboardingPaths(projectID) }
