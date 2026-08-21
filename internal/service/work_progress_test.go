package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestWorkProgressAdvancesPostGateBaselineAndNextCallUsesDelta(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	path := filepath.Join(root, "progress.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		calls++
		if err := os.WriteFile(path, []byte("after-format\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return fakeReceiptResults(names), nil
	}
	first, err := s.WorkProgress(context.Background(), WorkProgressInput{Root: root, ProjectID: "example"})
	if err != nil || first.Status != "completed" || !first.BaselineAdvanced {
		t.Fatalf("first progress=%#v err=%v", first, err)
	}
	state, err := readWorkProgressState(workProgressStatePath(s.Config.StateDir, root, "example"))
	if err != nil || state.Baseline["progress.txt"] == "" || state.GateIdentity == "" {
		t.Fatalf("baseline state=%#v err=%v", state, err)
	}
	second, err := s.WorkProgress(context.Background(), WorkProgressInput{Root: root, ProjectID: "example"})
	if err != nil || second.Status != "completed" || !second.Reused || second.BaselineAdvanced || len(second.ChangedFiles) != 0 {
		t.Fatalf("second progress=%#v err=%v", second, err)
	}
	if calls != 1 {
		t.Fatalf("gates executed %d times after baseline advanced", calls)
	}
}

func TestWorkProgressFailureDoesNotAdvanceBaseline(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	path := filepath.Join(root, "progress-failure.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fail := true
	calls := 0
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		calls++
		if fail {
			return nil, errors.New("gate failed")
		}
		return fakeReceiptResults(names), nil
	}
	first, err := s.WorkProgress(context.Background(), WorkProgressInput{Root: root, ProjectID: "example"})
	if err == nil || first.Status != "failed" || first.BaselineAdvanced {
		t.Fatalf("failed progress=%#v err=%v", first, err)
	}
	state, stateErr := readWorkProgressState(workProgressStatePath(s.Config.StateDir, root, "example"))
	if stateErr != nil || len(state.Baseline) != 0 {
		t.Fatalf("failed progress advanced baseline=%#v err=%v", state, stateErr)
	}
	fail = false
	second, err := s.WorkProgress(context.Background(), WorkProgressInput{Root: root, ProjectID: "example"})
	if err != nil || second.Status != "completed" || !second.BaselineAdvanced || calls != 2 {
		t.Fatalf("retry progress=%#v err=%v calls=%d", second, err, calls)
	}
}
