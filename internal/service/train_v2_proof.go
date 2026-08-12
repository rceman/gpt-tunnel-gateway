package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// validateTrainV2HistoricalReportProof validates the immutable checkpoint,
// not the current lane branch. Later Train items may have advanced the same
// branch and must not invalidate an earlier finalized item review.
func (s *Service) validateTrainV2HistoricalReportProof(ctx context.Context, report model.Report, run model.Run, project config.ProjectConfig) error {
	proof, err := s.deriveMirrorRepositoryProof(ctx, run, project, report.Repository.Head)
	if err != nil {
		return err
	}
	if report.Repository.Branch != proof.Branch || report.Repository.BaseAncestor != proof.BaseAncestor || report.Repository.DiffScope != proof.DiffScope || !sameStrings(proof.ChangedFiles, report.Repository.ChangedFiles) || !sameStrings(proof.Commits, report.Repository.Commits) {
		return fmt.Errorf("Train v2 historical report proof does not match the immutable checkpoint")
	}
	return nil
}
