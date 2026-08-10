package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCanonicalTestGateReceiptReusesIdenticalTree(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	calls := 0
	var calledNames [][]string
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		calls++
		calledNames = append(calledNames, append([]string{}, names...))
		results := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			results[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return results, nil
	}
	if err := s.ExecuteCanonicalTestGate(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("initial test calls=%d", calls)
	}
	calls = 0
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(calledNames) != 2 || len(calledNames[1]) != 2 || calledNames[1][0] != "format" || calledNames[1][1] != "check" {
		t.Fatalf("reuse executed unexpected gates: calls=%d names=%v", calls, calledNames)
	}
	if results[2].Execution != "reused" || results[2].TreeID == "" || results[2].ContractDigest == "" || results[2].ReceiptDigest == "" {
		t.Fatalf("test gate was not auditable reuse: %#v", results)
	}
}

func TestTestGateReceiptInvalidatesForDirtyTreeAndContractChange(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	calls := 0
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		calls++
		results := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			results[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return results, nil
	}
	if err := s.ExecuteCanonicalTestGate(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || results[2].Execution != "executed" {
		t.Fatalf("dirty tree reused receipt: calls=%d results=%#v", calls, results)
	}
	if err := os.Remove(filepath.Join(root, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	calls = 0
	results, err = s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || results[1].Execution != "executed" {
		t.Fatalf("contract change reused receipt: calls=%d results=%#v", calls, results)
	}
}

func TestFailedTestGateDoesNotCreateReusableReceipt(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	s.gateExecutor = func(context.Context, string, []string) ([]model.CompletionGateResult, error) {
		return nil, errors.New("test failed")
	}
	if err := s.ExecuteCanonicalTestGate(context.Background(), root); err == nil {
		t.Fatal("failed test gate was accepted")
	}
	if _, _, err := s.loadTestPassReceipt("example"); err == nil {
		t.Fatal("failed test created a reusable receipt")
	}
}

func TestTestGateReceiptReusesAcrossIdenticalCommittedTree(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		results := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			results[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return results, nil
	}
	if err := s.ExecuteCanonicalTestGate(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	before := testutil.Git(t, root, "rev-parse", "HEAD^{tree}")
	testutil.Git(t, root, "commit", "--allow-empty", "-m", "same tree content checkpoint")
	after := testutil.Git(t, root, "rev-parse", "HEAD^{tree}")
	if before != after {
		t.Fatalf("empty commit changed tree: before=%s after=%s", before, after)
	}
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if results[2].Execution != "reused" {
		t.Fatalf("identical committed tree did not reuse receipt: %#v", results)
	}
}

func TestTestGateReceiptReusesDirtyContentAfterCommit(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	if err := os.WriteFile(filepath.Join(root, "dirty-pass.txt"), []byte("tested bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		calls++
		results := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			results[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return results, nil
	}
	if err := s.ExecuteCanonicalTestGate(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("initial test calls=%d", calls)
	}
	testutil.Git(t, root, "add", "dirty-pass.txt")
	testutil.Git(t, root, "commit", "-m", "commit tested dirty content")
	calls = 0
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || results[2].Execution != "reused" {
		t.Fatalf("committed tested bytes did not reuse receipt: calls=%d results=%#v", calls, results)
	}
}
