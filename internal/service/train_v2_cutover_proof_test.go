package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestTrainV2CutoverUsesProofOnlyCallbackInsteadOfActivator(t *testing.T) {
	called := false
	s := &Service{
		taskActivator: func(context.Context, config.ProjectConfig, string) (TaskActivationResult, error) {
			t.Fatal("cutover invoked the mutating task activator")
			return TaskActivationResult{}, nil
		},
		runtimeSourceProver: func(_ context.Context, _ config.ProjectConfig, source string) (TaskActivationResult, error) {
			called = true
			return TaskActivationResult{SourceHead: source, Activation: "already_active", Smoke: "passed"}, nil
		},
	}

	proof, err := s.runtimeSourceProver(context.Background(), config.ProjectConfig{}, "source-head")
	if err != nil || proof.Activation != "already_active" || !called {
		t.Fatalf("proof-only callback was not selected: proof=%#v err=%v called=%v", proof, err, called)
	}
}
