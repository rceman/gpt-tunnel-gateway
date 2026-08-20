package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const (
	minAwaitMinutes      = 1
	maxAwaitMinutes      = 10
	minAwaitSeconds      = 1
	maxAwaitSeconds      = maxAwaitMinutes * 60
	agentStatusTailLines = 20
	minAwaitTailLines    = 10
	maxAwaitTailLines    = 40
)

type awaitResult struct {
	FinishedAt   string `json:"finished_at"`
	Continuation any    `json:"continuation,omitempty"`
}

type awaitInput struct {
	Minutes         *int            `json:"minutes,omitempty"`
	Seconds         *int            `json:"seconds,omitempty"`
	OnComplete      string          `json:"on_complete,omitempty"`
	OnCompleteInput json.RawMessage `json:"on_complete_input,omitempty"`
}

func awaitContinuationInputSchema() map[string]any {
	return obj(map[string]any{
		"agent_id": str("Registered Agent identifier for the read-only status continuation."),
	})
}

func awaitInputSchema() map[string]any {
	continuation := map[string]any{
		"on_complete":       outputEnum("agent/status"),
		"on_complete_input": awaitContinuationInputSchema(),
	}
	minutes := map[string]any{"minutes": integer("Minutes to await.", minAwaitMinutes, maxAwaitMinutes)}
	seconds := map[string]any{"seconds": integer("Seconds to await.", minAwaitSeconds, maxAwaitSeconds)}
	for key, value := range continuation {
		minutes[key] = value
		seconds[key] = value
	}
	return map[string]any{
		"oneOf": []any{
			obj(minutes, "minutes"),
			obj(seconds, "seconds"),
		},
	}
}

func (s *Server) ensureSystemAwaitActions() {
	if s.Service == nil {
		return
	}
	s.systemAwaitActions.Do(func() {
		s.systemAwaitActionErr = s.RegisterGenericAction(GenericAction{
			Path:        "system/await",
			Description: "Block for a bounded interval while preserving caller cancellation.",
			InputSchema: awaitInputSchema(),
			OutputSchema: closedOutput(map[string]any{
				"finished_at":  outputString(),
				"continuation": map[string]any{"type": "object", "additionalProperties": true},
			}, "finished_at"),
			Annotations: ToolAnnotations{
				ReadOnlyHint:   true,
				IdempotentHint: true,
			},
			LocalReadOnly: true,
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var input awaitInput
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				duration, err := awaitInputDuration(input)
				if err != nil {
					return nil, err
				}
				if err := validateAwaitContinuation(input.OnComplete); err != nil {
					return nil, err
				}
				return s.awaitWithContinuation(ctx, duration, input.OnComplete, input.OnCompleteInput)
			},
		})
	})
	if s.systemAwaitActionErr != nil {
		panic(s.systemAwaitActionErr)
	}
}

func awaitInputDuration(input awaitInput) (time.Duration, error) {
	if input.Minutes != nil && input.Seconds != nil {
		return 0, fmt.Errorf("minutes and seconds are mutually exclusive")
	}
	if input.Minutes == nil && input.Seconds == nil {
		return 0, fmt.Errorf("one of minutes or seconds is required")
	}
	if input.Minutes != nil {
		if *input.Minutes < minAwaitMinutes || *input.Minutes > maxAwaitMinutes {
			return 0, fmt.Errorf("minutes must be between %d and %d", minAwaitMinutes, maxAwaitMinutes)
		}
		return time.Duration(*input.Minutes) * time.Minute, nil
	}
	if *input.Seconds < minAwaitSeconds || *input.Seconds > maxAwaitSeconds {
		return 0, fmt.Errorf("seconds must be between %d and %d", minAwaitSeconds, maxAwaitSeconds)
	}
	return time.Duration(*input.Seconds) * time.Second, nil
}

func validateAwaitContinuation(action string) error {
	if action == "" || action == "agent/status" {
		return nil
	}
	return fmt.Errorf("await continuation %q is not an allowlisted read-only action", action)
}

func (s *Server) awaitWithContinuation(ctx context.Context, duration time.Duration, action string, raw json.RawMessage) (awaitResult, error) {
	result, err := awaitDuration(ctx, duration)
	if err != nil {
		return awaitResult{}, err
	}
	if action == "" {
		return result, nil
	}
	continuation, err := s.agentStatusContinuationProjection(ctx, raw, awaitTailLines(duration))
	if err != nil {
		return awaitResult{}, err
	}
	result.Continuation = continuation
	return result, nil
}

func awaitTailLines(duration time.Duration) int {
	lines := int(math.Ceil(duration.Seconds() / 3))
	if lines < minAwaitTailLines {
		return minAwaitTailLines
	}
	if lines > maxAwaitTailLines {
		return maxAwaitTailLines
	}
	return lines
}

func awaitDuration(ctx context.Context, duration time.Duration) (awaitResult, error) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		finished := time.Now().UTC()
		return awaitResult{
			FinishedAt: finished.Format("15:04:05"),
		}, nil
	case <-ctx.Done():
		return awaitResult{}, ctx.Err()
	}
}
