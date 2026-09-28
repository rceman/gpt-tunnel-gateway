package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectConfigurationMutationsDoNotAllocateOperations(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	ctx := WithAgentSessionID(trustedWorkflowPolicyContext(context.Background(), "planner"), "planner-test")
	before, err := db.ListLocalOperations(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "notify_work_finished",
		Definition: workFinishedProcedureDefinition(),
		Reason:     "Add the project work-finished action.",
	})
	if err != nil || result.Revision != 2 {
		t.Fatalf("Procedure create=%#v err=%v", result, err)
	}
	after, err := db.ListLocalOperations(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("synchronous configuration mutation allocated a Local Operation: before=%d after=%d", len(before), len(after))
	}
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil || configuration.Procedures["notify_work_finished"].Script == "" || configuration.Revision != result.Revision {
		t.Fatalf("configuration after mutation=%#v err=%v", configuration, err)
	}
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		t.Fatalf("persisted configuration is invalid: %v", err)
	}
}
