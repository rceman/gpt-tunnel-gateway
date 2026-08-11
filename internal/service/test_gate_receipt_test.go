package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
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
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, gates.FullTestScope())
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
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, gates.FullTestScope())
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
	results, err = s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"check", "test"}, gates.FullTestScope())
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
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, gates.FullTestScope())
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
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, gates.FullTestScope())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || results[2].Execution != "reused" {
		t.Fatalf("committed tested bytes did not reuse receipt: calls=%d results=%#v", calls, results)
	}
}

func TestTestGateReceiptProspectiveTreeConvergesAddModifyDelete(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	if err := os.WriteFile(filepath.Join(root, "modify.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "delete.txt"), []byte("remove\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("filtered.txt filter=test-clean\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "filtered.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "config", "filter.test-clean.clean", "sed 's/dirty/clean/g'")
	testutil.Git(t, root, "add", "modify.txt", "delete.txt", ".gitattributes", "filtered.txt")
	testutil.Git(t, root, "commit", "-m", "seed prospective tree test")
	if err := os.WriteFile(filepath.Join(root, "modify.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "add.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "filtered.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
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
	prospective, _, err := s.currentTestIdentity(context.Background(), "example", root)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-m", "commit prospective tree test")
	committed := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD^{tree}"))
	if prospective != committed {
		t.Fatalf("prospective tree did not converge: dirty=%s committed=%s", prospective, committed)
	}
	results, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, gates.FullTestScope())
	if err != nil {
		t.Fatal(err)
	}
	if results[2].Execution != "reused" {
		t.Fatalf("add/modify/delete content did not reuse receipt: %#v", results)
	}
}

func TestScopedReceiptMatrixUsesExactServiceScopeIdentity(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	var calls [][]string
	fakeResults := func(names []string) []model.CompletionGateResult {
		results := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			results[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return results
	}
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		calls = append(calls, append([]string{}, names...))
		return fakeResults(names), nil
	}
	s.gateExecutorWithScope = func(_ context.Context, _ string, names []string, _ gates.TestScope) ([]model.CompletionGateResult, error) {
		calls = append(calls, append([]string{}, names...))
		return fakeResults(names), nil
	}
	scoped := gates.TestScope{Mode: gates.TestScopePackages, Packages: []string{"./internal/service", "./internal/gates", "./internal/service"}}
	result, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, scoped)
	if err != nil || len(result) != 3 || result[2].Execution != "executed" {
		t.Fatalf("initial scoped execution=%#v err=%v", result, err)
	}
	reordered := gates.TestScope{Mode: gates.TestScopePackages, Packages: []string{"./internal/gates", "./internal/service"}}
	result, err = s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, reordered)
	if err != nil || result[2].Execution != "reused" || result[2].TreeID == "" || result[2].ContractDigest == "" || result[2].ReceiptDigest == "" {
		t.Fatalf("normalized scope did not reuse auditable receipt=%#v err=%v", result, err)
	}
	if len(calls) != 2 || len(calls[1]) != 2 || calls[1][0] != "format" || calls[1][1] != "check" {
		t.Fatalf("non-test gates were not executed on reuse: %v", calls)
	}
	different := gates.TestScope{Mode: gates.TestScopePackages, Packages: []string{"./internal/model"}}
	result, err = s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, different)
	if err != nil || result[2].Execution != "executed" {
		t.Fatalf("different package scope reused receipt=%#v err=%v", result, err)
	}
}

func TestScopedAndFullReceiptsNeverCrossReuse(t *testing.T) {
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
	s.gateExecutorWithScope = func(_ context.Context, _ string, names []string, _ gates.TestScope) ([]model.CompletionGateResult, error) {
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
	scoped := gates.TestScope{Mode: gates.TestScopePackages, Packages: []string{"./internal/service"}}
	result, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, scoped)
	if err != nil || result[2].Execution != "executed" {
		t.Fatalf("full receipt satisfied scoped request=%#v err=%v", result, err)
	}
	result, err = s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"format", "check", "test"}, gates.FullTestScope())
	if err != nil || result[2].Execution != "executed" {
		t.Fatalf("scoped receipt satisfied full request=%#v err=%v", result, err)
	}
	if calls < 3 {
		t.Fatalf("scope separation did not execute expected gates: calls=%d", calls)
	}
}

func TestLegacyAndFailedScopedReceiptsFailClosed(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	path, err := s.testPassReceiptPath("example")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(map[string]any{"schema_version": 1, "project_id": "example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.gateExecutorWithScope = func(_ context.Context, _ string, names []string, _ gates.TestScope) ([]model.CompletionGateResult, error) {
		calls++
		results := make([]model.CompletionGateResult, len(names))
		for i, name := range names {
			results[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
		}
		return results, nil
	}
	scope := gates.TestScope{Mode: gates.TestScopePackages, Packages: []string{"./internal/service"}}
	if _, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"test"}, scope); err != nil || calls != 1 {
		t.Fatalf("legacy receipt did not trigger execution: calls=%d err=%v", calls, err)
	}
	if err := s.invalidateTestPassReceipt("example"); err != nil {
		t.Fatal(err)
	}
	s.gateExecutorWithScope = func(context.Context, string, []string, gates.TestScope) ([]model.CompletionGateResult, error) {
		return nil, errors.New("scoped test failed")
	}
	if _, err := s.executeProjectGatesWithTestReuse(context.Background(), "example", root, []string{"test"}, scope); err == nil {
		t.Fatal("failed scoped test was accepted")
	}
	if _, _, err := s.loadTestPassReceipt("example"); err == nil {
		t.Fatal("failed scoped test left reusable receipt")
	}
}
