package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

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

func TestActivationRecoveryClampsBackwardClockToCommittedReceipt(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	hooks.Now = func() time.Time { return time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC) }
	hooks.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror outage")
	}
	fixture.coordinator.Hooks = hooks
	_, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
	recovered, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != StateRecoveryRequired {
		t.Fatalf("recovery journal state = %q, want recovery_required", recovered.State)
	}
	committed, err := parseReceiptTime(*recovered.Timestamps.HubCommittedAt)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := parseReceiptTime(recovered.Timestamps.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Before(committed) {
		t.Fatalf("recovery updated_at=%s precedes hub_committed_at=%s", updated, committed)
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
	fixture := activationFixture{
		coordinator: NewActivationCoordinator(baseFixture.coordinator.Hub),
		request:     baseFixture.request,
		operation:   baseFixture.operation,
		base:        baseFixture.base,
	}
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
