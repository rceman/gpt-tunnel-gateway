package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK604TaskCompleteAfterRestartIgnoresEnvironmentDrift(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task, integration := tsk585IntegratedCompleteFixture(t, s, "tsk604-restart")
	sessionID := tsk585PlannerSession(t, s)
	evidence := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585ReviewRationale(t, task, 1, "integrated", integration, "")}, []string{integration})
	input := tsk585CompletionInput(task, "integrated", "accepted after restart", evidence.ID)

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", filepath.Join(t.TempDir(), "shadow")+string(os.PathListSeparator)+oldPath)
	t.Setenv("GOFLAGS", "-run=^TestNothing$")
	t.Setenv("GPT_TUNNEL_UI_INJECTED", "changed-after-restart")
	t.Setenv("PWD", filepath.Join(t.TempDir(), "changed", "slash"))

	restarted := NewWithDurabilityDeferredWorkers(s.Config, db)
	restarted.gateExecutorWithProjectCommands = func(context.Context, string, []string, model.ProjectGateCommands, string) ([]model.CompletionGateResult, error) {
		t.Fatal("task/complete must not rerun mutable verification gates")
		return nil, nil
	}
	out, err := restarted.TaskComplete(ctx, input, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if out.Key != task.ID || out.Status != model.TaskAuthoringDone || out.Revision != task.Revision {
		t.Fatalf("completion after restart=%#v", out)
	}
}

func TestTSK604GateProfileTracksConfiguredDefinition(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	_, before, err := s.taskExecutionGateProfile(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	commands := configuration.Workflow.GateCommands
	commands.Format.Command = append(append([]string{}, commands.Format.Command...), "--profile-change")
	if _, _, err := s.ProjectConfigurationUpdate(ctx, ProjectConfigurationUpdateInput{
		ProjectID:        "example",
		ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{
			GateCommands: &commands,
		},
		UpdatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, after, err := s.taskExecutionGateProfile(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatalf("configured gate definition did not change profile digest: %s", before)
	}
}
