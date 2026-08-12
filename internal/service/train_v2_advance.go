package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) dispatchTrainV2Continuation(ctx context.Context, previous trainv2.RuntimeBinding, run model.Run, expected string, now time.Time) (hub.TransactionResult, error) {
	runtime := previous
	runtime.RunID, runtime.StartedAt = run.ID, now
	if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
		return hub.TransactionResult{}, err
	}
	runtimePath := trainv2.RuntimePath(s.Config.StateDir, run.ProjectID, run.TrainID)
	if err := fsutil.WriteJSONAtomic(runtimePath, runtime, 0o600); err != nil {
		return hub.TransactionResult{}, err
	}
	keepRuntime := false
	defer func() {
		if !keepRuntime {
			_ = fsutil.WriteJSONAtomic(runtimePath, previous, 0o600)
		}
	}()
	packet, err := s.materializeTrainV2Packet(ctx, run, runtime)
	if err != nil {
		return hub.TransactionResult{}, fmt.Errorf("materialize Train continuation packet: %w", err)
	}
	message := trainv2.PacketDispatchMessage(packet)
	dispatch, err := s.Airelay.Prompt(ctx, run.SessionKey, message)
	if err != nil {
		return hub.TransactionResult{}, fmt.Errorf("train continuation dispatch failed: %w", err)
	}
	code := dispatch.ExitCode
	run.Status, run.DispatchMessage, run.DispatchExitCode = "dispatched", message, &code
	run.DispatchStdout, run.DispatchStderr = dispatch.Stdout, dispatch.Stderr
	dispatchedAt := dispatch.FinishedAt
	if dispatchedAt.IsZero() {
		dispatchedAt = now
	}
	run.DispatchedAt = &dispatchedAt
	tx, err := s.Hub.Transact(ctx, expected, "gateway: dispatch next Train v2 item "+run.TaskID, func(worktree string) ([]string, error) {
		var current model.Run
		if err := readWorktreeJSON(worktree, s.runPath(run.ProjectID, run.ID), &current); err != nil {
			return nil, err
		}
		if current.ID != run.ID || current.Status != "created" || current.TrainID != run.TrainID || current.TaskID != run.TaskID {
			return nil, fmt.Errorf("next Train Run changed before dispatch")
		}
		if err := model.ValidateRun(run); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.runPath(run.ProjectID, run.ID), run); err != nil {
			return nil, err
		}
		return []string{s.runPath(run.ProjectID, run.ID)}, nil
	})
	if err != nil {
		return hub.TransactionResult{}, err
	}
	keepRuntime = true
	return tx, nil
}
