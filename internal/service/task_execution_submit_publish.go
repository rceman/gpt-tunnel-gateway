package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// taskSubmissionExpectedRemoteHead returns the remote lane head this Task's
// origin ref is durably expected to hold: the head recorded by the latest
// submission phase on the same lane branch, or "" when the lane has never
// been published.
func (s *Service) taskSubmissionExpectedRemoteHead(ctx context.Context, projectID, key, branch string) (string, error) {
	expected := ""
	expectedRevision := 0
	for _, stage := range []string{"code", "tests", "rebase"} {
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, projectID, key, stage)
		if err != nil {
			return "", err
		}
		for _, phase := range phases {
			if phase.EventKind != "submission" || phase.Branch != branch {
				continue
			}
			if phase.ExecutionRevision >= expectedRevision {
				expectedRevision = phase.ExecutionRevision
				expected = phase.Head
			}
		}
	}
	return expected, nil
}

// taskSelectorHeadIsSubmitted reports whether the selector head is a recorded
// submission artifact on this lane branch — heads that must resolve to the
// origin lane ref rather than local-only commits.
func (s *Service) taskSelectorHeadIsSubmitted(ctx context.Context, state model.TaskExecutionState, head string) (bool, error) {
	for _, stage := range []string{"code", "tests", "rebase"} {
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, state.ProjectID, state.TaskID, stage)
		if err != nil {
			return false, err
		}
		for _, phase := range phases {
			if phase.EventKind == "submission" && phase.Branch == state.Branch && phase.Head == head {
				return true, nil
			}
		}
	}
	return false, nil
}

// publishTaskSubmissionLane advances the server-owned origin lane ref to the
// exact submitted candidate through expected-old compare-and-swap semantics,
// reconciles an uncertain push outcome against the authoritative remote ref,
// and verifies the remote lane resolves to the exact commit and tree. A
// remote ref that already holds the candidate is reconciled as a landed
// publication; any other unrecorded remote head fails closed as divergence.
func (s *Service) publishTaskSubmissionLane(ctx context.Context, project, lane config.ProjectConfig, state model.TaskExecutionState, candidate, candidateTree string) error {
	expected, err := s.taskSubmissionExpectedRemoteHead(ctx, state.ProjectID, state.TaskID, state.Branch)
	if err != nil {
		return err
	}
	remote, _, err := s.Git.RemoteLaneHead(ctx, project, state.Branch)
	if err != nil {
		return err
	}
	if remote != candidate {
		if remote != expected {
			if remote == "" {
				return fmt.Errorf("origin Task lane lost its last submitted head")
			}
			// A remote head that is a strict ancestor of the candidate is
			// reconcilable: it can only be a landed publication this Gateway
			// pushed before its durable submission record, never foreign
			// history that the lease would silently overwrite.
			ancestor, ancestorErr := s.Git.IsAncestor(ctx, lane.Root, remote, candidate)
			if ancestorErr != nil || !ancestor {
				return fmt.Errorf("origin Task lane diverged from its last submitted head")
			}
		}
		if err := s.Git.PushTaskLaneCAS(ctx, project, state.Branch, remote, candidate); err != nil {
			if errors.Is(err, gitx.ErrTaskLaneExpectedOldMismatch) {
				return err
			}
			probe, _, probeErr := s.Git.RemoteLaneHead(ctx, project, state.Branch)
			if probeErr != nil || probe != candidate {
				return err
			}
		}
	}
	return s.verifyTaskSubmissionLane(ctx, project, state.Branch, candidate, candidateTree)
}

// verifyTaskSubmissionLane confirms the origin lane ref resolves to the exact
// candidate commit, materializes the remote objects, and checks that the
// remote commit carries the exact candidate tree.
func (s *Service) verifyTaskSubmissionLane(ctx context.Context, project config.ProjectConfig, branch, candidate, candidateTree string) error {
	remote, remoteExists, err := s.Git.RemoteLaneHead(ctx, project, branch)
	if err != nil {
		return err
	}
	if !remoteExists || remote != candidate {
		return fmt.Errorf("origin Task lane did not advance to the submitted head")
	}
	if err := s.Git.MaterializeRemoteBranchObjects(ctx, project, branch); err != nil {
		return err
	}
	tree, err := s.Git.CommitTree(ctx, project, candidate)
	if err != nil {
		return fmt.Errorf("origin Task lane commit is unavailable: %w", err)
	}
	if tree != candidateTree {
		return fmt.Errorf("origin Task lane tree does not match the submitted candidate")
	}
	return nil
}
