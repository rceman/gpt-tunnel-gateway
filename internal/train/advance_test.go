package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestBuildNextAttemptCreatesItemLocalAttemptOne(t *testing.T) {
	attempt, err := BuildNextAttempt(NextAttemptInput{GatewayID: "gateway", CurrentAttempt: model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "agent-1", AirelaySessionKey: "session", GatewayID: "gateway", StartHead: strings.Repeat("a", 40), StartedAt: time.Now().UTC()}, Next: model.TrainV2Item{Position: 1, TaskID: "GTW-TSK2", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("b", 64), Status: model.TrainV2ItemQueued, AddedAt: time.Now().UTC()}, AgentID: "agent-1", SessionKey: "session", StartHead: strings.Repeat("c", 40), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Number != 1 || attempt.Status != model.TrainV2AttemptRunning {
		t.Fatalf("unexpected attempt: %#v", attempt)
	}
}

func TestBuildNextAttemptRejectsUnsuccessfulCurrent(t *testing.T) {
	_, err := BuildNextAttempt(NextAttemptInput{GatewayID: "gateway", CurrentAttempt: model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptFailed}, Next: model.TrainV2Item{Status: model.TrainV2ItemQueued}, AgentID: "agent-1", SessionKey: "session", StartHead: strings.Repeat("a", 40), CreatedAt: time.Now().UTC()})
	if err == nil {
		t.Fatal("failed Attempt was advanced")
	}
}

func TestRetryAttemptAppendsItemLocalAttemptWithoutRunIdentity(t *testing.T) {
	now := time.Now().UTC()
	finished := now.Add(-time.Minute)
	item := model.TrainV2Item{Position: 0, TaskID: "GTW-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemBlocked, AddedAt: finished, Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptFailed, AgentID: "agent-1", GatewayID: "gateway", AirelaySessionKey: "session", StartHead: strings.Repeat("b", 40), StartedAt: finished, FinishedAt: &finished}}}
	updated, attempt, err := RetryAttempt(item, "agent-2", "gateway", "session-2", strings.Repeat("c", 40), now)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Number != 2 || len(updated.Attempts) != 2 || updated.Attempts[0].Status != model.TrainV2AttemptFailed || updated.ActiveAttemptNumber != 2 || updated.Status != model.TrainV2ItemRunning {
		t.Fatalf("retry did not append exact Attempt 2: %#v", updated)
	}
}
