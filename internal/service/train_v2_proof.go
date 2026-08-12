package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// validateTrainV2HistoricalReportProof validates the immutable checkpoint,
// not the current lane branch. Later Train items may have advanced the same
// branch and must not invalidate an earlier finalized item review.
func validateTrainV2HistoricalReportProof(report model.Report, run model.Run) error {
	proof := report.Repository
	if proof.Branch != run.Branch || proof.Head == "" || model.ValidateCommitSHA(proof.Head) != nil || !proof.WorktreeClean || !proof.BaseAncestor {
		return fmt.Errorf("Train v2 historical report proof has invalid immutable checkout identity")
	}
	if proof.DiffScope != run.BaseRevision+".."+proof.Head {
		return fmt.Errorf("Train v2 historical report proof has invalid diff scope")
	}
	if len(proof.Commits) == 0 || len(proof.ChangedFiles) == 0 {
		return fmt.Errorf("Train v2 historical report proof is incomplete")
	}
	seen := map[string]bool{}
	for _, commit := range proof.Commits {
		if model.ValidateCommitSHA(commit) != nil || seen[commit] {
			return fmt.Errorf("Train v2 historical report proof has invalid commit list")
		}
		seen[commit] = true
	}
	seen = map[string]bool{}
	for _, path := range proof.ChangedFiles {
		if err := model.ValidateRelativePath(path); err != nil || seen[path] {
			return fmt.Errorf("Train v2 historical report proof has invalid changed-file list")
		}
		seen[path] = true
	}
	return nil
}

func (s *Service) localTrainRepositoryProof(ctx context.Context, run model.Run, localRoot, branch, head string, clean bool) (model.RepositoryProof, []string, error) {
	if branch != run.Branch || !clean || model.ValidateCommitSHA(head) != nil {
		return model.RepositoryProof{}, nil, fmt.Errorf("Train lane checkout is not an exact clean branch")
	}
	ancestor, err := s.Git.IsAncestor(ctx, localRoot, run.BaseRevision, head)
	if err != nil {
		return model.RepositoryProof{}, nil, err
	}
	if !ancestor {
		return model.RepositoryProof{}, nil, fmt.Errorf("Train lane head is not descended from run execution base")
	}
	files, err := s.Git.ChangedFiles(ctx, localRoot, run.BaseRevision, head)
	if err != nil {
		return model.RepositoryProof{}, nil, err
	}
	commits, err := s.Git.LocalLog(ctx, localRoot, run.BaseRevision, head, s.Config.MaxListItems)
	if err != nil {
		return model.RepositoryProof{}, nil, err
	}
	ids := make([]string, 0, len(commits))
	for _, commit := range commits {
		ids = append(ids, commit.SHA)
	}
	return model.RepositoryProof{Branch: branch, Head: head, WorktreeClean: clean, BaseAncestor: true, Commits: ids, ChangedFiles: files, DiffScope: run.BaseRevision + ".." + head}, nil, nil
}
