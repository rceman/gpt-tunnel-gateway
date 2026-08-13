package train

import (
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// NextAttemptInput contains only the canonical identity and immutable owner
// snapshot needed to start the immediate queued TrainItem.
type NextAttemptInput struct {
	CurrentAttempt model.TrainV2Attempt
	Next           model.TrainV2Item
	AgentID        string
	GatewayID      string
	SessionKey     string
	StartHead      string
	CreatedAt      time.Time
}

// RetryAttempt appends a new item-local Attempt after a terminal failure. It
// never overwrites the prior Attempt and never allocates a global Run ID.
func RetryAttempt(item model.TrainV2Item, agentID, gatewayID, sessionKey, startHead string, createdAt time.Time) (model.TrainV2Item, model.TrainV2Attempt, error) {
	if len(item.Attempts) == 0 || item.Status != model.TrainV2ItemBlocked || createdAt.IsZero() || model.ValidateObjectIdentifier(agentID) != nil || model.ValidateObjectIdentifier(gatewayID) != nil || sessionKey == "" || model.ValidateCommitSHA(startHead) != nil {
		return model.TrainV2Item{}, model.TrainV2Attempt{}, fmt.Errorf("Train item is not retryable")
	}
	previous := item.Attempts[len(item.Attempts)-1]
	if previous.Status != model.TrainV2AttemptFailed && previous.Status != model.TrainV2AttemptAborted && previous.Status != model.TrainV2AttemptRecovered {
		return model.TrainV2Item{}, model.TrainV2Attempt{}, fmt.Errorf("last Train Attempt is not terminally retryable")
	}
	attempt := model.TrainV2Attempt{Number: uint64(len(item.Attempts) + 1), Status: model.TrainV2AttemptRunning, AgentID: agentID, GatewayID: gatewayID, AirelaySessionKey: sessionKey, StartHead: startHead, StartedAt: createdAt}
	if err := model.ValidateTrainV2Attempt(attempt); err != nil {
		return model.TrainV2Item{}, model.TrainV2Attempt{}, err
	}
	updated := item
	updated.Status = model.TrainV2ItemRunning
	updated.ActiveAttemptNumber = attempt.Number
	updated.SuccessfulAttemptNumber = 0
	updated.Attempts = append(append([]model.TrainV2Attempt{}, item.Attempts...), attempt)
	return updated, attempt, nil
}

func BuildNextAttempt(in NextAttemptInput) (model.TrainV2Attempt, error) {
	if in.CurrentAttempt.Number == 0 || in.CurrentAttempt.Status != model.TrainV2AttemptSucceeded || in.Next.Status != model.TrainV2ItemQueued || in.CreatedAt.IsZero() || model.ValidateObjectIdentifier(in.AgentID) != nil || model.ValidateObjectIdentifier(in.GatewayID) != nil || in.SessionKey == "" || model.ValidateCommitSHA(in.StartHead) != nil {
		return model.TrainV2Attempt{}, fmt.Errorf("current Train Attempt or next item is not advanceable")
	}
	attempt := model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: in.AgentID, GatewayID: in.GatewayID, AirelaySessionKey: in.SessionKey, StartHead: in.StartHead, StartedAt: in.CreatedAt}
	if err := model.ValidateTrainV2Attempt(attempt); err != nil {
		return model.TrainV2Attempt{}, err
	}
	return attempt, nil
}
