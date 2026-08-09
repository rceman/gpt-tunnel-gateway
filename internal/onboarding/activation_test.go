package onboarding

import (
	"context"
	"errors"
	"os"
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
	return activationFixture{
		coordinator: coordinator,
		request:     baseFixture.request,
		operation:   baseFixture.operation,
		base:        baseFixture.base,
	}
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
				return SessionProof{
					Required: false,
					Status:   "not_required",
				}, nil
			}
			key := *request.Airelay.SessionKey
			protocol := PositiveInteger(1)
			return SessionProof{
				Required:                  true,
				SessionKey:                &key,
				Status:                    "active",
				ControllerProtocolVersion: &protocol,
			}, nil
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
	receipt, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	if result.RegistryBefore != receipt.RegistryDigests.ManagedBeforeSHA256 || result.RegistryAfter != receipt.RegistryDigests.ManagedAfterSHA256 || result.RegistryBefore == result.RegistryAfter {
		t.Fatalf("activated replay registry proof = before=%q after=%q receipt=%#v", result.RegistryBefore, result.RegistryAfter, receipt.RegistryDigests)
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
