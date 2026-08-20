package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	minAwaitMinutes = 1
	maxAwaitMinutes = 10
)

type awaitResult struct {
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	ElapsedSeconds float64   `json:"elapsed_seconds"`
}

func (s *Server) ensureSystemAwaitActions() {
	if s.Service == nil {
		return
	}
	s.systemAwaitActions.Do(func() {
		minutes := integer("Minutes to await.", minAwaitMinutes, maxAwaitMinutes)
		s.systemAwaitActionErr = s.RegisterGenericAction(GenericAction{
			Path:        "system/await",
			Description: "Block for a bounded interval while preserving caller cancellation.",
			InputSchema: obj(map[string]any{"minutes": minutes}, "minutes"),
			OutputSchema: closedOutput(map[string]any{
				"started_at": outputDateTime(), "finished_at": outputDateTime(), "elapsed_seconds": map[string]any{"type": "number"},
			}, "started_at", "finished_at", "elapsed_seconds"),
			Annotations: ToolAnnotations{
				ReadOnlyHint:   true,
				IdempotentHint: true,
			},
			LocalReadOnly: true,
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var input struct {
					Minutes int `json:"minutes"`
				}
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				if input.Minutes < minAwaitMinutes || input.Minutes > maxAwaitMinutes {
					return nil, fmt.Errorf("minutes must be between %d and %d", minAwaitMinutes, maxAwaitMinutes)
				}
				return awaitDuration(ctx, time.Duration(input.Minutes)*time.Minute)
			},
		})
	})
	if s.systemAwaitActionErr != nil {
		panic(s.systemAwaitActionErr)
	}
}

func awaitDuration(ctx context.Context, duration time.Duration) (awaitResult, error) {
	started := time.Now().UTC()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		finished := time.Now().UTC()
		return awaitResult{
			StartedAt:      started,
			FinishedAt:     finished,
			ElapsedSeconds: finished.Sub(started).Seconds(),
		}, nil
	case <-ctx.Done():
		return awaitResult{}, ctx.Err()
	}
}
