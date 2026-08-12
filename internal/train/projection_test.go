package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectStatusUsesTasksAndTrainsWithoutPlanState(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	task := readyAdmissionTask(t, "GTW-TSK184", now)
	train, err := New("gateway", "GTW-TRN1", "planner", []model.TaskAuthoring{task}, now)
	if err != nil {
		t.Fatal(err)
	}
	projection := ProjectStatus([]model.TaskAuthoring{task}, []model.TrainV2{train})
	if projection.TaskCounts[model.TaskAuthoringReady] != 1 || projection.TrainCounts[model.TrainV2Planned] != 1 || projection.NextAction != "start Train GTW-TRN1" {
		t.Fatalf("unexpected planned projection: %#v", projection)
	}
	train.Status = model.TrainV2Running
	train.Items[0].Status = model.TrainV2ItemRunning
	train.Items[0].RunID = "GTW-TSK184-RUN1"
	train.Items[0].AgentID = "agent-1"
	train.Items[0].StartHead = strings.Repeat("a", 40)
	projection = ProjectStatus([]model.TaskAuthoring{task}, []model.TrainV2{train})
	if projection.CurrentTrain != train.ID || projection.CurrentTask != task.ID || projection.CurrentRun != train.Items[0].RunID {
		t.Fatalf("running Train was not projected: %#v", projection)
	}
}
