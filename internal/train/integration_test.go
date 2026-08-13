package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestResetImplementationProofsClearsAttempts(t *testing.T) {
	now := time.Now().UTC()
	finished := now.Add(time.Minute)
	train := model.TrainV2{SchemaVersion: 1, ID: "GTW-TRN1", ProjectID: "gateway", Revision: 1, Status: model.TrainV2ReadyForIntegration, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: "GTW-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemFinalized, AddedAt: now, Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "agent-1", AirelaySessionKey: "session", GatewayID: "gateway", StartHead: strings.Repeat("b", 40), StartedAt: now, FinishedAt: &finished}}, SuccessfulAttemptNumber: 1, Proof: &model.TrainV2ImplementationProof{CheckpointHead: strings.Repeat("c", 40), ImplementationSHA: strings.Repeat("d", 40), ReportID: "report", GateResults: []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}, RecordedAt: now}}}}
	updated, err := ResetImplementationProofsForRestart(train, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items[0].Attempts) != 0 || updated.Items[0].Status != model.TrainV2ItemQueued {
		t.Fatalf("attempt history was retained on restart reset: %#v", updated.Items[0])
	}
}
