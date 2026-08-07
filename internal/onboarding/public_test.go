package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestPublicStatusProjectionRedactsLocalCapabilities(t *testing.T) {
	request := receiptTestRequest(t)
	request.GatewayStateDir = filepath.Join(t.TempDir(), "state")
	request.Root = "/home/secret/project"
	receipt := preparedReceiptForTest(t, request)
	if _, err := WritePreparedJournal(request.GatewayStateDir, request, receipt); err != nil {
		t.Fatalf("write persisted receipt: %v", err)
	}
	orchestrator := NewPublicOrchestrator(hub.Store{Config: config.Config{StateDir: request.GatewayStateDir}})
	projection, err := orchestrator.Status(context.Background(), PublicInput{OperationID: receipt.OperationID, Request: request})
	if err != nil {
		t.Fatalf("status from persisted receipt: %v", err)
	}
	data := string(mustJSONForPublicTest(projection))
	for _, forbidden := range []string{"/home/secret/project", request.GatewayStateDir, "example_master", "mirror_path", "session_key", "repository_url"} {
		if strings.Contains(data, forbidden) {
			t.Fatalf("projection leaked %q: %s", forbidden, data)
		}
	}
}

func TestPublicRecoverRejectsMissingOperationWithoutMutation(t *testing.T) {
	request := receiptTestRequest(t)
	request.GatewayStateDir = filepath.Join(t.TempDir(), "state")
	request.ProjectID = "example"
	request.InitialPlan.ProjectID = request.ProjectID
	store := hub.Store{Config: config.Config{StateDir: request.GatewayStateDir}}
	orchestrator := NewPublicOrchestrator(store)
	_, err := orchestrator.Recover(authority.WithPlanner(context.Background()), PublicInput{
		OperationID: "11111111-1111-1111-1111-111111111111", Request: request,
	})
	if err == nil || !strings.Contains(err.Error(), ErrOnboardingRecoveryRequired.Error()) {
		t.Fatalf("missing operation error = %v, want recovery-required", err)
	}
	if !errors.Is(err, ErrPreparedJournalNotFound) && !strings.Contains(err.Error(), ErrPreparedJournalNotFound.Error()) {
		t.Fatalf("missing operation error lost not-found cause: %v", err)
	}
}

func newPublicTestFixture(t *testing.T) (coordinatorFixture, *PublicOrchestrator) {
	t.Helper()
	fixture := newCoordinatorFixture(t)
	fixture.request.RepositoryURL = fixture.bare
	testutil.Git(t, fixture.request.Root, "remote", "set-head", "origin", "-a")
	airelay := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nif [ \"$1\" = session-status ]; then\n  printf 'Controller: reachable\\nProtocol version: 1\\nState: idle\\n'\n  exit 0\nfi\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.coordinator.Hub.Config.AirelayCommand = airelay
	fixture.coordinator.Hub.Config.DispatchTimeoutSeconds = 5
	orchestrator := NewPublicOrchestrator(fixture.coordinator.Hub)
	orchestrator.Activation.Hooks = testActivationHooks(t)
	return fixture, orchestrator
}

func TestPublicOnboardFreshSuccessAndReplayUsePersistedResult(t *testing.T) {
	fixture, orchestrator := newPublicTestFixture(t)
	input := PublicInput{OperationID: fixture.operation, Request: fixture.request}
	ctx := authority.WithPlanner(context.Background())
	first, err := orchestrator.Onboard(ctx, input)
	if err != nil {
		t.Fatalf("fresh public onboarding: %v", err)
	}
	if first.State != StateActivated || first.RequestSHA256 == "" || first.ReceiptSHA256 == "" || first.RecoveryStatus != string(RecoveryNotRequired) {
		t.Fatalf("fresh public result = %#v", first)
	}
	receipt, err := LoadOnboardingJournal(fixture.request.GatewayStateDir, fixture.operation)
	if err != nil || receipt.State != StateActivated {
		t.Fatalf("persisted public receipt = %#v, err=%v", receipt, err)
	}
	replay, err := orchestrator.Onboard(ctx, input)
	if err != nil {
		t.Fatalf("public onboarding replay: %v", err)
	}
	if replay.State != StateActivated || !replay.JournalRepairOnly || replay.ReceiptSHA256 != first.ReceiptSHA256 || replay.RequestSHA256 != first.RequestSHA256 {
		t.Fatalf("public replay result = %#v", replay)
	}
}

func TestPublicOnboardConcurrentIdenticalCallsHaveOneDurableResult(t *testing.T) {
	fixture, orchestrator := newPublicTestFixture(t)
	input := PublicInput{OperationID: fixture.operation, Request: fixture.request}
	ctx := authority.WithPlanner(context.Background())
	results := make([]PublicResult, 2)
	errorsOut := make([]error, 2)
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			results[index], errorsOut[index] = orchestrator.Onboard(ctx, input)
		}(i)
	}
	group.Wait()
	for i := range results {
		if errorsOut[i] != nil || results[i].State != StateActivated || results[i].ReceiptSHA256 == "" {
			t.Fatalf("concurrent result %d = %#v, err=%v", i, results[i], errorsOut[i])
		}
	}
	if results[0].ReceiptSHA256 != results[1].ReceiptSHA256 {
		t.Fatalf("concurrent receipt digests differ: %#v", results)
	}
}

func TestPublicOnboardConflictingRequestPreservesWinner(t *testing.T) {
	fixture, orchestrator := newPublicTestFixture(t)
	input := PublicInput{OperationID: fixture.operation, Request: fixture.request}
	ctx := authority.WithPlanner(context.Background())
	winner, err := orchestrator.Onboard(ctx, input)
	if err != nil || winner.State != StateActivated {
		t.Fatalf("winner onboarding = %#v, err=%v", winner, err)
	}
	path, err := PreparedJournalPath(fixture.request.GatewayStateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	beforeReceipt, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeHub, err := fixture.coordinator.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	conflicting := fixture.request
	conflicting.InitialPlan.Summary += " conflicting request"
	_, err = orchestrator.Onboard(ctx, PublicInput{OperationID: fixture.operation, Request: conflicting})
	if err == nil || !strings.Contains(err.Error(), OnboardingOperationConflict) {
		t.Fatalf("conflicting onboarding error = %v", err)
	}
	afterReceipt, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeReceipt, afterReceipt) {
		t.Fatal("conflicting onboarding changed winner receipt")
	}
	afterHub, err := fixture.coordinator.Hub.RemoteRevision(context.Background())
	if err != nil || afterHub != beforeHub {
		t.Fatalf("conflicting onboarding changed Hub: before=%s after=%s err=%v", beforeHub, afterHub, err)
	}
}

func TestPublicOnboardRecoveryAfterMirrorFailure(t *testing.T) {
	fixture, orchestrator := newPublicTestFixture(t)
	hooks := testActivationHooks(t)
	hooks.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror failure")
	}
	orchestrator.Activation.Hooks = hooks
	input := PublicInput{OperationID: fixture.operation, Request: fixture.request}
	ctx := authority.WithPlanner(context.Background())
	if _, err := orchestrator.Onboard(ctx, input); err == nil || !strings.Contains(err.Error(), OnboardingRecoveryRequired) {
		t.Fatalf("mirror failure did not require recovery: %v", err)
	}
	recovery, err := LoadOnboardingJournal(fixture.request.GatewayStateDir, fixture.operation)
	if err != nil || recovery.State != StateRecoveryRequired {
		t.Fatalf("recovery receipt = %#v, err=%v", recovery, err)
	}
	orchestrator.Activation.Hooks = testActivationHooks(t)
	result, err := orchestrator.Recover(ctx, input)
	if err != nil || result.State != StateActivated {
		t.Fatalf("recovered public onboarding = %#v, err=%v", result, err)
	}
}

func mustJSONForPublicTest(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
