package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
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

func TestActivatedRetryReturnsWithoutLocalActivationOrRecovery(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.coordinator.Hooks = testActivationHooks(t)
	if _, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	hooks := ActivationHooks{
		Mirror: func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
			calls["mirror"]++
			return gitx.MirrorVerification{}, errors.New("activated retry must not reconcile mirror")
		},
		ProjectReady: func(context.Context, Request, config.ProjectConfig, model.Project, model.Plan, model.ProjectIdentifiers) error {
			calls["project"]++
			return errors.New("activated retry must not rerun project readiness")
		},
		SessionReady: func(context.Context, Request) (SessionProof, error) {
			calls["session"]++
			return SessionProof{}, errors.New("activated retry must not rerun Airelay readiness")
		},
		JournalWrite: func(string, Request, Receipt) (HubCommittedJournalWriteReceipt, error) {
			calls["journal"]++
			return HubCommittedJournalWriteReceipt{}, errors.New("activated retry must not rewrite journal")
		},
		RecoveryWrite: func(string, Request, Receipt) (HubCommittedJournalWriteReceipt, error) {
			calls["recovery"]++
			return HubCommittedJournalWriteReceipt{}, errors.New("activated retry must not downgrade recovery")
		},
	}
	fixture.coordinator.Hooks = hooks
	result, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil || result.State != StateActivated || !result.JournalRepairOnly {
		t.Fatalf("activated retry = %+v, err=%v", result, err)
	}
	for name, count := range calls {
		if count != 0 {
			t.Fatalf("activated retry invoked %s hook %d times", name, count)
		}
	}

	if err := os.WriteFile(config.ManagedProjectRegistryPath(fixture.coordinator.StateDir), []byte("{\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
	if result.State != StateActivated || calls["recovery"] != 0 {
		t.Fatalf("activated verification failure downgraded: result=%+v calls=%v", result, calls)
	}
	final, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || final.State != StateActivated {
		t.Fatalf("activated journal after verification failure = %#v, err=%v", final, err)
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

func TestActivationRecoveryResumesFromPersistedReceiptAfterCoordinatorRestart(t *testing.T) {
	fixture := newActivationFixture(t)
	failing := testActivationHooks(t)
	failing.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror outage")
	}
	fixture.coordinator.Hooks = failing
	_, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
	recovered, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || recovered.State != StateRecoveryRequired {
		t.Fatalf("recovery journal = %#v, err=%v", recovered, err)
	}

	restarted := NewActivationCoordinator(fixture.coordinator.Hub)
	restarted.Hooks = testActivationHooks(t)
	result, err := restarted.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	if err != nil || result.State != StateActivated {
		t.Fatalf("restart activation = %+v, err=%v", result, err)
	}
	final, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || final.State != StateActivated {
		t.Fatalf("final journal = %#v, err=%v", final, err)
	}
}

func TestActivationConcurrentRecoveryCallsRemainTypedAndUnrelatedJournalIsUntouched(t *testing.T) {
	fixture := newActivationFixture(t)
	other := preparedReceiptForTest(t, fixture.request)
	other.OperationID = "33333333-3333-3333-3333-333333333333"
	if _, err := WritePreparedJournal(fixture.coordinator.StateDir, fixture.request, other); err != nil {
		t.Fatal(err)
	}
	failing := testActivationHooks(t)
	failing.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror outage")
	}
	fixture.coordinator.Hooks = failing
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
		requireCoordinatorErrorCode(t, errorsOut[i], OnboardingRecoveryRequired)
		if results[i].State != StateRecoveryRequired {
			t.Fatalf("concurrent recovery[%d] = %+v", i, results[i])
		}
	}
	untouched, err := LoadOnboardingJournal(fixture.coordinator.StateDir, other.OperationID)
	if err != nil || untouched.State != StatePrepared {
		t.Fatalf("unrelated journal = %#v, err=%v", untouched, err)
	}
}

func TestActivationRecoveryPreservesUnrelatedRegistryRecordByteForByte(t *testing.T) {
	baseFixture := newCoordinatorFixture(t)
	otherBare, otherRoot, _ := testutil.RepoWithBareRemote(t)
	current, err := config.LoadManagedProjects(baseFixture.coordinator.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	managed := config.EmptyManagedProjectRegistry()
	managed.Revision = 1
	managed.Projects["unrelated"] = config.ManagedProjectEntry{Root: otherRoot, RepositoryURL: otherBare, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "unrelated_master"}
	if _, err := config.WriteManagedProjectRegistry(baseFixture.coordinator.StateDir, beforeDigest, managed); err != nil {
		t.Fatal(err)
	}
	registryBefore, err := config.LoadManagedProjects(baseFixture.coordinator.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	registryBeforeDigest, err := registryBefore.Digest()
	if err != nil {
		t.Fatal(err)
	}
	project, plan, identifiers, digests, err := buildDurableObjects(baseFixture.request)
	_ = project
	_ = plan
	_ = identifiers
	if err != nil {
		t.Fatal(err)
	}
	receipt := preparedReceiptForTest(t, baseFixture.request)
	receipt.OperationID = baseFixture.operation
	receipt.RegistryDigests.ManagedBeforeSHA256 = registryBeforeDigest
	managed.Revision++
	managed.Projects[baseFixture.request.ProjectID] = managedEntryForRequest(baseFixture.request)
	afterDigest, err := managed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.RegistryDigests.ManagedAfterSHA256 = afterDigest
	receipt.RegistryDigests.ProjectSHA256 = digests.project
	receipt.RegistryDigests.PlanSHA256 = digests.plan
	receipt.RegistryDigests.IdentifiersSHA256 = digests.identifiers
	if _, err := WritePreparedJournal(baseFixture.coordinator.StateDir, baseFixture.request, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := baseFixture.coordinator.Execute(trustedCoordinatorContext(), baseFixture.request, baseFixture.operation); err != nil {
		t.Fatal(err)
	}
	fixture := activationFixture{coordinator: NewActivationCoordinator(baseFixture.coordinator.Hub), request: baseFixture.request, operation: baseFixture.operation, base: baseFixture.base}
	registryPath := config.ManagedProjectRegistryPath(fixture.coordinator.StateDir)
	entryBytes := func() []byte {
		t.Helper()
		data, err := os.ReadFile(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		var projects map[string]json.RawMessage
		if err := json.Unmarshal(object["projects"], &projects); err != nil {
			t.Fatal(err)
		}
		return append([]byte(nil), projects["unrelated"]...)
	}
	beforeEntry := entryBytes()
	failing := testActivationHooks(t)
	failing.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror outage")
	}
	fixture.coordinator.Hooks = failing
	if _, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	if afterFailure := entryBytes(); string(afterFailure) != string(beforeEntry) {
		t.Fatalf("unrelated registry entry changed after recovery: before=%s after=%s", beforeEntry, afterFailure)
	}
	restarted := NewActivationCoordinator(fixture.coordinator.Hub)
	restarted.Hooks = testActivationHooks(t)
	if _, err := restarted.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation); err != nil {
		t.Fatal(err)
	}
	if afterRetry := entryBytes(); string(afterRetry) != string(beforeEntry) {
		t.Fatalf("unrelated registry entry changed after retry: before=%s after=%s", beforeEntry, afterRetry)
	}
}

func TestActivationRejectsNonCanonicalMirrorAndPersistsRecovery(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	hooks.Mirror = func(_ context.Context, _ config.ProjectConfig, repositoryURL, _ string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{Path: filepath.Join(t.TempDir(), "wrong.git"), RepositoryURL: repositoryURL, Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
	}
	fixture.coordinator.Hooks = hooks
	_, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
	receipt, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil || receipt.State != StateRecoveryRequired || receipt.MirrorProof != nil {
		t.Fatalf("non-canonical mirror recovery = %#v, err=%v", receipt, err)
	}
}

func TestActivatedJournalRequiresPriorHubCommittedJournalAndPreservesEvidence(t *testing.T) {
	fixture := newActivationFixture(t)
	prior, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	activated := prior
	activated.State = StateActivated
	activated.MirrorProof = &MirrorProof{Path: config.ManagedProjectMirrorPath(fixture.coordinator.StateDir, fixture.request.ProjectID), RepositoryURL: fixture.request.RepositoryURL, Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	activatedAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	activated.Timestamps.ActivatedAt = &activatedAt
	activated.Timestamps.UpdatedAt = activatedAt
	activated.Recovery = Recovery{Status: RecoveryNotRequired}
	if _, err := writeActivatedJournalLocked(fixture.coordinator.StateDir, fixture.request, activated); !errors.Is(err, ErrPreparedJournalNotFound) {
		t.Fatalf("missing prior journal error = %v, want ErrPreparedJournalNotFound", err)
	}
}

func TestRecoveryEvidenceCannotMoveBackward(t *testing.T) {
	fixture := newActivationFixture(t)
	prior, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	step := RecoveryStepManagedMirror
	lastState := StateHubCommitted
	reason := "mirror later failed"
	action := RecoveryActionResumeActivation
	prior.State = StateRecoveryRequired
	prior.MirrorProof = &MirrorProof{Path: config.ManagedProjectMirrorPath(fixture.coordinator.StateDir, fixture.request.ProjectID), RepositoryURL: fixture.request.RepositoryURL, Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	prior.Recovery = Recovery{Status: RecoveryRequired, LastCompletedState: &lastState, LastDurableStep: &step, Reason: &reason, SafeCorrectiveAction: &action}
	updated := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	prior.Timestamps.UpdatedAt = updated
	if _, err := writeRecoveryJournalLocked(fixture.coordinator.StateDir, fixture.request, prior); err != nil {
		t.Fatal(err)
	}
	backward := prior
	backwardStep := RecoveryStepHubCommitted
	backward.Recovery.LastDurableStep = &backwardStep
	backward.Recovery.Reason = receiptTestString("earlier failure")
	if _, err := writeRecoveryJournalLocked(fixture.coordinator.StateDir, fixture.request, backward); err == nil {
		t.Fatal("backward recovery transition unexpectedly succeeded")
	}
}

func TestRecoveryJournalMissingDurableStepFailsClosed(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	hooks.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror outage")
	}
	fixture.coordinator.Hooks = hooks
	if _, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	var recovery map[string]json.RawMessage
	if err := json.Unmarshal(object["recovery"], &recovery); err != nil {
		t.Fatal(err)
	}
	delete(recovery, "last_durable_step")
	object["recovery"], err = json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	corrupt, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(corrupt, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation); err == nil {
		t.Fatal("corrupt recovery journal unexpectedly loaded")
	}
}

func TestDefaultSessionReadinessRequiresExplicitHealthyStateAndProtocol(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.coordinator.Airelay.Timeout = time.Second
	cases := []struct {
		state, protocol string
		exit            int
		wantErr         bool
	}{
		{state: "running", protocol: "2"},
		{state: "waiting", protocol: "2"},
		{state: "idle", protocol: "2"},
		{state: "error", protocol: "2", wantErr: true},
		{state: "unknown", protocol: "2", wantErr: true},
		{state: "idle", protocol: "", wantErr: true},
		{state: "idle", protocol: "0", wantErr: true},
		{state: "idle", protocol: "2", exit: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s-%s-exit-%d", tc.state, tc.protocol, tc.exit), func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "airelay-status.sh")
			protocolLine := ""
			if tc.protocol != "" {
				protocolLine = fmt.Sprintf("printf 'Protocol version: %s\\n'\n", tc.protocol)
			}
			body := fmt.Sprintf("#!/bin/sh\nprintf 'Controller: reachable\\n'\nprintf 'State: %s\\n'\n%sexit %d\n", tc.state, protocolLine, tc.exit)
			if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			fixture.coordinator.Airelay.Command = script
			_, err := fixture.coordinator.defaultSessionReadiness(context.Background(), fixture.request)
			if (err != nil) != tc.wantErr {
				t.Fatalf("readiness error = %v, wantErr=%t", err, tc.wantErr)
			}
		})
	}
}
