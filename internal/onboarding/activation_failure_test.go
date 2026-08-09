package onboarding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
)

func TestActivationRegistryFailureLeavesHubCommittedAndNoUnrelatedMutation(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	hooks.RegistryWrite = func(string, string, config.ManagedProjectRegistry) (config.ManagedProjectRegistryWriteReceipt, error) {
		return config.ManagedProjectRegistryWriteReceipt{}, errors.New("injected registry failure")
	}
	fixture.coordinator.Hooks = hooks
	result, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
	if result.State != StateRecoveryRequired {
		t.Fatalf("result = %+v, want recovery_required", result)
	}
	registry, err := config.LoadManagedProjects(fixture.coordinator.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Projects) != 0 {
		t.Fatalf("registry changed after injected failure: %#v", registry)
	}
	receipt, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || receipt.State != StateRecoveryRequired {
		t.Fatalf("journal = %#v, err=%v", receipt, err)
	}
	if receipt.Recovery.LastDurableStep == nil || *receipt.Recovery.LastDurableStep != RecoveryStepHubCommitted || receipt.Recovery.SafeCorrectiveAction == nil || *receipt.Recovery.SafeCorrectiveAction != RecoveryActionResumeActivation {
		t.Fatalf("recovery receipt = %#v", receipt.Recovery)
	}
}

func TestActivationMirrorFailurePreservesCommittedRegistryForRecovery(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	hooks.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror failure")
	}
	fixture.coordinator.Hooks = hooks
	result, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
	if result.State != StateRecoveryRequired {
		t.Fatalf("result = %+v, want recovery_required", result)
	}
	registry, err := config.LoadManagedProjects(fixture.coordinator.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Projects[fixture.request.ProjectID]; !ok {
		t.Fatal("registry activation was lost after mirror failure")
	}
}

func TestActivationJournalFailureRetriesWithoutDuplicateRegistryOrHubMutation(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	registryWrites := 0
	hooks.RegistryWrite = func(stateDir, expected string, next config.ManagedProjectRegistry) (config.ManagedProjectRegistryWriteReceipt, error) {
		registryWrites++
		return config.WriteManagedProjectRegistryLocked(stateDir, expected, next)
	}
	journalWrites := 0
	hooks.JournalWrite = func(stateDir string, request Request, receipt Receipt) (HubCommittedJournalWriteReceipt, error) {
		journalWrites++
		if journalWrites == 1 {
			return HubCommittedJournalWriteReceipt{}, errors.New("injected activated journal failure")
		}
		return writeActivatedJournalLocked(stateDir, request, receipt)
	}
	fixture.coordinator.Hooks = hooks
	beforeHub, err := fixture.coordinator.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
	if first.State != StateRecoveryRequired {
		t.Fatalf("first result = %+v", first)
	}
	second, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil || second.State != StateActivated {
		t.Fatalf("retry result = %+v, err=%v", second, err)
	}
	afterHub, err := fixture.coordinator.Hub.RemoteRevision(context.Background())
	if err != nil || beforeHub != afterHub {
		t.Fatalf("activation retry changed Hub: before=%s after=%s err=%v", beforeHub, afterHub, err)
	}
	if registryWrites != 1 || journalWrites != 2 {
		t.Fatalf("writes = registry %d journal %d, want 1 and 2", registryWrites, journalWrites)
	}
}

func TestActivationConcurrentIdenticalCallsProduceOneActivation(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.coordinator.Hooks = testActivationHooks(t)
	results := make([]ActivationResult, 2)
	errorsOut := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errorsOut[i] = fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errorsOut[i] != nil || results[i].State != StateActivated {
			t.Fatalf("concurrent activation[%d] = %+v, err=%v", i, results[i], errorsOut[i])
		}
	}
	if _, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation); err != nil {
		t.Fatal(err)
	}
}

func TestActivationRejectsConflictingRegistryEntry(t *testing.T) {
	fixture := newActivationFixture(t)
	current, err := config.LoadManagedProjects(fixture.coordinator.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	current.Revision = 1
	otherRoot := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(otherRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	current.Projects[fixture.request.ProjectID] = config.ManagedProjectEntry{Root: otherRoot, RepositoryURL: "git@example.invalid:other/repo.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "other_master"}
	if _, err := config.WriteManagedProjectRegistry(fixture.coordinator.StateDir, before, current); err != nil {
		t.Fatal(err)
	}
	fixture.coordinator.Hooks = testActivationHooks(t)
	_, err = fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
}
