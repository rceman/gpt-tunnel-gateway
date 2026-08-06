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
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type activationFixture struct {
	coordinator *ActivationCoordinator
	request     Request
	operation   string
	base        string
}

func newActivationFixture(t *testing.T) activationFixture {
	t.Helper()
	baseFixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, baseFixture)
	if _, err := baseFixture.coordinator.Execute(trustedCoordinatorContext(), baseFixture.request, baseFixture.operation); err != nil {
		t.Fatalf("create hub-committed fixture: %v", err)
	}
	coordinator := NewActivationCoordinator(baseFixture.coordinator.Hub)
	return activationFixture{coordinator: coordinator, request: baseFixture.request, operation: baseFixture.operation, base: baseFixture.base}
}

func testActivationHooks(t *testing.T) ActivationHooks {
	t.Helper()
	return ActivationHooks{
		Mirror: func(_ context.Context, p config.ProjectConfig, repositoryURL, _ string) (gitx.MirrorVerification, error) {
			return gitx.MirrorVerification{Path: p.Mirror, RepositoryURL: repositoryURL, Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Created: true}, nil
		},
		ProjectReady: func(_ context.Context, _ Request, _ config.ProjectConfig, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) error {
			if err := model.ValidateProject(project); err != nil {
				return err
			}
			if err := model.ValidatePlan(plan); err != nil {
				return err
			}
			return model.ValidateProjectIdentifiers(identifiers)
		},
		SessionReady: func(_ context.Context, request Request) (SessionProof, error) {
			if !request.Airelay.SessionRequired || request.Airelay.SessionKey == nil {
				return SessionProof{Required: false, Status: "not_required"}, nil
			}
			key := *request.Airelay.SessionKey
			protocol := PositiveInteger(1)
			return SessionProof{Required: true, SessionKey: &key, Status: "active", ControllerProtocolVersion: &protocol}, nil
		},
	}
}

func TestActivationCoordinatorActivatesAndRetriesIdempotently(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	fixture.coordinator.Hooks = hooks
	before, err := fixture.coordinator.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatalf("first activation: %v", err)
	}
	if first.State != StateActivated || first.RegistryBefore == "" || first.RegistryAfter == "" || first.Mirror.Head == "" {
		t.Fatalf("activation result = %+v", first)
	}
	after, err := fixture.coordinator.Hub.RemoteRevision(context.Background())
	if err != nil || before != after {
		t.Fatalf("activation mutated Hub: before=%s after=%s err=%v", before, after, err)
	}
	registry, err := config.LoadManagedProjects(fixture.coordinator.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := registry.Projects[fixture.request.ProjectID]
	if !ok || entry.RepositoryURL != fixture.request.RepositoryURL {
		t.Fatalf("activated registry entry = %#v, registry=%#v", entry, registry)
	}
	receipt, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || receipt.State != StateActivated || receipt.MirrorProof == nil {
		t.Fatalf("activated receipt = %#v, err=%v", receipt, err)
	}
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil {
		t.Fatalf("idempotent activation: %v", err)
	}
	if !second.JournalRepairOnly || second.ReceiptSHA256 != first.ReceiptSHA256 {
		t.Fatalf("idempotent result = %+v", second)
	}
	secondBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("idempotent activation rewrote the activated journal")
	}
}

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
	if err != nil || receipt.State != StateHubCommitted {
		t.Fatalf("journal = %#v, err=%v", receipt, err)
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
