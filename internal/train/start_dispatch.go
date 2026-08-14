package train

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type dispatchReceipt struct {
	TrainID            string    `json:"train_id"`
	ItemPosition       int       `json:"item_position"`
	TaskID             string    `json:"task_id"`
	TaskRevision       int       `json:"task_revision"`
	TaskRevisionSHA256 string    `json:"task_revision_sha256"`
	AttemptNumber      uint64    `json:"attempt_number"`
	AgentID            string    `json:"agent_id"`
	SessionKey         string    `json:"session_key"`
	StartHead          string    `json:"start_head"`
	PacketPath         string    `json:"packet_path"`
	WorktreePath       string    `json:"worktree_path"`
	Message            string    `json:"message"`
	ExitCode           int       `json:"exit_code"`
	Stdout             string    `json:"stdout,omitempty"`
	Stderr             string    `json:"stderr,omitempty"`
	FinishedAt         time.Time `json:"finished_at"`
}

func PacketDispatchMessage(packet AgentTaskPacket) string {
	return "Read " + packet.Path + " and execute."
}

func dispatchReceiptPath(stateDir, projectID, trainID string) string {
	return runtimePath(stateDir, projectID, trainID) + ".dispatch.json"
}

func dispatchAttempt(ctx context.Context, deps StartDependencies, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, runtime RuntimeBinding, expected string) error {
	if deps.MaterializePacket == nil {
		return fmt.Errorf("Train agent packet materializer is unavailable")
	}
	if runtime.TrainID != train.ID || runtime.ItemPosition != item.Position || runtime.TaskID != item.TaskID || runtime.AttemptNumber != attempt.Number || runtime.SessionKey != attempt.AirelaySessionKey {
		return fmt.Errorf("Train Attempt has no exact local runtime binding")
	}
	packet, err := deps.MaterializePacket(ctx, train, item, attempt, runtime)
	if err != nil {
		return fmt.Errorf("materialize Train agent packet: %w", err)
	}
	if packet.Path == "" || packet.WorktreePath == "" {
		return fmt.Errorf("Train agent packet has incomplete paths")
	}
	receiptPath := dispatchReceiptPath(deps.StateDir, train.ProjectID, train.ID)
	message := PacketDispatchMessage(packet)
	var receipt dispatchReceipt
	if err := fsutil.ReadJSONBounded(receiptPath, 1<<20, &receipt); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read durable Train dispatch receipt: %w", err)
		}
		dispatch, promptErr := deps.Airelay.PromptWithProvenance(ctx, attempt.AirelaySessionKey, deps.SessionOrigin, message)
		if promptErr != nil {
			return fmt.Errorf("train agent dispatch retry failed: %w", promptErr)
		}
		finishedAt := dispatch.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = time.Now().UTC()
		}
		receipt = dispatchReceipt{
			TrainID:            train.ID,
			ItemPosition:       item.Position,
			TaskID:             item.TaskID,
			TaskRevision:       item.TaskRevision,
			TaskRevisionSHA256: item.TaskRevisionSHA256,
			AttemptNumber:      attempt.Number,
			AgentID:            attempt.AgentID,
			SessionKey:         attempt.AirelaySessionKey,
			StartHead:          attempt.StartHead,
			PacketPath:         packet.Path,
			WorktreePath:       packet.WorktreePath,
			Message:            message,
			ExitCode:           dispatch.ExitCode,
			Stdout:             dispatch.Stdout,
			Stderr:             dispatch.Stderr,
			FinishedAt:         finishedAt,
		}
		if err := fsutil.WriteJSONAtomic(receiptPath, receipt, 0o600); err != nil {
			return fmt.Errorf("persist durable Train dispatch receipt: %w", err)
		}
	}
	if !dispatchReceiptMatchesAttempt(receipt, train, item, attempt, packet, message) {
		reused, err := reconcileDispatchReceipt(receipt, train, item, attempt)
		if err != nil {
			return err
		}
		if reused {
			return nil
		}
		if err := os.Remove(receiptPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("retire stale Train dispatch receipt: %w", err)
		}
		return dispatchAttempt(ctx, deps, train, item, attempt, runtime, expected)
	}
	if expected == "" {
		var err error
		expected, err = deps.Hub.RemoteRevision(ctx)
		if err != nil {
			return err
		}
	}
	_, err = deps.Hub.Transact(ctx, expected, "gateway: dispatch Train v2 Attempt", func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, trainPath(train.ProjectID, train.ID), &current); err != nil {
			return nil, err
		}
		if current.ProjectID != train.ProjectID || current.ID != train.ID || item.Position >= len(current.Items) {
			return nil, fmt.Errorf("Train Attempt binding changed before dispatch")
		}
		currentItem := current.Items[item.Position]
		if currentItem.TaskID != item.TaskID || len(currentItem.Attempts) < int(attempt.Number) {
			return nil, fmt.Errorf("Train Attempt item changed before dispatch")
		}
		currentAttempt := &currentItem.Attempts[attempt.Number-1]
		if currentAttempt.AgentID != attempt.AgentID || currentAttempt.AirelaySessionKey != attempt.AirelaySessionKey || currentAttempt.StartHead != attempt.StartHead {
			return nil, fmt.Errorf("Train Attempt execution snapshot changed before dispatch")
		}
		code := receipt.ExitCode
		currentAttempt.Status = model.TrainV2AttemptRunning
		currentAttempt.DispatchedAt = &receipt.FinishedAt
		currentItem.Attempts[attempt.Number-1] = *currentAttempt
		current.Items[item.Position] = currentItem
		if err := model.ValidateTrainV2(current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, trainPath(train.ProjectID, train.ID), current); err != nil {
			return nil, err
		}
		_ = code
		return []string{trainPath(train.ProjectID, train.ID)}, nil
	})
	if err == nil {
		_ = os.Remove(receiptPath)
	}
	return err
}

func dispatchReceiptMatchesAttempt(receipt dispatchReceipt, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, packet AgentTaskPacket, message string) bool {
	return receipt.TrainID == train.ID && receipt.ItemPosition == item.Position && receipt.TaskID == item.TaskID && receipt.TaskRevision == item.TaskRevision && receipt.TaskRevisionSHA256 == item.TaskRevisionSHA256 && receipt.AttemptNumber == attempt.Number && receipt.AgentID == attempt.AgentID && receipt.SessionKey == attempt.AirelaySessionKey && receipt.StartHead == attempt.StartHead && receipt.PacketPath == packet.Path && receipt.WorktreePath == packet.WorktreePath && receipt.Message == message && !receipt.FinishedAt.IsZero()
}

func reconcileDispatchReceipt(receipt dispatchReceipt, train model.TrainV2, currentItem model.TrainV2Item, currentAttempt model.TrainV2Attempt) (bool, error) {
	if receipt.TrainID != train.ID || receipt.ItemPosition < 0 || receipt.ItemPosition >= len(train.Items) {
		return false, fmt.Errorf("durable Train dispatch receipt does not match the Train")
	}
	owner := train.Items[receipt.ItemPosition]
	if owner.TaskID != receipt.TaskID || receipt.AttemptNumber == 0 || receipt.AttemptNumber > uint64(len(owner.Attempts)) {
		return false, fmt.Errorf("durable Train dispatch receipt does not match an authoritative Attempt")
	}
	ownerAttempt := owner.Attempts[receipt.AttemptNumber-1]
	legacy := receipt.AgentID == "" && receipt.StartHead == "" && receipt.TaskRevision == 0 && receipt.TaskRevisionSHA256 == ""
	if ownerAttempt.AirelaySessionKey != receipt.SessionKey || (!legacy && (ownerAttempt.AgentID != receipt.AgentID || ownerAttempt.StartHead != receipt.StartHead || owner.TaskRevision != receipt.TaskRevision || owner.TaskRevisionSHA256 != receipt.TaskRevisionSHA256)) {
		return false, fmt.Errorf("durable Train dispatch receipt execution identity does not match the Attempt")
	}
	if owner.Position == currentItem.Position && ownerAttempt.Number == currentAttempt.Number {
		if ownerAttempt.DispatchedAt != nil {
			return true, nil
		}
		return false, fmt.Errorf("durable Train dispatch receipt does not match the current Attempt")
	}
	if ownerAttempt.Status != model.TrainV2AttemptSucceeded && ownerAttempt.Status != model.TrainV2AttemptFailed && ownerAttempt.Status != model.TrainV2AttemptAborted && ownerAttempt.Status != model.TrainV2AttemptRecovered {
		return false, fmt.Errorf("durable Train dispatch receipt belongs to a non-terminal Attempt")
	}
	return false, nil
}

// DispatchAttempt resumes or delivers an already-persisted TrainItem Attempt.
// The durable dispatch receipt makes retries idempotent after a prompt has
// completed but before its Hub transaction has been observed by the caller.
func DispatchAttempt(ctx context.Context, deps StartDependencies, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, runtime RuntimeBinding, expected string) error {
	return dispatchAttempt(ctx, deps, train, item, attempt, runtime, expected)
}
