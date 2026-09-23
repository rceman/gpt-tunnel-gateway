package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func taskExecutionReviewSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "stage": outputEnum("code", "tests", "rebase")}, "key", "stage")
}

func taskExecutionReviewDecisionSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "stage": outputEnum("code", "tests", "rebase"), "decision": outputEnum("accept", "reject"), "comment": str("Optional bounded review comment.")}, "key", "stage", "decision")
}

func taskExecutionReworkSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "stage": outputEnum("code", "tests", "rebase"), "comment": str("Required bounded rework comment.")}, "key", "stage", "comment")
}

func taskExecutionParkSchema() map[string]any {
	reason := str("Required bounded block or resume reason.")
	reason["minLength"], reason["maxLength"] = 1, 1024
	return obj(map[string]any{"key": str("Canonical Task identifier."), "reason": reason}, "key", "reason")
}

func taskExecutionAgentInputSchema() map[string]any {
	return obj(map[string]any{})
}

func taskExecutionParkOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "status": outputString(), "stage": outputString(), "worktree": outputString(), "head": taskExecutionPublicHeadSchema(), "agent": outputString(), "execution_revision": outputInteger(), "reason": outputString(), "updated_at": outputDateTime()}, "key", "status", "stage", "worktree", "head", "agent", "execution_revision", "reason")
}

func taskExecutionReviewOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "stage": outputEnum("code", "tests", "rebase"), "status": outputString(), "worktree": outputString(), "base": taskExecutionPublicHeadSchema(), "head": taskExecutionPublicHeadSchema(), "agent": outputString(), "execution_revision": outputInteger(), "submitted_at": outputDateTime()}, "key", "stage", "status", "worktree", "base", "head", "agent", "execution_revision", "submitted_at")
}

func (s *Server) registerTaskExecutionReviewActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = durableSession.RoleLead
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:                 "task/review",
		Description:          "Read the latest immutable Task execution submission for review.",
		InputSchema:          taskExecutionReviewSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionReviewSchema()),
		OutputSchema:         taskExecutionReviewOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Stage     string `json:"stage"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionReview(ctx, service.TaskExecutionReviewInput{ProjectID: in.ProjectID, Key: in.Key, Stage: in.Stage})
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/review_decide",
		Description:          "Accept or reject one immutable Task execution review.",
		InputSchema:          taskExecutionReviewDecisionSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionReviewDecisionSchema()),
		OutputSchema:         taskExecutionLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Stage     string `json:"stage"`
				Decision  string `json:"decision"`
				Comment   string `json:"comment,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionReviewDecide(ctx, service.TaskExecutionReviewDecisionInput{ProjectID: in.ProjectID, Key: in.Key, Stage: in.Stage, Decision: in.Decision, Comment: in.Comment})
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/rework",
		Description:          "Reopen one Task execution stage with a bounded Planner comment.",
		InputSchema:          taskExecutionReworkSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionReworkSchema()),
		OutputSchema:         taskExecutionLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Stage     string `json:"stage"`
				Comment   string `json:"comment"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionRework(ctx, service.TaskExecutionReworkInput{ProjectID: in.ProjectID, Key: in.Key, Stage: in.Stage, Comment: in.Comment})
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/refresh",
		Description:          "Refresh one safe Task lane onto the exact current canonical main.",
		InputSchema:          taskExecutionParkSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionParkSchema()),
		OutputSchema:         taskExecutionParkOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Reason    string `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionRefresh(ctx, service.TaskExecutionRefreshInput{ProjectID: in.ProjectID, Key: in.Key, Reason: in.Reason})
		},
	}); err != nil {
		return err
	}
	for _, action := range []struct {
		path        string
		description string
		call        func(context.Context, service.TaskExecutionBlockInput) (service.TaskExecutionPublicOutput, error)
	}{
		{path: "task/block", description: "Durably block one Worker-actionable Task lane without changing its authority.", call: func(ctx context.Context, in service.TaskExecutionBlockInput) (service.TaskExecutionPublicOutput, error) {
			return s.Service.TaskExecutionBlock(ctx, in)
		}},
		{path: "task/resume", description: "Resume one durably blocked Task lane after a bounded Planner decision.", call: func(ctx context.Context, in service.TaskExecutionBlockInput) (service.TaskExecutionPublicOutput, error) {
			return s.Service.TaskExecutionResume(ctx, service.TaskExecutionResumeInput{ProjectID: in.ProjectID, Key: in.Key, Reason: in.Reason})
		}},
	} {
		park := action
		if err := register(GenericAction{
			Path:                 park.path,
			Description:          park.description,
			InputSchema:          taskExecutionParkSchema(),
			ExecutionInputSchema: adrExecutionSchema(taskExecutionParkSchema()),
			OutputSchema:         taskExecutionParkOutputSchema(),
			Annotations: ToolAnnotations{
				DestructiveHint: true,
				IdempotentHint:  true,
			},
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in struct {
					ProjectID string `json:"project_id"`
					Key       string `json:"key"`
					Reason    string `json:"reason"`
				}
				if err := decode(raw, &in); err != nil {
					return nil, err
				}
				return park.call(ctx, service.TaskExecutionBlockInput{ProjectID: in.ProjectID, Key: in.Key, Reason: in.Reason})
			},
		}); err != nil {
			return err
		}
	}
	registerAgent := func(action GenericAction) error {
		action.AuthorityRole = durableSession.RoleWorker
		action.SessionBound = true
		action.SessionRequired = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	for _, action := range []struct {
		path        string
		description string
		serviceCall func(context.Context, string) (service.TaskExecutionPublicOutput, error)
	}{
		{path: "task/current", description: "Read the current execution state for the assigned canonical Task.", serviceCall: func(ctx context.Context, projectID string) (service.TaskExecutionPublicOutput, error) {
			return s.Service.TaskExecutionCurrent(ctx, projectID)
		}},
		{path: "task/submit-code", description: "Submit the assigned Task worktree's combined production and test candidate for Lead review.", serviceCall: func(ctx context.Context, projectID string) (service.TaskExecutionPublicOutput, error) {
			return s.Service.TaskExecutionSubmitCodeForAgent(ctx, projectID)
		}},
		{path: "task/submit-rebase", description: "Submit the assigned Task worktree's rebased production and test candidate for Lead review.", serviceCall: func(ctx context.Context, projectID string) (service.TaskExecutionPublicOutput, error) {
			return s.Service.TaskExecutionSubmitRebaseForAgent(ctx, projectID)
		}},
	} {
		call := action.serviceCall
		if err := registerAgent(GenericAction{
			Path:                 action.path,
			Description:          action.description,
			InputSchema:          taskExecutionAgentInputSchema(),
			ExecutionInputSchema: adrExecutionSchema(taskExecutionAgentInputSchema()),
			OutputSchema:         taskExecutionLifecycleOutputSchema(),
			Annotations: ToolAnnotations{
				DestructiveHint: action.path != "task/current",
				IdempotentHint:  true,
			},
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in struct {
					ProjectID string `json:"project_id"`
				}
				if err := decode(raw, &in); err != nil {
					return nil, err
				}
				return call(ctx, in.ProjectID)
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

const taskGuideWorkflow = "Planner owns durable WHAT/WHY: architecture, ADR/Task/Rule and Milestone/Track composition, scope, acceptance, dependencies/priority, final Track review, and executable-work curation. Planner is not the dispatch, Worker-supervision, technical-review, test, or integration proxy. Lead owns ordinary Task dispatch, Worker supervision, technical review/rework, verification, integration, continuation, Track submission, and Task lifecycle mechanics without changing Planner-owned semantics. Worker implements the assigned Task and makes one production+tests submit-code handoff."
const taskGuideReview = "Planner delegates one Track through durable MSG carrying only its key. Ordered membership is planning intent, not FIFO. Lead rereads membership and live Task dependencies, priority, status/execution stage, and Worker availability; selects eligible members sequentially, reuses persistent execution after restart, never dispatches while Worker has an actionable Task, and continues without ordinary Planner round-trips. Lead reviews each submission with task/review and bounded code/read, code/tree, code/search, and code/diff."
const taskGuideVerification = "Before one submit-code handoff, Worker runs only focused/affected deterministic tests plus scripts/test-fast.py; the candidate includes production and tests. Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E. Lead performs project-required full Task verification after submission, requests bounded rework through Task actions, integrates verified evidence, rereads Track state, and continues to the next eligible member."
const taskGuideCompletion = "Server derives Track readiness; Lead calls track/submit and stops for Planner track/accept. Planner accepts with a concise same-project planner-notes journal reference and applicable integration proof in task/complete. Candidate, verification, integration, and Task revision facts remain in authoritative receipts. Completion and archive are status-only lifecycle events at unchanged content revision; task/read(revision) is immutable content while task/history includes lifecycle events."
const taskGuideBoundaries = "Planner owns semantic decisions and final Track acceptance. Lead never mutates Planner-owned semantics, creates or updates Planner-owned Tasks, Tracks, ADRs, or Rules, or proxies Worker implementation or submission. Durable MSG is only for genuine semantic, public-contract, security, persistence, or scope blockers and completed Track handoff; there is no ordinary Lead-to-Planner channel. Evidence uses canonical journal/* actions; journal/contract is the sole stream-rules authority, and ADR72 Gates 1-20 are the sole review taxonomy. Final project activation/release waits for source-bound Planner Track review. Keep diagnostics and retries bounded."

func taskGuideSchema() map[string]any {
	return obj(map[string]any{})
}

func taskGuideOutputSchema() map[string]any {
	return projectGuideOutputSchema("task")
}
