package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestRecordImplementationProofUsesExactAttempt(t *testing.T) {
	now := time.Now().UTC()
	train := model.TrainV2{SchemaVersion: 1, ID: "GTW-TRN1", ProjectID: "gateway", Revision: 1, Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: "GTW-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemRunning, AddedAt: now, Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "agent-1", AirelaySessionKey: "session", GatewayID: "gateway", StartHead: strings.Repeat("b", 40), StartedAt: now}}, ActiveAttemptNumber: 1}}}
	gates := []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}
	updated, err := RecordImplementationProof(train, "GTW-TSK1", 1, strings.Repeat("c", 40), strings.Repeat("d", 40), "report-1", gates, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Items[0].Attempts[0].Status != model.TrainV2AttemptSucceeded || updated.Items[0].SuccessfulAttemptNumber != 1 {
		t.Fatalf("Attempt was not finalized: %#v", updated.Items[0])
	}
	if _, err := RecordImplementationProof(train, "GTW-TSK1", 2, strings.Repeat("c", 40), strings.Repeat("d", 40), "report-1", gates, now.Add(time.Minute)); err == nil {
		t.Fatal("wrong Attempt was accepted")
	}
}
