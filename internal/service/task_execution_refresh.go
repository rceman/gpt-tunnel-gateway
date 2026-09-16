package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type TaskExecutionRefreshInput struct {
	ProjectID string
	Key       string
	Reason    string
}

type taskExecutionRefreshEvidence struct {
	OldBase            string `json:"old_base"`
	NewBase            string `json:"new_base"`
	OldHead            string `json:"old_head"`
	NewHead            string `json:"new_head"`
	Reason             string `json:"reason"`
	ExecutionRevision  int    `json:"execution_revision"`
	Stage              string `json:"stage"`
	Status             string `json:"status"`
	Branch             string `json:"branch"`
	TaskRevisionSHA256 string `json:"task_revision_sha256"`
}

func decodeTaskExecutionRefreshEvidence(comment string) (taskExecutionRefreshEvidence, error) {
	var evidence taskExecutionRefreshEvidence
	if err := json.Unmarshal([]byte(comment), &evidence); err != nil ||
		model.ValidateCommitSHA(evidence.OldBase) != nil ||
		model.ValidateCommitSHA(evidence.NewBase) != nil ||
		model.ValidateCommitSHA(evidence.OldHead) != nil ||
		model.ValidateCommitSHA(evidence.NewHead) != nil ||
		evidence.OldBase != evidence.OldHead ||
		evidence.NewBase != evidence.NewHead ||
		evidence.ExecutionRevision < 1 ||
		(evidence.Stage != "code" && evidence.Stage != "tests" && evidence.Stage != "rebase") ||
		!validTaskExecutionRefreshStatus(evidence.Status) ||
		model.ValidateBranch(evidence.Branch) != nil ||
		model.ValidateSHA256(evidence.TaskRevisionSHA256) != nil ||
		strings.TrimSpace(evidence.Reason) == "" ||
		len([]rune(evidence.Reason)) > taskExecutionBlockReasonLimit {
		return taskExecutionRefreshEvidence{}, fmt.Errorf("invalid Task refresh evidence")
	}
	return evidence, nil
}

func validTaskExecutionRefreshStatus(status string) bool {
	switch status {
	case model.TaskExecutionDispatched, model.TaskExecutionInProgress, model.TaskExecutionAwaitingReview, model.TaskExecutionChangesRequested, model.TaskExecutionReadyForVerification, model.TaskExecutionVerifying, model.TaskExecutionVerified, model.TaskExecutionIntegrating, model.TaskExecutionIntegrated, model.TaskExecutionDone, model.TaskExecutionBlocked, model.TaskExecutionFailed:
		return true
	default:
		return false
	}
}

func (s *Service) TaskExecutionRefresh(ctx context.Context, in TaskExecutionRefreshInput) (TaskExecutionPublicOutput, error) {
	reason, err := validateTaskExecutionRefreshInput(in)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	if err := s.ensureTaskExecutionRefreshable(ctx, state); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	canonical, err := s.resolveTaskExecutionCanonicalMain(ctx, project)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	lane, err := s.taskExecutionLane(in.ProjectID, in.Key, state)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	actual, branch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || branch != state.Branch {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task lane is not clean on its server-owned branch")
	}
	if actual != state.Head && (actual != canonical || state.BaseHead == canonical) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task lane does not match durable or canonical refresh identity")
	}
	if actual == state.Head && state.BaseHead == canonical {
		out := taskExecutionPublicOutput(state)
		out.Reason = reason
		return out, nil
	}
	recovered := actual == canonical && actual != state.Head
	ancestorRoot := project.Root
	if !recovered {
		if err := s.Git.MaterializeMirrorCommit(ctx, project, project.DefaultBranch, canonical); err != nil {
			return TaskExecutionPublicOutput{}, fmt.Errorf("materialize canonical Task refresh base: %w", err)
		}
	} else {
		ancestorRoot = lane.Root
	}
	ancestor, err := s.Git.IsAncestor(ctx, ancestorRoot, state.BaseHead, canonical)
	if err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("verify canonical Task refresh ancestry: %w", err)
	}
	if !ancestor {
		return TaskExecutionPublicOutput{}, fmt.Errorf("canonical main diverged from the Task base")
	}
	if !recovered {
		newHead, reconcileErr := s.Git.ReconcileTaskLane(ctx, lane, canonical, state.BaseHead)
		if reconcileErr != nil {
			return TaskExecutionPublicOutput{}, fmt.Errorf("refresh Task lane: %w", reconcileErr)
		}
		if newHead != canonical {
			return TaskExecutionPublicOutput{}, fmt.Errorf("refreshed Task lane did not reach canonical main")
		}
		if s.taskExecutionRefreshFaultHook != nil {
			if hookErr := s.taskExecutionRefreshFaultHook(ctx, "after_lane_reconcile"); hookErr != nil {
				return TaskExecutionPublicOutput{}, hookErr
			}
		}
		actual, branch, clean, err = s.Git.CurrentHead(ctx, lane)
		if err != nil || !clean || branch != state.Branch || actual != canonical {
			return TaskExecutionPublicOutput{}, fmt.Errorf("refreshed Task lane failed identity validation")
		}
	}
	nextRevision := state.ExecutionRevision + 1
	evidence, err := json.Marshal(taskExecutionRefreshEvidence{
		OldBase:            state.BaseHead,
		NewBase:            canonical,
		OldHead:            state.Head,
		NewHead:            actual,
		Reason:             reason,
		ExecutionRevision:  nextRevision,
		Stage:              state.Stage,
		Status:             state.Status,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
	})
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	state.BaseHead = canonical
	state.Head = actual
	state.Worktree = taskExecutionWorktree(in.Key, strings.ToLower(actual[:8]))
	state.ExecutionRevision = nextRevision
	state.UpdatedAt = time.Now().UTC()
	phase := sqlitestore.TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  state.ExecutionRevision,
		Stage:              state.Stage,
		Status:             state.Status,
		Head:               state.Head,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          "refresh",
		Comment:            string(evidence),
		CreatedAt:          state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	out := taskExecutionPublicOutput(state)
	out.Reason = reason
	return out, nil
}

func validateTaskExecutionRefreshInput(in TaskExecutionRefreshInput) (string, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return "", err
	}
	if err := model.ValidateCanonicalTaskID(in.Key); err != nil {
		return "", err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > taskExecutionBlockReasonLimit {
		return "", fmt.Errorf("Task refresh reason is required and bounded")
	}
	return reason, nil
}

func (s *Service) ensureTaskExecutionRefreshable(ctx context.Context, state model.TaskExecutionState) error {
	switch state.Status {
	case model.TaskExecutionDispatched, model.TaskExecutionInProgress:
	case model.TaskExecutionBlocked:
		if _, err := s.taskExecutionBlockedPhase(ctx, state); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Task execution is not at a safe refresh boundary")
	}
	if state.Head != state.BaseHead {
		return fmt.Errorf("Task execution has progressed beyond its base")
	}
	if err := s.ensureNoInFlightWorkerTurn(ctx, state); err != nil {
		return err
	}
	for _, stage := range []string{"code", "tests", "rebase", "integration"} {
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, state.ProjectID, state.TaskID, stage)
		if err != nil {
			return fmt.Errorf("inspect Task refresh evidence: %w", err)
		}
		for _, phase := range phases {
			if phase.EventKind != "block" && phase.EventKind != "resume" && phase.EventKind != "refresh" {
				return fmt.Errorf("Task execution has immutable lifecycle evidence")
			}
		}
		if _, err := validateTaskExecutionRefreshChain(state, phases); err != nil {
			return err
		}
	}
	verification, found, err := s.Durability.ReadLatestTaskExecutionVerification(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return fmt.Errorf("inspect Task verification evidence: %w", err)
	}
	if found || verification.TaskID != "" {
		return fmt.Errorf("Task execution has immutable verification evidence")
	}
	return nil
}

func validateTaskExecutionRefreshChain(state model.TaskExecutionState, phases []sqlitestore.TaskExecutionPhase) (taskExecutionRefreshEvidence, error) {
	var latest taskExecutionRefreshEvidence
	foundRefresh := false
	for i, phase := range phases {
		if phase.EventKind != "refresh" {
			continue
		}
		evidence, err := decodeTaskExecutionRefreshEvidence(phase.Comment)
		if err != nil || phase.ExecutionRevision != evidence.ExecutionRevision || phase.Stage != evidence.Stage || phase.Status != evidence.Status || phase.Branch != evidence.Branch || phase.TaskRevisionSHA256 != evidence.TaskRevisionSHA256 || phase.Head != evidence.NewHead {
			return taskExecutionRefreshEvidence{}, fmt.Errorf("Task refresh evidence does not match its phase")
		}
		var priorBase, priorHead string
		wantRevision := 2
		if i > 0 {
			previous := phases[i-1]
			wantRevision = previous.ExecutionRevision + 1
			switch previous.EventKind {
			case "block", "resume":
				priorBase, priorHead = previous.Head, previous.Head
			case "refresh":
				previousEvidence, previousErr := decodeTaskExecutionRefreshEvidence(previous.Comment)
				if previousErr != nil {
					return taskExecutionRefreshEvidence{}, fmt.Errorf("Task refresh chain has invalid prior evidence")
				}
				priorBase, priorHead = previousEvidence.NewBase, previousEvidence.NewHead
			default:
				return taskExecutionRefreshEvidence{}, fmt.Errorf("Task refresh chain has an invalid prior phase")
			}
		}
		if (i > 0 && (evidence.OldBase != priorBase || evidence.OldHead != priorHead)) || phase.ExecutionRevision != wantRevision || evidence.NewBase != evidence.NewHead || evidence.NewBase != phase.Head {
			return taskExecutionRefreshEvidence{}, fmt.Errorf("Task refresh chain is not monotonic")
		}
		latest = evidence
		foundRefresh = true
	}
	if !foundRefresh {
		return taskExecutionRefreshEvidence{}, nil
	}
	if latest.NewBase != state.BaseHead || latest.NewHead != state.Head || latest.Stage != state.Stage || latest.Branch != state.Branch || latest.TaskRevisionSHA256 != state.TaskRevisionSHA256 {
		return taskExecutionRefreshEvidence{}, fmt.Errorf("Task refresh evidence does not bind the current execution")
	}
	return latest, nil
}

func (s *Service) resolveTaskExecutionCanonicalMain(ctx context.Context, project config.ProjectConfig) (string, error) {
	canonical, err := s.Git.RefreshDefaultBranch(ctx, project)
	if err != nil {
		return "", fmt.Errorf("resolve canonical main: %w", err)
	}
	return canonical, nil
}
