package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/onboarding"
)

type ProjectOnboardInput = onboarding.PublicInput
type ProjectOnboardResult = onboarding.PublicResult
type ProjectOnboardStatus = onboarding.StatusProjection

func (s *Service) ProjectOnboard(ctx context.Context, in ProjectOnboardInput) (ProjectOnboardResult, error) {
	return onboarding.NewPublicOrchestrator(s.Hub).Onboard(ctx, in)
}

func (s *Service) ProjectOnboardRecover(ctx context.Context, in ProjectOnboardInput) (ProjectOnboardResult, error) {
	return onboarding.NewPublicOrchestrator(s.Hub).Recover(ctx, in)
}

func (s *Service) ProjectOnboardStatus(ctx context.Context, in ProjectOnboardInput) (ProjectOnboardStatus, error) {
	return onboarding.NewPublicOrchestrator(s.Hub).Status(ctx, in)
}
