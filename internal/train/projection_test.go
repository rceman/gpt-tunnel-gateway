package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectStatusProjectsCurrentAttempt(t *testing.T) {
	now := time.Now().UTC()
	task := model.TaskAuthoring{ID: "GTW-TSK1", Status: model.TaskAuthoringReady}
	train := model.TrainV2{SchemaVersion: 1, ID: "GTW-TRN1", ProjectID: "gateway", Revision: 1, Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: task.ID, TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemRunning, AddedAt: now, Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "agent", AirelaySessionKey: "session", GatewayID: "gateway", StartHead: strings.Repeat("b", 40), StartedAt: now}}, ActiveAttemptNumber: 1}}}
	projection := ProjectStatus([]model.TaskAuthoring{task}, []model.TrainV2{train})
	if projection.CurrentTrain != train.ID || projection.CurrentTask != task.ID || projection.CurrentAttempt != "GTW-TRN1:1" {
		t.Fatalf("unexpected projection: %#v", projection)
	}
}
