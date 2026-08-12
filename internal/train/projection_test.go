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

func TestProjectStatusExposesAmbiguousActiveTrains(t *testing.T) {
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	task1 := readyAdmissionTask(t, "GTW-TSK185", now)
	task2 := readyAdmissionTask(t, "GTW-TSK186", now)
	train1, err := New("gateway", "GTW-TRN1", "planner", []model.TaskAuthoring{task1}, now)
	if err != nil {
		t.Fatal(err)
	}
	train2, err := New("gateway", "GTW-TRN2", "planner", []model.TaskAuthoring{task2}, now)
	if err != nil {
		t.Fatal(err)
	}
	train1.Status = model.TrainV2Running
	train2.Status = model.TrainV2ReadyForIntegration
	projection := ProjectStatus([]model.TaskAuthoring{task1, task2}, []model.TrainV2{train2, train1})
	if !projection.AmbiguousActive || len(projection.ActiveTrains) != 2 || projection.CurrentTrain != "" || projection.NextAction == "" {
		t.Fatalf("active Train ambiguity was hidden: %#v", projection)
	}
	if projection.ActiveTrains[0] != "GTW-TRN1" || projection.ActiveTrains[1] != "GTW-TRN2" {
		t.Fatalf("active Train list is not deterministic: %#v", projection.ActiveTrains)
	}
}
