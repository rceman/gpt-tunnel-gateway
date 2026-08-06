package onboarding

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	request.Airelay.SessionRequired = false
	request.Airelay.SessionKey = nil
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
	if second.HubTransaction || second.JournalRepairOnly || second.Hub.After != "" {
		t.Fatalf("idempotent result = %+v, want no second Hub transaction", second)
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
	if !first.HubTransaction || first.Hub.After == "" {
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
