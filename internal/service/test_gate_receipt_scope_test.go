package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func fakeReceiptResults(names []string) []model.CompletionGateResult {
	results := make([]model.CompletionGateResult, len(names))
	for i, name := range names {
		results[i] = model.CompletionGateResult{ID: name, ExitCode: 0}
	}
	return results
}

func TestScopedReceiptMatrixUsesExactServiceScopeIdentity(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	root := s.Config.Projects["example"].Root
	var calls [][]string
	s.gateExecutor = func(_ context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		calls = append(calls, append([]string{}, names...))
		return fakeReceiptResults(names), nil
	}
	s.gateExecutorWithScope = func(_ context.Context, _ string, names []string, _ gates.TestScope) ([]model.CompletionGateResult, error) {
		calls = append(calls, append([]string{}, names...))
		return fakeReceiptResults(names), nil
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
		return fakeReceiptResults(names), nil
	}
	s.gateExecutorWithScope = func(_ context.Context, _ string, names []string, _ gates.TestScope) ([]model.CompletionGateResult, error) {
		calls++
		return fakeReceiptResults(names), nil
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
		return fakeReceiptResults(names), nil
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
