package onboarding

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	return coordinatorFixture{
		coordinator: coordinator,
		request:     request,
		operation:   operation,
		bare:        bare,
		base:        base,
	}
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
	return authority.WithPlanner(context.Background())
}

func TestCoordinatorRequiresTrustedAuthorityBeforeJournalAccess(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	_, err := fixture.coordinator.Execute(context.Background(), fixture.request, fixture.operation)
	if err == nil || !strings.Contains(err.Error(), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("Execute error = %v, want AUTHORITY_UNAVAILABLE", err)
	}
}

func requireCoordinatorErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var coordinatorErr *CoordinatorError
	if err == nil || !errors.As(err, &coordinatorErr) || coordinatorErr.Code != code {
		t.Fatalf("error = %v, want coordinator code %s", err, code)
	}
}

func TestCoordinatorMissingJournalReturnsTypedRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	_, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
}

func TestCoordinatorMalformedJournalReturnsTypedRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	writeJournalRaw(t, path, []byte("{\n"))
	_, err = fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
}

func TestCoordinatorSameIdentityPreparedValidationFailureReturnsTypedRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	receipt, err := LoadPreparedJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	receipt.RepositoryProof.Root = filepath.Join(t.TempDir(), "different-root")
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	writeJournalRaw(t, path, journalCanonicalFileBytes(t, receipt))
	_, err = fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
}

func TestCoordinatorSameIdentityCommittedValidationFailureReturnsTypedRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	prepareCoordinatorJournal(t, fixture)
	if _, err := fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation); err != nil {
		t.Fatal(err)
	}
	receipt, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	receipt.CreatedIdentifiers.ProjectCode = "XYZ"
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	writeJournalRaw(t, path, journalCanonicalFileBytes(t, receipt))
	_, err = fixture.coordinator.Execute(trustedCoordinatorContext(), fixture.request, fixture.operation)
	requireCoordinatorErrorCode(t, err, OnboardingRecoveryRequired)
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

func pathsForTest(projectID string) []string { return canonicalOnboardingPaths(projectID) }
