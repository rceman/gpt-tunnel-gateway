package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type TaskExecutionReviewInput struct {
	ProjectID string
	Key       string
	Stage     string
}

type TaskExecutionReviewDecisionInput struct {
	ProjectID string
	Key       string
	Stage     string
	Decision  string
	Comment   string
}

type TaskExecutionReworkInput struct {
	ProjectID string
	Key       string
	Stage     string
	Comment   string
}

type TaskExecutionReviewOutput struct {
	Key               string `json:"key"`
	Stage             string `json:"stage"`
	Status            string `json:"status"`
	Worktree          string `json:"worktree"`
	Head              string `json:"head"`
	Agent             string `json:"agent"`
	ExecutionRevision int    `json:"execution_revision"`
	SubmittedAt       string `json:"submitted_at"`
}

func (s *Service) TaskExecutionSubmitCode(ctx context.Context, projectID, key string) (TaskExecutionPublicOutput, error) {
	return s.submitTaskExecution(ctx, projectID, key, "code")
}

func (s *Service) TaskExecutionSubmitTests(ctx context.Context, projectID, key string) (TaskExecutionPublicOutput, error) {
	return s.submitTaskExecution(ctx, projectID, key, "tests")
}

func (s *Service) TaskExecutionSubmitRebase(ctx context.Context, projectID, key string) (TaskExecutionPublicOutput, error) {
	return s.submitTaskExecution(ctx, projectID, key, "rebase")
}

func (s *Service) submitTaskExecution(ctx context.Context, projectID, key, stage string) (TaskExecutionPublicOutput, error) {
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, projectID, key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has not been dispatched")
	}
	if state.Stage != stage || (state.Status != model.TaskExecutionDispatched && state.Status != model.TaskExecutionChangesRequested) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task is not accepting a %s submission", stage)
	}
	resolved, err := s.ResolveAgent(ctx, AgentResolveInput{
		ProjectID:       projectID,
		Role:            model.AgentRoleCoding,
		AgentID:         state.Agent,
		RequireAttached: true,
	})
	if err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("assigned Agent is not usable: %w", err)
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && resolved.SessionKey != sessionID {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task is assigned to a different Agent session")
	}
	actual, branch, clean, err := s.taskExecutionLaneHead(ctx, projectID, key, state)
	if err != nil || !clean || branch != state.Branch {
		return TaskExecutionPublicOutput{}, fmt.Errorf("assigned Task worktree must be clean on its server-owned branch")
	}
	now := time.Now().UTC()
	state.Head = actual
	state.Status = model.TaskExecutionAwaitingReview
	state.ExecutionRevision++
	state.UpdatedAt = now
	phase := sqlitestore.TaskExecutionPhase{TaskID: key, ProjectID: projectID, ExecutionRevision: state.ExecutionRevision, Stage: stage, Status: state.Status, Head: actual, Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256, EventKind: "submission", CreatedAt: now}
	if stage != "code" {
		previousStage := "code"
		if stage == "rebase" {
			previousStage = "tests"
		}
		accepted, acceptedFound, acceptedErr := s.Durability.ReadLatestTaskExecutionPhase(ctx, projectID, key, previousStage)
		if acceptedErr != nil || !acceptedFound || accepted.Decision != "accept" || accepted.EventKind != "review" {
			if acceptedErr != nil {
				return TaskExecutionPublicOutput{}, acceptedErr
			}
			return TaskExecutionPublicOutput{}, fmt.Errorf("accepted %s submission is required", previousStage)
		}
		lanePath, pathErr := gitx.TaskWorktreePath(s.Config.StateDir, projectID, key)
		if pathErr != nil {
			return TaskExecutionPublicOutput{}, pathErr
		}
		ancestor, ancestorErr := s.Git.IsAncestor(ctx, lanePath, accepted.Head, actual)
		if ancestorErr != nil || !ancestor {
			return TaskExecutionPublicOutput{}, fmt.Errorf("%s submission must descend from accepted %s head", stage, previousStage)
		}
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	return taskExecutionPublicOutput(state), nil
}

func (s *Service) TaskExecutionReview(ctx context.Context, in TaskExecutionReviewInput) (TaskExecutionReviewOutput, error) {
	if err := validateTaskExecutionReviewInput(in.ProjectID, in.Key, in.Stage); err != nil {
		return TaskExecutionReviewOutput{}, err
	}
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionReviewOutput{}, err
		}
		return TaskExecutionReviewOutput{}, fmt.Errorf("Task has no execution state")
	}
	phase, found, err := s.Durability.ReadLatestTaskExecutionPhase(ctx, in.ProjectID, in.Key, in.Stage)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionReviewOutput{}, err
		}
		return TaskExecutionReviewOutput{}, fmt.Errorf("Task has no %s submission", in.Stage)
	}
	if phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 || phase.Status != model.TaskExecutionAwaitingReview {
		return TaskExecutionReviewOutput{}, fmt.Errorf("Task review is stale")
	}
	if len(phase.Head) < 8 {
		return TaskExecutionReviewOutput{}, fmt.Errorf("Task review has invalid head authority")
	}
	return taskExecutionReviewOutput(state, phase), nil
}

func (s *Service) TaskExecutionReviewDecide(ctx context.Context, in TaskExecutionReviewDecisionInput) (TaskExecutionPublicOutput, error) {
	if err := validateTaskExecutionReviewInput(in.ProjectID, in.Key, in.Stage); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if in.Decision != "accept" && in.Decision != "reject" {
		return TaskExecutionPublicOutput{}, fmt.Errorf("review decision must be accept or reject")
	}
	if strings.TrimSpace(in.Comment) != "" && utf8.RuneCountInString(in.Comment) > 1024 {
		return TaskExecutionPublicOutput{}, fmt.Errorf("review comment is too long")
	}
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	phase, found, err := s.Durability.ReadLatestTaskExecutionPhase(ctx, in.ProjectID, in.Key, in.Stage)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no %s submission", in.Stage)
	}
	if phase.Decision != "" {
		if phase.Decision == in.Decision && phase.Comment == strings.TrimSpace(in.Comment) {
			return taskExecutionPublicOutput(state), nil
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("contradictory review decision")
	}
	if state.Status != model.TaskExecutionAwaitingReview || state.Stage != in.Stage {
		return TaskExecutionPublicOutput{}, fmt.Errorf("review decision is stale")
	}
	now := time.Now().UTC()
	state.ExecutionRevision++
	state.UpdatedAt = now
	if in.Decision == "reject" {
		state.Status = model.TaskExecutionChangesRequested
	} else if in.Stage == "code" {
		state.Stage, state.Status = "tests", model.TaskExecutionDispatched
	} else {
		state.Status = model.TaskExecutionReadyForIntegration
	}
	phase = sqlitestore.TaskExecutionPhase{TaskID: in.Key, ProjectID: in.ProjectID, ExecutionRevision: state.ExecutionRevision, Stage: in.Stage, Status: state.Status, Head: state.Head, Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256, EventKind: "review", Decision: in.Decision, Comment: strings.TrimSpace(in.Comment), CreatedAt: now}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	return taskExecutionPublicOutput(state), nil
}

func (s *Service) TaskExecutionRework(ctx context.Context, in TaskExecutionReworkInput) (TaskExecutionPublicOutput, error) {
	if err := validateTaskExecutionReviewInput(in.ProjectID, in.Key, in.Stage); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if strings.TrimSpace(in.Comment) == "" || utf8.RuneCountInString(in.Comment) > 1024 {
		return TaskExecutionPublicOutput{}, fmt.Errorf("rework comment is required and bounded")
	}
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	if (in.Stage == "code" && state.Stage != "code" && state.Stage != "tests" && state.Stage != "rebase") || (in.Stage == "tests" && state.Stage != "tests" && state.Stage != "rebase") || (in.Stage == "rebase" && state.Stage != "rebase") {
		return TaskExecutionPublicOutput{}, fmt.Errorf("rework stage is stale")
	}
	if state.Status == model.TaskExecutionChangesRequested {
		return taskExecutionPublicOutput(state), nil
	}
	now := time.Now().UTC()
	state.Stage = in.Stage
	state.Status = model.TaskExecutionChangesRequested
	state.ExecutionRevision++
	state.UpdatedAt = now
	phase := sqlitestore.TaskExecutionPhase{TaskID: in.Key, ProjectID: in.ProjectID, ExecutionRevision: state.ExecutionRevision, Stage: in.Stage, Status: state.Status, Head: state.Head, Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256, EventKind: "rework", Comment: strings.TrimSpace(in.Comment), CreatedAt: now}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	return taskExecutionPublicOutput(state), nil
}

func validateTaskExecutionReviewInput(projectID, key, stage string) error {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	if err := model.ValidateCanonicalTaskID(key); err != nil {
		return err
	}
	if stage != "code" && stage != "tests" && stage != "rebase" {
		return fmt.Errorf("invalid review stage")
	}
	return nil
}

func (s *Service) readExecutionForMutation(ctx context.Context, projectID, key string) (model.TaskExecutionState, bool, error) {
	if s.Durability == nil {
		return model.TaskExecutionState{}, false, fmt.Errorf("shared durability is unavailable")
	}
	return s.Durability.ReadTaskExecutionState(ctx, projectID, key)
}

func (s *Service) taskExecutionLaneHead(ctx context.Context, projectID, key string, state model.TaskExecutionState) (string, string, bool, error) {
	path, err := gitx.TaskWorktreePath(s.Config.StateDir, projectID, key)
	if err != nil {
		return "", "", false, err
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return "", "", false, err
	}
	project.Root = path
	return s.Git.CurrentHead(ctx, project)
}

func taskExecutionReviewOutput(state model.TaskExecutionState, phase sqlitestore.TaskExecutionPhase) TaskExecutionReviewOutput {
	return TaskExecutionReviewOutput{
		Key:               state.TaskID,
		Stage:             phase.Stage,
		Status:            phase.Status,
		Worktree:          state.Worktree,
		Head:              strings.ToLower(phase.Head[:8]),
		Agent:             state.Agent,
		ExecutionRevision: phase.ExecutionRevision,
		SubmittedAt:       phase.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}
