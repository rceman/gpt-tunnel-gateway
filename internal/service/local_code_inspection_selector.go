package service

import (
	"context"
)

func (s *Service) resolveExactHotfixCodeTarget(ctx context.Context, projectID, selector, slug string, live bool) (localCodeTarget, error) {
	return s.resolveExactHotfixCodeTargetDetached(ctx, projectID, selector, slug, live)
}
