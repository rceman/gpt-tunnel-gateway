package service

import (
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) durableNow() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

func monotonicTimestamp(candidate time.Time, floors ...time.Time) time.Time {
	candidate = candidate.UTC()
	for _, floor := range floors {
		floor = floor.UTC()
		if candidate.Before(floor) {
			candidate = floor
		}
	}
	return candidate
}

func (s *Service) handoffNow(handoff model.DeliveryHandoff) time.Time {
	return monotonicTimestamp(s.durableNow(), handoff.CreatedAt, handoff.UpdatedAt)
}
