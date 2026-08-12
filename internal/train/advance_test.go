package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestBuildNextRunPreservesTrainOwnerAndResetsDispatchState(t *testing.T) {
	now := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	current := model.Run{
		SchemaVersion: model.SchemaVersion, ID: "GTW-TSK1-RUN1", TaskID: "GTW-TSK1", TaskSHA256: strings.Repeat("a", 64),
		ProjectID: "gateway", GatewayID: "gateway", SessionKey: "agent-session", AgentID: "agent-1",
		Branch: "train/GTW-TRN1", TrainID: "GTW-TRN1", LaneBranch: "train/GTW-TRN1", BaseRevision: strings.Repeat("b", 40),
		Status: "succeeded", CompletionPath: "/state/runs/GTW-TSK1-RUN1/completion.json", CreatedAt: now.Add(-time.Minute), FinishedAt: &now,
	}
	next := model.TrainV2Item{TaskID: "GTW-TSK2", TaskRevision: 2, TaskRevisionSHA256: strings.Repeat("c", 64), Status: model.TrainV2ItemQueued}
	got, err := BuildNextRun(NextRunInput{
		Current:      current,
		Next:         next,
		RunID:        "GTW-TSK2-RUN1",
		BaseRevision: strings.Repeat("d", 40),
		StateDir:     "/state",
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "created" || got.TaskID != next.TaskID || got.TaskSHA256 != next.TaskRevisionSHA256 || got.TrainID != current.TrainID || got.SessionKey != current.SessionKey || got.BaseRevision != strings.Repeat("d", 40) {
		t.Fatalf("unexpected next Run: %#v", got)
	}
	if got.FinishedAt != nil || got.DispatchedAt != nil || got.DispatchMessage != "" || got.CompletionPath != "/state/runs/GTW-TSK2-RUN1/completion.json" {
		t.Fatalf("created next Run retained terminal/dispatch state: %#v", got)
	}
}

func TestBuildNextRunRejectsNonQueuedOrMismatchedItem(t *testing.T) {
	current := model.Run{SchemaVersion: model.SchemaVersion, ID: "GTW-TSK1-RUN1", TaskID: "GTW-TSK1", TaskSHA256: strings.Repeat("a", 64), ProjectID: "gateway", GatewayID: "gateway", SessionKey: "session", AgentID: "agent-1", Branch: "train/GTW-TRN1", TrainID: "GTW-TRN1", LaneBranch: "train/GTW-TRN1", BaseRevision: strings.Repeat("b", 40), Status: "succeeded", CompletionPath: "/state/completion.json", CreatedAt: time.Now().UTC()}
	_, err := BuildNextRun(NextRunInput{
		Current:      current,
		Next:         model.TrainV2Item{TaskID: "GTW-TSK2", TaskRevisionSHA256: strings.Repeat("c", 64), Status: model.TrainV2ItemFinalized},
		RunID:        "GTW-TSK2-RUN1",
		BaseRevision: strings.Repeat("d", 40),
		StateDir:     "/state",
		CreatedAt:    time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("non-queued item was accepted")
	}
}
