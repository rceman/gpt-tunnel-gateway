package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) registerTrainV2ActionSet3() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/attempt-finalize",
		Description:  "Finalize one exact TrainItem Attempt without creating a global Run.",
		InputSchema:  trainV2AttemptFinalizeSchema(),
		OutputSchema: trainV2AttemptFinalizeReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: "planner",
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AttemptFinalizeInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2AttemptFinalizeAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/correction-start",
		Description:  "Start one exact queued Train correction after a rejected immutable review.",
		InputSchema:  trainV2CorrectionStartSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    "planner",
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2CorrectionStartInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2CorrectionStartAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/attempt-review",
		Description:  "Publish review for one exact successful TrainItem Attempt.",
		InputSchema:  trainV2AttemptReviewSchema(),
		OutputSchema: trainV2AttemptReviewReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: "planner",
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AttemptReviewInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2AttemptReviewAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/review-resolve",
		Description:  "Record Planner-owned resolution evidence for an exact rejected Train review.",
		InputSchema:  trainV2ReviewResolveSchema(),
		OutputSchema: trainV2ReviewResolutionOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: durableSession.RolePlanner,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2ReviewResolveInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := s.boundTrainProject(ctx)
			if err != nil {
				return nil, err
			}
			in.ProjectID = projectID
			return s.Service.TrainV2ReviewResolve(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/review-backfill",
		Description:  "Dry-run or atomically backfill accepted reviews for immutable pre-review Train Attempts.",
		InputSchema:  trainV2ReviewBackfillSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    "planner",
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2ReviewBackfillInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2ReviewBackfillAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	return nil
}
