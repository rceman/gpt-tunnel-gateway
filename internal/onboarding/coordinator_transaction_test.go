package onboarding

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

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

func TestCoordinatorConcurrentAllPathTamperBeforeJournalRequiresRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	originalHook := beforeOnboardingJournalHook
	defer func() { beforeOnboardingJournalHook = originalHook }()
	beforeOnboardingJournalHook = func(ctx context.Context, store hub.Store, transaction hub.TransactionResult, projectID string) error {
		project, plan, identifiers, _, err := buildDurableObjects(fixture.request)
		if err != nil {
			return err
		}
		project.RepositoryURL = "git@example.invalid:owner/tampered.git"
		plan.Summary = "tampered after transaction"
		identifiers.ProjectCode = "ZZZ"
		paths := canonicalOnboardingPaths(projectID)
		_, err = store.Transact(ctx, transaction.After, "concurrent all-path tamper", func(worktree string) ([]string, error) {
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
		return err
	}
	result, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
		t.Fatalf("all-path tamper error = %v, result=%+v, want recovery required", err, result)
	}
	if result.State != StateRecoveryRequired || result.Hub.After == "" {
		t.Fatalf("all-path tamper result = %+v, want recovery evidence", result)
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
