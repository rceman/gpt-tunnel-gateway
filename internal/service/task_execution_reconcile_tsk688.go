package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// TSK688 extends the transition-only reconciliation to the three MIL1
// prerequisite Tasks whose real integrated executions predate the current
// authoring model — GTW-TSK589 (unified durable PLAW message authority),
// GTW-TSK594 (role-aware agent/task guides), and GTW-TSK593 (autonomous Lead
// Track orchestration). GTW-JRN18 is the shared Planner authorization; each
// Task additionally requires a post-JRN18 Lead review/gate journal plus its
// task-specific live semantic proof.
const (
	tsk589TaskID             = "GTW-TSK589"
	tsk589TaskRevision       = 6
	tsk589IntegratedRevision = 14
	tsk589CompletedRevision  = 15
	tsk589MainBase           = "176486810df9450bdfeff4910e988b8572d6fee3"
	tsk589ImplCommit         = "f09d0834d125362d63e1d12b361ad34c63e50d8d"
	tsk589ImplTree           = "083eef7c8fbe27ae2d094cbb843573da406f6ca5"
	tsk589FirstHead          = "ef8d24a3bb054e1e58e3aab7ac9dd7484f9ea9e3"
	tsk589SecondHead         = "efc51d9cddb38bb6b52b289c4224871b878ac81b"
	tsk589FinalHead          = "061b27f16ed9aa3a2a751b0cac222d496d1d63fc"
	tsk589Worktree           = "WT-TSK589-061b27f1"
	tsk589Branch             = "task/GTW-TSK589-task-implement-unified-durable-plaw-message-auth"
	tsk589FailedVerify       = "GTW-OPR3781"
	tsk589SucceededVerify    = "GTW-OPR3784"

	tsk594TaskID             = "GTW-TSK594"
	tsk594TaskRevision       = 18
	tsk594IntegratedRevision = 13
	tsk594CompletedRevision  = 14
	tsk594MainBase           = "490b8beb50363ee9437030a8558743ccd0c0629d"
	tsk594ImplCommit         = "3ba4bc8ab740c35cec90e0dfe70109daaa7e1032"
	tsk594ImplTree           = "733767553d0464bea7d1292244760ef7fee4eaee"
	tsk594FirstHead          = "11b41df33e0fa6be536d7177ff0be53a5c7c5e37"
	tsk594SecondHead         = "952316f202428641009a6aa6754741ed73b6ad98"
	tsk594FinalHead          = "fbf06efc415b8cb85e6e908dad4a30d816e9a61c"
	tsk594Worktree           = "WT-TSK594-fbf06efc"
	tsk594Branch             = "task/GTW-TSK594-task-add-role-aware-agent-guide-for-lead-operati"
	tsk594SucceededVerify    = "GTW-OPR3634"
	tsk594GuideRule          = "GTW-RUL7"
	tsk594GuideRuleRevision  = 5
	tsk594TaskRule           = "GTW-RUL8"
	tsk594TaskRuleRevision   = 1

	tsk593TaskID             = "GTW-TSK593"
	tsk593TaskRevision       = 6
	tsk593IntegratedRevision = 7
	tsk593CompletedRevision  = 8
	tsk593MainBase           = "3a365c92557bb3b0929d7f63f83f4721a2108d46"
	tsk593ImplCommit         = "9cff3bf19dd45ffc8b0c6452804325787653554d"
	tsk593ImplTree           = "2abb412d98873fe4ab8a1f6b52f4b05409549856"
	tsk593FinalHead          = "9036959996f3aea103ce81bd8e1b6ee3aa20eda4"
	tsk593Worktree           = "WT-TSK593-90369599"
	tsk593Branch             = "task/GTW-TSK593-task-add-autonomous-lead-track-orchestration-ove"
	tsk593SucceededVerify    = "GTW-OPR3827"
	tsk593TrackKey           = "GTW-TRK2"
	tsk593TrackRevision      = 51
	tsk593TrackReviewHead    = "9e11c28aa2985657729d243902318d49eecc6c02"
	tsk593TrackReviewTree    = "db807663c3bce0947fa579e6153e4fc94fd99e0f"
	tsk593TrackReviewDigest  = "29c44397e5a7d8bf4bec2aae0fbc8a6fbbf37dcae58d5cf09a47fa6a037750d6"
	tsk593TrackSubmittedBy   = "HOM_GTW_P_y4cu4"
	tsk593TrackTaskCount     = 29

	tsk688PlannerJournalKey = "GTW-JRN18"
	tsk688PlannerJournalSeq = uint64(18)
)

var tsk589ReconcileSpec = taskReconcileSpec{
	taskID:             tsk589TaskID,
	taskRevision:       tsk589TaskRevision,
	integratedRevision: tsk589IntegratedRevision,
	completedRevision:  tsk589CompletedRevision,
	kind:               tsk660ReconcileKind,
	reason:             "transition reconciliation of already-landed stale-authoring TSK589 per GTW-JRN18; validates the existing rev14 integrated lifecycle, the live message/* PLAW authority, and reconciles only the authoring record",
	mainBase:           tsk589MainBase,
	implCommit:         tsk589ImplCommit,
	implTree:           tsk589ImplTree,
	stateHead:          tsk589FinalHead,
	stage:              "code",
	worktree:           tsk589Worktree,
	branch:             tsk589Branch,
	phases: []taskReconcilePhaseSpec{
		{Stage: "code", Revision: 2, Status: model.TaskExecutionAwaitingReview, Head: tsk589FirstHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 3, Status: model.TaskExecutionChangesRequested, Head: tsk589FirstHead, EventKind: "rework"},
		{Stage: "code", Revision: 4, Status: model.TaskExecutionAwaitingReview, Head: tsk589SecondHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 5, Status: model.TaskExecutionReadyForVerification, Head: tsk589SecondHead, EventKind: "review", Decision: "accept"},
		{Stage: "code", Revision: 8, Status: model.TaskExecutionChangesRequested, Head: tsk589SecondHead, EventKind: "rework"},
		{Stage: "code", Revision: 9, Status: model.TaskExecutionAwaitingReview, Head: tsk589FinalHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 10, Status: model.TaskExecutionReadyForVerification, Head: tsk589FinalHead, EventKind: "review", Decision: "accept"},
		{Stage: "integration", Revision: tsk589IntegratedRevision, Status: model.TaskExecutionIntegrated, Head: tsk589ImplCommit, EventKind: "integration", Decision: "accept", CommentEmpty: true},
	},
	receipts: []taskReconcileReceiptSpec{
		{OperationID: tsk589FailedVerify, Attempt: 6, Outcome: "failed", CandidateHead: tsk589SecondHead, CandidateTree: "5ca9addf11f4aab38ee95f41f28a70526d09cf21", CodeReviewID: 415},
		{OperationID: tsk589SucceededVerify, Attempt: 11, Outcome: model.TaskExecutionVerificationSucceeded, CandidateHead: tsk589FinalHead, CandidateTree: tsk589ImplTree, CodeReviewID: 418, RequiredGates: []string{"format", "check", "test"}},
	},
	successReceipt: tsk589SucceededVerify,
	plannerKey:     tsk688PlannerJournalKey,
	plannerSeq:     tsk688PlannerJournalSeq,
	plannerValid:   tsk688PlannerAuthorizationValid,
	semantic:       proveTSK589MessageDomain,
}

var tsk594ReconcileSpec = taskReconcileSpec{
	taskID:             tsk594TaskID,
	taskRevision:       tsk594TaskRevision,
	integratedRevision: tsk594IntegratedRevision,
	completedRevision:  tsk594CompletedRevision,
	kind:               tsk660ReconcileKind,
	reason:             "transition reconciliation of already-landed stale-authoring TSK594 per GTW-JRN18; validates the existing rev13 integrated lifecycle, the current accepted RUL7/RUL8 role-aware guide projection, and reconciles only the authoring record",
	mainBase:           tsk594MainBase,
	implCommit:         tsk594ImplCommit,
	implTree:           tsk594ImplTree,
	stateHead:          tsk594FinalHead,
	stage:              "tests",
	worktree:           tsk594Worktree,
	branch:             tsk594Branch,
	phases: []taskReconcilePhaseSpec{
		{Stage: "code", Revision: 2, Status: model.TaskExecutionAwaitingReview, Head: tsk594FirstHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 3, Status: model.TaskExecutionChangesRequested, Head: tsk594FirstHead, EventKind: "review", Decision: "reject"},
		{Stage: "code", Revision: 4, Status: model.TaskExecutionAwaitingReview, Head: tsk594SecondHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 5, Status: model.TaskExecutionChangesRequested, Head: tsk594SecondHead, EventKind: "review", Decision: "reject"},
		{Stage: "code", Revision: 6, Status: model.TaskExecutionAwaitingReview, Head: tsk594FinalHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 7, Status: model.TaskExecutionDispatched, Head: tsk594FinalHead, EventKind: "review", Decision: "accept"},
		{Stage: "tests", Revision: 8, Status: model.TaskExecutionAwaitingReview, Head: tsk594FinalHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "tests", Revision: 9, Status: model.TaskExecutionReadyForVerification, Head: tsk594FinalHead, EventKind: "review", Decision: "accept"},
		{Stage: "integration", Revision: tsk594IntegratedRevision, Status: model.TaskExecutionIntegrated, Head: tsk594ImplCommit, EventKind: "integration", Decision: "accept", CommentEmpty: true},
	},
	receipts: []taskReconcileReceiptSpec{{
		OperationID: tsk594SucceededVerify, Attempt: 10, Outcome: model.TaskExecutionVerificationSucceeded,
		CandidateHead: tsk594FinalHead, CandidateTree: tsk594ImplTree, CodeReviewID: 255, TestsReviewID: 257, RequiredGates: []string{"format", "check", "test"},
	}},
	successReceipt: tsk594SucceededVerify,
	plannerKey:     tsk688PlannerJournalKey,
	plannerSeq:     tsk688PlannerJournalSeq,
	plannerValid:   tsk688PlannerAuthorizationValid,
	semantic:       proveTSK594GuidePolicy,
}

var tsk593ReconcileSpec = taskReconcileSpec{
	taskID:             tsk593TaskID,
	taskRevision:       tsk593TaskRevision,
	integratedRevision: tsk593IntegratedRevision,
	completedRevision:  tsk593CompletedRevision,
	kind:               tsk660ReconcileKind,
	reason:             "transition reconciliation of already-landed stale-authoring TSK593 per GTW-JRN18; validates the existing rev7 integrated lifecycle, the accepted TRK2 autonomous Lead orchestration evidence, and reconciles only the authoring record",
	mainBase:           tsk593MainBase,
	implCommit:         tsk593ImplCommit,
	implTree:           tsk593ImplTree,
	stateHead:          tsk593FinalHead,
	stage:              "code",
	worktree:           tsk593Worktree,
	branch:             tsk593Branch,
	phases: []taskReconcilePhaseSpec{
		{Stage: "code", Revision: 2, Status: model.TaskExecutionAwaitingReview, Head: tsk593FinalHead, EventKind: "submission", CommentEmpty: true},
		{Stage: "code", Revision: 3, Status: model.TaskExecutionReadyForVerification, Head: tsk593FinalHead, EventKind: "review", Decision: "accept"},
		{Stage: "integration", Revision: tsk593IntegratedRevision, Status: model.TaskExecutionIntegrated, Head: tsk593ImplCommit, EventKind: "integration", Decision: "accept", CommentEmpty: true},
	},
	receipts: []taskReconcileReceiptSpec{{
		OperationID: tsk593SucceededVerify, Attempt: 4, Outcome: model.TaskExecutionVerificationSucceeded,
		CandidateHead: tsk593FinalHead, CandidateTree: tsk593ImplTree, CodeReviewID: 450, RequiredGates: []string{"format", "check", "test"},
	}},
	successReceipt: tsk593SucceededVerify,
	plannerKey:     tsk688PlannerJournalKey,
	plannerSeq:     tsk688PlannerJournalSeq,
	plannerValid:   tsk688PlannerAuthorizationValid,
	semantic:       proveTSK593TrackOrchestration,
}

// tsk688PlannerAuthorizationValid pins GTW-JRN18 — the Planner authorization
// covering all three stale-authoring reconciliations — by exact content.
func tsk688PlannerAuthorizationValid(data taskBootstrapPlannerJournalData) bool {
	want := taskBootstrapPlannerJournalData{
		Summary: "Planner reviewed the remaining MIL1 blocker chain after TRK2 acceptance. GTW-TSK589, GTW-TSK594 and GTW-TSK593 have stale planned authoring records but canonical execution state already reports integrated; their capabilities are live and exercised. They should be historically completed/reconciled, not reimplemented.",
		Decisions: []string{
			"Do not redispatch or reimplement TSK589, TSK594 or TSK593 merely because their authoring records still say planned.",
			"Use canonical historical completion/reconciliation if it can honestly bind the existing integrated execution evidence.",
			"TSK589 is represented by the live message/* PLAW domain; exact selector naming follows current ADR83/action contracts rather than stale pre-hard-cut field wording.",
			"TSK594 is represented by the live role-aware agent/guide policy. RUL7 was repaired to rev5 so its bounded projection is valid and preserves current Lead autonomy and supervision semantics.",
			"TSK593 capability has been exercised by the completed TRK2 run: Lead autonomously selected eligible Tasks, dispatched/supervised Worker, reviewed/reworked, verified, integrated, continued, submitted the Track, and stopped for Planner acceptance.",
			"After these historical prerequisites are reconciled, GTW-TSK434 is the only substantive MIL1 implementation/proof Task remaining.",
		},
		Commitments: []string{
			"Final TSK434 clean-room proof must use current Procedures/Hooks/Track acceptance and no transition/debug bypass.",
			"Do not revive superseded project/activate or project/release semantics; current activate_local/release_prod Procedures are authoritative.",
			"Do not fabricate missing execution receipts; historical completion may only use existing truthful integrated state.",
		},
		Facts: []string{
			"task/status reports TSK589 execution_revision 14 integrated at head 061b27f1.",
			"task/status reports TSK594 execution_revision 13 integrated at head fbf06efc.",
			"task/status reports TSK593 execution_revision 7 integrated at head 90369599.",
			"message/* domain is live with create/read/list/cancel.",
			"agent/guide now projects accepted GTW-RUL7 rev5 successfully.",
			"GTW-TRK2 rev51 is accepted after autonomous Lead execution of 29 Tasks.",
		},
		Assumptions: []string{},
		Blockers:    []string{},
		Unresolved:  []string{},
		NextActions: []string{
			"Attempt canonical task/complete historical reconciliation for TSK589, TSK594 and TSK593.",
			"If canonical historical completion cannot represent their existing integrated executions, create one bounded transition reconciliation Task rather than reimplementing them.",
			"Then execute GTW-TRK3 / TSK434 clean-room MIL1 exit proof.",
		},
		References: []string{"GTW-TSK589", "GTW-TSK594", "GTW-TSK593", "GTW-TSK434", "GTW-RUL7", "GTW-TRK2"},
	}
	return sameTSK678PlannerJournalData(data, want)
}

// proveTSK589MessageDomain verifies that the live message/* PLAW capability
// TSK589 delivered exists under the current durable model: the bounded list
// authority answers, and the read record carries the cancelled_by/cancelled_at
// fields the accepted lifecycle requires.
func proveTSK589MessageDomain(ctx context.Context, s *Service, _ config.ProjectConfig, _ string) error {
	// Compile-time proof the durable Message read record carries the
	// cancelled_at/cancelled_by fields TSK589 added.
	var marker model.Message
	_ = marker.CancelledBy
	_ = marker.CancelledAt
	if _, err := s.MessageList(ctx, MessageListInput{ProjectID: config.GTWProjectID}); err != nil {
		return fmt.Errorf("live message/* authority does not answer for %s: %w", config.GTWProjectID, err)
	}
	return nil
}

// proveTSK594GuidePolicy verifies that the current agent and task guides are
// bound to and validly project the accepted GTW-RUL7/GTW-RUL8 policy with
// Lead autonomy and Planner semantic authority intact.
func proveTSK594GuidePolicy(ctx context.Context, s *Service, _ config.ProjectConfig, _ string) error {
	rule, values, bound, err := s.ProjectGuideRead(ctx, config.GTWProjectID, "agent")
	if err != nil {
		return fmt.Errorf("agent guide projection is not available: %w", err)
	}
	if !bound || rule.ID != tsk594GuideRule || rule.Revision != tsk594GuideRuleRevision {
		return fmt.Errorf("agent guide is not bound to accepted %s rev%d", tsk594GuideRule, tsk594GuideRuleRevision)
	}
	// Verified live RUL7 rev5 projection: role_authority and delegation name
	// both roles; authority and checkpoints name only one.
	for _, field := range []string{"role_authority", "delegation"} {
		if text := values[field]; !strings.Contains(text, "Lead") || !strings.Contains(text, "Planner") {
			return fmt.Errorf("agent guide %s no longer projects the accepted role-aware policy", field)
		}
	}
	if !strings.Contains(values["authority"], "Planner") {
		return fmt.Errorf("agent guide authority no longer projects the accepted role-aware policy")
	}
	if !strings.Contains(values["checkpoints"], "Lead") {
		return fmt.Errorf("agent guide checkpoints no longer projects the accepted role-aware policy")
	}
	taskRule, taskValues, bound, err := s.ProjectGuideRead(ctx, config.GTWProjectID, "task")
	if err != nil {
		return fmt.Errorf("task guide projection is not available: %w", err)
	}
	if !bound || taskRule.ID != tsk594TaskRule || taskRule.Revision != tsk594TaskRuleRevision {
		return fmt.Errorf("task guide is not bound to accepted %s rev%d", tsk594TaskRule, tsk594TaskRuleRevision)
	}
	if !strings.Contains(taskValues["workflow"], "Lead") {
		return fmt.Errorf("task guide workflow no longer projects the accepted role-aware policy")
	}
	return nil
}

// proveTSK593TrackOrchestration verifies the durable evidence that Lead
// autonomously executed a multi-Task Track under the current orchestration
// semantics: accepted GTW-TRK2 rev51, whose review binds every member and
// whose dispatched_tasks record the autonomous Lead execution log.
func proveTSK593TrackOrchestration(ctx context.Context, s *Service, project config.ProjectConfig, canonicalMain string) error {
	// The stored record is the durable evidence: canonical main may have
	// moved past the review snapshot, which makes the derived status stale
	// without invalidating the accepted rev51 record.
	track, err := s.trackReadStored(ctx, config.GTWProjectID, tsk593TrackKey, 0)
	if err != nil {
		return fmt.Errorf("Track orchestration evidence is unavailable: %w", err)
	}
	if err := validateTSK593TrackEvidence(track); err != nil {
		return err
	}
	landed, err := s.Git.IsAncestor(ctx, project.Root, tsk593TrackReviewHead, canonicalMain)
	if err != nil {
		return err
	}
	if !landed {
		return fmt.Errorf("%s accepted head is not an ancestor of canonical main", tsk593TrackKey)
	}
	return nil
}

// validateTSK593TrackEvidence pins the durable TRK2 orchestration evidence:
// accepted at rev51, the review binds the exact acceptance head/tree/digest
// and every member, and dispatched_tasks records the autonomous Lead run.
func validateTSK593TrackEvidence(track model.Track) error {
	if track.Status != model.TrackAccepted || track.Revision != tsk593TrackRevision {
		return fmt.Errorf("%s is not the accepted rev%d orchestration evidence", tsk593TrackKey, tsk593TrackRevision)
	}
	review := track.Review
	if review == nil || review.Head != tsk593TrackReviewHead || review.Tree != tsk593TrackReviewTree || review.Digest != tsk593TrackReviewDigest || review.TrackRevision != tsk593TrackRevision || review.SubmittedBy != tsk593TrackSubmittedBy {
		return fmt.Errorf("%s review does not bind the authorized acceptance", tsk593TrackKey)
	}
	if len(track.Tasks) != tsk593TrackTaskCount || len(track.DispatchedTasks) != tsk593TrackTaskCount || !slices.Equal(sortedStrings(track.Tasks), sortedStrings(track.DispatchedTasks)) {
		return fmt.Errorf("%s does not carry complete autonomous Lead dispatch evidence", tsk593TrackKey)
	}
	if len(review.Tasks) != tsk593TrackTaskCount {
		return fmt.Errorf("%s review does not bind every member", tsk593TrackKey)
	}
	return nil
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return result
}
