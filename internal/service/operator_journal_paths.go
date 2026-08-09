package service

import (
	"context"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

func isOperatorHubConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "hub_revision_conflict") || strings.Contains(message, "non-fast-forward") || strings.Contains(message, "fetch first") || strings.Contains(message, "resource temporarily unavailable")
}

func (s *Service) operatorTransact(ctx context.Context, expected, subject string, mutate hub.Mutator) (hub.TransactionResult, error) {
	attempts := 1
	if expected == "" {
		attempts = allocatorRetryLimit
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := s.Hub.Transact(ctx, expected, subject, mutate)
		if err == nil {
			return result, nil
		}
		last = err
		if expected != "" || !isOperatorHubConflict(err) || attempt+1 == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return hub.TransactionResult{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return hub.TransactionResult{}, last
}
