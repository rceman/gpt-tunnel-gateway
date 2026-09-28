package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectConfigurationProcedureAndHookManagement(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := WithAgentSessionID(trustedWorkflowPolicyContext(context.Background(), "planner"), "planner-test")
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Revision != 1 || configuration.ProjectID != "example" || configuration.Procedures == nil || configuration.Hooks == nil {
		t.Fatalf("unexpected registered configuration: %#v", configuration)
	}
	definition := workFinishedProcedureDefinition()
	created, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "notify_work_finished",
		Definition: definition,
		Reason:     "Add the project work-finished action.",
	})
	if err != nil || created.Revision != 2 {
		t.Fatalf("Procedure create=%#v err=%v", created, err)
	}
	bound, err := s.ConfigHookBind(ctx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPostAgentWorkFinished,
		Procedure: "notify_work_finished",
		Reason:    "Bind work-finished notification.",
	})
	if err != nil || bound.Revision != 3 {
		t.Fatalf("Hook bind=%#v err=%v", bound, err)
	}
	if _, err := s.ConfigProcedureRemove(ctx, ConfigProcedureRemoveInput{
		ProjectID: "example",
		Name:      "notify_work_finished",
		Reason:    "Remove the obsolete notification.",
	}); err == nil {
		t.Fatal("bound Procedure removal succeeded")
	}
	unbound, err := s.ConfigHookUnbind(ctx, ConfigHookUnbindInput{
		ProjectID: "example",
		Hook:      model.HookPostAgentWorkFinished,
		Reason:    "Unbind the work-finished notification.",
	})
	if err != nil || unbound.Revision != 4 {
		t.Fatalf("Hook unbind=%#v err=%v", unbound, err)
	}
	removed, err := s.ConfigProcedureRemove(ctx, ConfigProcedureRemoveInput{
		ProjectID: "example",
		Name:      "notify_work_finished",
		Reason:    "Remove the obsolete notification.",
	})
	if err != nil || removed.Revision != 5 {
		t.Fatalf("Procedure remove=%#v err=%v", removed, err)
	}
	status, err := s.ProjectConfigurationSummary(ctx, "example")
	if err != nil || status.ConfigurationRevision != 5 || status.ProcedureCount != 0 || status.BoundHookCount != 0 {
		t.Fatalf("unexpected configuration summary: %#v err=%v", status, err)
	}
}

func workFinishedProcedureDefinition() model.ProjectProcedureDefinition {
	return model.ProjectProcedureDefinition{
		Script:  "scripts/task-submit.sh",
		Summary: "Notify after Agent work completes",
		Guide:   "Accepts the completed work epoch and its project and Agent references.",
		Input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"epoch":   map[string]any{"type": "string", "minLength": 47, "maxLength": 47},
				"project": map[string]any{"$ref": "EntityKeyAndReference"},
				"agent":   map[string]any{"$ref": "EntityKeyAndReference"},
			},
			"required":             []any{"epoch", "project"},
			"additionalProperties": false,
		},
		Output: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func TestProjectConfigurationDefaultsAreVersionThree(t *testing.T) {
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	if configuration.SchemaVersion != 3 {
		t.Fatalf("configuration schema version=%d", configuration.SchemaVersion)
	}
}
