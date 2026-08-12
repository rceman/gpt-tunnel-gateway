package service

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestTrainV2CutoverUsesProofOnlyCallbackInsteadOfActivator(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	mutatingCalled := false
	proofCalled := false
	s.taskActivator = func(context.Context, config.ProjectConfig, string) (TaskActivationResult, error) {
		mutatingCalled = true
		t.Fatal("cutover invoked the mutating task activator")
		return TaskActivationResult{}, nil
	}
	s.runtimeSourceProver = func(_ context.Context, _ config.ProjectConfig, source string) (TaskActivationResult, error) {
		proofCalled = true
		return TaskActivationResult{
			SourceHead: source,
			Activation: "already_active",
			Smoke:      "passed",
		}, nil
	}

	_, _, err := s.TrainV2Cutover(trustedWorkflowPolicyContext(context.Background(), "delivery"), TrainV2CutoverInput{
		ProjectID:                   "example",
		MaterializationAcknowledged: true,
		PlanRetirementAcknowledged:  true,
		UpdatedBy:                   "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "exact target runtime is not active") {
		t.Fatalf("cutover did not reach the non-mutating runtime proof boundary: err=%v", err)
	}
	if !proofCalled || mutatingCalled {
		t.Fatalf("cutover callback selection was unsafe: proof_called=%v mutating_called=%v", proofCalled, mutatingCalled)
	}
	if got, readErr := s.Hub.RemoteRevision(context.Background()); readErr != nil || got != hubRevision {
		t.Fatalf("failed cutover mutated Hub: before=%s after=%s err=%v", hubRevision, got, readErr)
	}
}
