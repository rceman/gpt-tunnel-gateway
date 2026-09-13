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

func taskExecutionAgentInputSchema() map[string]any {
	return obj(map[string]any{})
}

func taskExecutionReviewOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "stage": outputEnum("code", "tests", "rebase"), "status": outputString(), "worktree": outputString(), "base": taskExecutionPublicHeadSchema(), "head": taskExecutionPublicHeadSchema(), "agent": outputString(), "execution_revision": outputInteger(), "submitted_at": outputDateTime()}, "key", "stage", "status", "worktree", "base", "head", "agent", "execution_revision", "submitted_at")
}

func (s *Server) registerTaskExecutionReviewActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = "planner"
		if action.Path == "task/review" {
			action.AuthorityRole = actionRolePlannerOrLead
		}
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
		{path: "task/submit-code", description: "Submit the assigned Task worktree for code review.", serviceCall: func(ctx context.Context, projectID string) (service.TaskExecutionPublicOutput, error) {
			return s.Service.TaskExecutionSubmitCodeForAgent(ctx, projectID)
		}},
		{path: "task/submit-tests", description: "Submit the assigned Task worktree for tests review.", serviceCall: func(ctx context.Context, projectID string) (service.TaskExecutionPublicOutput, error) {
			return s.Service.TaskExecutionSubmitTestsForAgent(ctx, projectID)
		}},
		{path: "task/submit-rebase", description: "Submit the assigned Task worktree for rebase review.", serviceCall: func(ctx context.Context, projectID string) (service.TaskExecutionPublicOutput, error) {
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
	return s.RegisterGenericAction(GenericAction{
		Path:                 "task/guide",
		Description:          "Read the builtin Task execution workflow guide.",
		InputSchema:          taskGuideSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskGuideSchema()),
		OutputSchema:         taskGuideOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRolePlannerOrManagedRuntime,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			_ = in.ProjectID
			return map[string]any{"source": "builtin", "workflow": taskGuideWorkflow, "review": taskGuideReview, "verification": taskGuideVerification, "completion": taskGuideCompletion, "boundaries": taskGuideBoundaries}, nil
		},
	})
}

const taskGuideWorkflow = "Use immutable revisioned Tasks under Planner authority. Freeze acceptance and scope before dispatch; an Agent works only in its server-owned assigned lane. Review code, then separate tests, then rebase when canonical main moved. Accept each immutable submission before verification. Integrate only verified evidence; integration is pending acceptance, not completion."
const taskGuideReview = "For each stage call task/review, then use its worktree and base with code/diff. Inspect only relevant files through targeted code/read, code/tree, or code/search calls; do not substitute broad worktree inventory. Code compares the dispatch base to the submitted candidate. Tests compare the accepted production head to the tests candidate. Rebase compares the accepted prior artifact to the rebased candidate; any changed artifact requires review and reverification."
const taskGuideVerification = "Tests are a distinct submitted artifact and review, not an assertion embedded in code review. After accepted code and tests, plus any required rebase review, use controlled asynchronous Task verification. Canonical drift requires controlled rebase, renewed review where artifacts changed, and fresh verification; stale receipts, prose, Agent claims, or caller-selected commits are not authority."
const taskGuideCompletion = "A verified integration moves the Task to integrated and pending Planner acceptance, not done. Planner acceptance requires immutable same-project Journal evidence and the applicable integration proof before task/complete. Completion and archive are status-only lifecycle events at the unchanged content revision; task/read(revision) remains immutable content while task/history includes lifecycle events."
const taskGuideBoundaries = "Planner alone decides reviews, integration recognition, acceptance, completion, and historical bootstrap. Agents may submit assigned-lane artifacts but cannot choose execution bases, comparison refs, canonical refs, paths outside bounded code inspection, or fabricate reviews or verification. bootstrap_full proves the live assigned candidate from its frozen base and full gates; legacy historical recognition is distinct and cannot manufacture missing evidence."

func taskGuideSchema() map[string]any {
	return obj(map[string]any{})
}

func taskGuideOutputSchema() map[string]any {
	source := outputString()
	source["const"] = "builtin"
	bounded := func() map[string]any {
		s := outputString()
		s["minLength"] = 1
		s["maxLength"] = 768
		return s
	}
	return closedOutput(map[string]any{"source": source, "workflow": bounded(), "review": bounded(), "verification": bounded(), "completion": bounded(), "boundaries": bounded()}, "source", "workflow", "review", "verification", "completion", "boundaries")
}
