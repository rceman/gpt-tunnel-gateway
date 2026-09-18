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
		action.AuthorityRole = "planner"
		if action.Path == "task/review" || action.Path == "task/block" || action.Path == "task/resume" || action.Path == "task/refresh" {
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
		AuthorityRole:   actionRoleWorkflow,
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

const taskGuideWorkflow = "Use immutable revisioned Tasks composed under Planner WHAT/WHY authority; Planner owns architecture, Task/Track scope, acceptance, dependencies/priority, and final Track semantic review, and is not the dispatch, supervision, review, test, or integration proxy. Lead owns HOW: dispatch, Worker supervision, technical review/rework, verification, integration, and continuation. Milestone Track is the Planner-to-Lead delegation unit, not an Agent/Worker/ad-hoc queue or a Wave; Track order is planning intent, not FIFO, and Lead weighs membership, dependencies, priority, status, and Worker availability. ADR138 role-permissive runtime does not transfer semantic authority between PLAW roles or make Worker-owned submit actions Lead-owned."
const taskGuideReview = "Lead performs technical review and rework at each stage: call task/review, then use its worktree and base with code/diff. Inspect only relevant files through targeted code/read, code/tree, or code/search calls; do not substitute broad worktree inventory. Code compares the dispatch base to the submitted candidate. Tests compare the accepted production head to the tests candidate. Rebase compares the accepted prior artifact to the rebased candidate; any changed artifact requires review and reverification. Lead may run authorized non-final staging, disposable E2E, or preflight, including focused post-Task integration checks after risky Tasks, subsets, or Track end; final project activate/release waits for source-bound Planner Track review."
const taskGuideVerification = "Tests are a distinct submitted artifact and review, not an assertion embedded in code review. After accepted code and tests, plus any required rebase review, Lead runs controlled asynchronous Task verification and integrates only verified evidence. Evidence goes through Track handoff, Journal, comments, or the standing evidence channels TSK609/619/632/633; there is no direct Lead-to-Planner channel, and owner/operator relay is only for semantic blockers or completed Track handoff, not execution proxy. ADR72 Gates 1-20, including the Gates 9/12/14/19/20 public-response evidence requirements, are the sole gate taxonomy. Canonical drift requires controlled rebase, renewed review where artifacts changed, and fresh verification."
const taskGuideCompletion = "A verified integration moves the Task to integrated and pending Planner acceptance, not done. Any multiple Workers are assigned at dispatch. Planner acceptance uses one concise same-project task-review Journal reference plus the applicable integration proof in task/complete; exact candidate, verification, integration and Task revision facts remain in their authoritative receipts. Completion and archive are status-only lifecycle events at the unchanged content revision; task/read(revision) remains immutable content while task/history includes lifecycle events."
const taskGuideBoundaries = "Lead owns lifecycle decisions but never hand-mutates Task lanes or canonical source via shell Git; canonical Task actions own the mechanics. Lead never proxies a Worker submit or impersonates a Session, and never creates or updates Planner-owned Tasks, Tracks, ADRs, or Rules. Planner owns acceptance, completion, and historical bootstrap. Agents submit assigned-lane artifacts only through the fixed CLI gpt-tunnel task submit-code|submit-tests|submit-rebase, never native MCP, and cannot choose bases, refs, or paths outside bounded inspection, or fabricate reviews or verification. bootstrap_full proves the live assigned candidate from its frozen base and full gates; legacy historical recognition cannot manufacture missing evidence."

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
