package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) resolveExactHotfixCodeTarget(ctx context.Context, projectID, selector, slug string, live bool) (localCodeTarget, error) {
	return s.resolveExactHotfixCodeTargetDetached(ctx, projectID, selector, slug, live)
}

func codeTrainBase(train model.TrainV2, currentHead string) (string, error) {
	if len(train.Items) == 0 || len(train.Items[0].Attempts) == 0 {
		if train.Status == model.TrainV2Planned {
			return currentHead, nil
		}
		return "", fmt.Errorf("managed Train %s has no canonical Shared Train base", train.ID)
	}
	base := train.Items[0].Attempts[0].StartHead
	if model.ValidateCommitSHA(base) != nil {
		return "", fmt.Errorf("managed Train %s has an invalid canonical Shared Train base", train.ID)
	}
	return base, nil
}
