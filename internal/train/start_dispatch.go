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
	RunID        string    `json:"run_id"`
	SessionKey   string    `json:"session_key"`
	PacketPath   string    `json:"packet_path"`
	WorktreePath string    `json:"worktree_path"`
	Message      string    `json:"message"`
	ExitCode     int       `json:"exit_code"`
	Stdout       string    `json:"stdout,omitempty"`
	Stderr       string    `json:"stderr,omitempty"`
	FinishedAt   time.Time `json:"finished_at"`
}

// PacketDispatchMessage deliberately carries only the server-owned packet
// path. The bounded Task contract, including the exact worktree, is in the
// packet; dispatch must not reconstruct it through a legacy CLI or MCP call.
func PacketDispatchMessage(packet AgentTaskPacket) string {
	return "Read " + packet.Path + " and execute."
}

func dispatchReceiptPath(stateDir, projectID, trainID string) string {
	return runtimePath(stateDir, projectID, trainID) + ".dispatch.json"
}

func packetDispatchRecorded(run model.Run, packet AgentTaskPacket) bool {
	info, err := os.Lstat(packet.Path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false
	}
	return run.Status == "dispatched" && run.DispatchMessage == PacketDispatchMessage(packet)
}

func resumeDispatch(ctx context.Context, deps StartDependencies, run model.Run, sessionKey string) error {
	_, err := dispatchRun(ctx, deps, run, sessionKey, "", time.Now().UTC())
	return err
}

func dispatchRun(ctx context.Context, deps StartDependencies, run model.Run, sessionKey, expected string, now time.Time) (model.Run, error) {
	if deps.MaterializePacket == nil {
		return model.Run{}, fmt.Errorf("Train agent packet materializer is unavailable")
	}
	runtime, err := ReadRuntime(deps.StateDir, run.ProjectID, run.TrainID)
	if err != nil || runtime.RunID != run.ID {
		return model.Run{}, fmt.Errorf("Train Run has no exact local runtime binding")
	}
	packet, err := deps.MaterializePacket(ctx, run, runtime)
	if err != nil {
		return model.Run{}, fmt.Errorf("materialize Train agent packet: %w", err)
	}
	if packet.Path == "" || packet.WorktreePath == "" {
		return model.Run{}, fmt.Errorf("Train agent packet has incomplete paths")
	}
	if packetDispatchRecorded(run, packet) {
		return run, nil
	}
	receiptPath := dispatchReceiptPath(deps.StateDir, run.ProjectID, run.TrainID)
	var receipt dispatchReceipt
	message := PacketDispatchMessage(packet)
	if err := fsutil.ReadJSONBounded(receiptPath, 1<<20, &receipt); err != nil {
		if !os.IsNotExist(err) {
			return model.Run{}, fmt.Errorf("read durable Train dispatch receipt: %w", err)
		}
		dispatch, promptErr := deps.Airelay.Prompt(ctx, sessionKey, message)
		if promptErr != nil {
			return model.Run{}, fmt.Errorf("train agent dispatch retry failed: %w", promptErr)
		}
		finishedAt := dispatch.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = now
		}
		receipt = dispatchReceipt{
			RunID:        run.ID,
			SessionKey:   sessionKey,
			PacketPath:   packet.Path,
			WorktreePath: packet.WorktreePath,
			Message:      message,
			ExitCode:     dispatch.ExitCode,
			Stdout:       dispatch.Stdout,
			Stderr:       dispatch.Stderr,
			FinishedAt:   finishedAt,
		}
		if err := fsutil.WriteJSONAtomic(receiptPath, receipt, 0o600); err != nil {
			return model.Run{}, fmt.Errorf("persist durable Train dispatch receipt: %w", err)
		}
	}
	if receipt.RunID != run.ID || receipt.SessionKey != sessionKey || receipt.PacketPath != packet.Path || receipt.WorktreePath != packet.WorktreePath || receipt.Message != message || receipt.FinishedAt.IsZero() {
		return model.Run{}, fmt.Errorf("durable Train dispatch receipt does not match the Run")
	}
	code := receipt.ExitCode
	run.Status = "dispatched"
	run.DispatchMessage = receipt.Message
	run.DispatchExitCode = &code
	run.DispatchStdout = receipt.Stdout
	run.DispatchStderr = receipt.Stderr
	run.DispatchedAt = &receipt.FinishedAt
	if expected == "" {
		var err error
		expected, err = deps.Hub.RemoteRevision(ctx)
		if err != nil {
			return model.Run{}, err
		}
	}
	_, err = deps.Hub.Transact(ctx, expected, "gateway: recover train v2 dispatch "+run.TrainID, func(worktree string) ([]string, error) {
		var current model.Run
		if err := readWorktreeJSON(worktree, runPath(run.ProjectID, run.ID), &current); err != nil {
			return nil, err
		}
		if current.Status != "created" && current.Status != "dispatching" && (current.Status != "dispatched" || current.DispatchMessage == PacketDispatchMessage(packet)) {
			return nil, fmt.Errorf("train Run changed before dispatch recovery")
		}
		if err := model.ValidateRun(run); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, runPath(run.ProjectID, run.ID), run); err != nil {
			return nil, err
		}
		return []string{runPath(run.ProjectID, run.ID)}, nil
	})
	if err == nil {
		_ = os.Remove(receiptPath)
	}
	return run, err
}
