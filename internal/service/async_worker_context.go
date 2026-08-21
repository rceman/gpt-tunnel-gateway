package service

import (
	"context"
	"errors"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

const defaultAsyncMutationTimeout = 60 * time.Second

// asyncMutationContext is created per operation attempt. A recovered
// operation therefore gets the same bounded lifetime as a fresh operation.
func (s *Service) asyncMutationContext(action, operationID string) (context.Context, context.CancelFunc) {
	timeout := s.asyncMutationTimeout
	if timeout <= 0 {
		timeout = defaultAsyncMutationTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = runtime_log.WithAction(ctx, action)
	ctx = runtime_log.WithOperationID(ctx, operationID)
	return ctx, cancel
}

func asyncMutationOutcomeUnknown(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
