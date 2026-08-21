package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestWorkProgressAdvancesPostGateBaselineAndNextCallUsesDelta(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	configureGoCheckpoint(t, s, revision)
	root := s.Config.Projects["example"].Root
	path := filepath.Join(root, "docs", "progress.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.workCheckpointExecutor = func(_ context.Context, _ string, _ string, changed []string, names []string) ([]model.CompletionGateResult, error) {
		if len(changed) == 0 {
			t.Fatal("checkpoint adapter received no changed files")
		}
		calls++
		if err := os.WriteFile(path, []byte("after-format\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return fakeReceiptResults(names), nil
	}
	first, err := s.WorkCheckpoint(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"})
	if err != nil || first.Status != "completed" || !first.BaselineAdvanced {
		t.Fatalf("first progress=%#v err=%v", first, err)
	}
	state, err := readWorkProgressState(workCheckpointStatePath(s.Config.StateDir, root, "example"))
	if err != nil || state.Baseline["docs/progress.md"] == "" || state.GateIdentity == "" {
		t.Fatalf("baseline state=%#v err=%v", state, err)
	}
	second, err := s.WorkCheckpoint(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"})
	if err != nil || second.Status != "completed" || !second.Reused || second.BaselineAdvanced || len(second.ChangedFiles) != 0 {
		t.Fatalf("second progress=%#v err=%v", second, err)
	}
	if calls != 1 {
		t.Fatalf("gates executed %d times after baseline advanced", calls)
	}
	status, err := s.WorkCheckpointStatus(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"})
	if err != nil || !status.BaselinePresent || len(status.ChangedFiles) != 0 || len(status.GateNames) == 0 {
		t.Fatalf("checkpoint status=%#v err=%v", status, err)
	}
}

func TestWorkProgressFailureDoesNotAdvanceBaseline(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	configureGoCheckpoint(t, s, revision)
	root := s.Config.Projects["example"].Root
	path := filepath.Join(root, "docs", "progress-failure.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fail := true
	calls := 0
	s.workCheckpointExecutor = func(_ context.Context, _ string, _ string, changed []string, names []string) ([]model.CompletionGateResult, error) {
		if len(changed) == 0 {
			t.Fatal("checkpoint adapter received no changed files")
		}
		calls++
		if fail {
			return nil, errors.New("gate failed")
		}
		return fakeReceiptResults(names), nil
	}
	first, err := s.WorkCheckpoint(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"})
	if err == nil || first.Status != "failed" || first.BaselineAdvanced {
		t.Fatalf("failed progress=%#v err=%v", first, err)
	}
	state, stateErr := readWorkProgressState(workCheckpointStatePath(s.Config.StateDir, root, "example"))
	if stateErr != nil || len(state.Baseline) != 0 {
		t.Fatalf("failed progress advanced baseline=%#v err=%v", state, stateErr)
	}
	fail = false
	second, err := s.WorkCheckpoint(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"})
	if err != nil || second.Status != "completed" || !second.BaselineAdvanced || calls != 2 {
		t.Fatalf("retry progress=%#v err=%v calls=%d", second, err, calls)
	}
}

func TestWorkCheckpointRequiresExplicitProjectAdapter(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	path := filepath.Join(root, "docs", "unconfigured.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WorkCheckpoint(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"}); err == nil {
		t.Fatal("checkpoint used an implicit adapter")
	}
}

func TestWorkCheckpointBusyProjectRootIsSingleFlight(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	configureGoCheckpoint(t, s, revision)
	root := s.Config.Projects["example"].Root
	path := filepath.Join(root, "docs", "single-flight.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s.workCheckpointExecutor = func(_ context.Context, _ string, _ string, _ []string, names []string) ([]model.CompletionGateResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return fakeReceiptResults(names), nil
	}
	firstResult := make(chan WorkProgressReceipt, 1)
	firstError := make(chan error, 1)
	go func() {
		receipt, err := s.WorkCheckpoint(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"})
		firstResult <- receipt
		firstError <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first checkpoint did not enter its adapter")
	}
	second, err := s.WorkCheckpoint(context.Background(), WorkCheckpointInput{Root: root, ProjectID: "example"})
	if err != nil || second.Status != "running" || !second.Reused || second.OperationID == "" {
		t.Fatalf("busy checkpoint=%#v err=%v", second, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("busy checkpoint started %d gate executions", got)
	}
	close(release)
	select {
	case first := <-firstResult:
		if first.Status != "completed" || !first.BaselineAdvanced {
			t.Fatalf("first checkpoint=%#v", first)
		}
	case <-time.After(time.Second):
		t.Fatal("first checkpoint did not finish")
	}
	if err := <-firstError; err != nil {
		t.Fatal(err)
	}
}

func configureGoCheckpoint(t *testing.T, s *Service, hubRevision string) {
	t.Helper()
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ProjectConfigurationUpdate(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectConfigurationUpdateInput{
		ProjectID:        "example",
		ExpectedRevision: configuration.Revision,
		Patch:            ProjectConfigurationPatch{Checkpoint: &model.ProjectCheckpointProfile{Adapter: "go"}},
		UpdatedBy:        "test",
		WriteOptions:     WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
}
