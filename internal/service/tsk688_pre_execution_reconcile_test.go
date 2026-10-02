package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

var tsk688Specs = []taskReconcileSpec{tsk589ReconcileSpec, tsk594ReconcileSpec, tsk593ReconcileSpec}

func tsk688Task(spec taskReconcileSpec) model.TaskAuthoring {
	return model.TaskAuthoring{
		ID: spec.taskID, ProjectID: config.GTWProjectID,
		Revision: spec.taskRevision, RevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskAuthoringPlanned,
	}
}

func tsk688Phases(spec taskReconcileSpec, task model.TaskAuthoring) taskPreExecutionPhases {
	var out taskPreExecutionPhases
	base := time.Now().UTC().Add(-24 * time.Hour)
	for index, want := range spec.phases {
		comment := ""
		if !want.CommentEmpty {
			comment = "historical lifecycle evidence"
		}
		phase := sqlitestore.TaskExecutionPhase{
			TaskID: spec.taskID, ProjectID: config.GTWProjectID,
			ExecutionRevision: want.Revision, Stage: want.Stage, Status: want.Status, Head: want.Head,
			Branch: spec.branch, TaskRevisionSHA256: task.RevisionSHA256,
			EventKind: want.EventKind, Decision: want.Decision, Comment: comment,
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}
		switch want.Stage {
		case "code":
			out.code = append(out.code, phase)
		case "tests":
			out.tests = append(out.tests, phase)
		case "rebase":
			out.rebase = append(out.rebase, phase)
		case "integration":
			out.integration = append(out.integration, phase)
		}
	}
	return out
}

func tsk688IntegratedState(spec taskReconcileSpec, task model.TaskAuthoring) model.TaskExecutionState {
	return model.TaskExecutionState{
		TaskID: spec.taskID, ProjectID: config.GTWProjectID,
		TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256,
		Status: model.TaskExecutionIntegrated, Stage: spec.stage,
		Worktree: spec.worktree, BaseHead: spec.mainBase, Head: spec.stateHead,
		Branch: spec.branch, Agent: config.GTWWorkerAgentID,
		ExecutionRevision: spec.integratedRevision, UpdatedAt: time.Now().UTC(),
	}
}

func tsk688ReceiptJSON(t *testing.T, spec taskReconcileSpec, want taskReconcileReceiptSpec, task model.TaskAuthoring, mutate func(*model.TaskExecutionVerification)) string {
	t.Helper()
	receipt := model.TaskExecutionVerification{
		ProjectID:          config.GTWProjectID,
		TaskID:             spec.taskID,
		OperationID:        want.OperationID,
		TaskRevisionSHA256: task.RevisionSHA256,
		BaseHead:           spec.mainBase,
		CandidateHead:      want.CandidateHead,
		CandidateTree:      spec.implTree,
		Branch:             spec.branch,
		GateProfileSHA256:  strings.Repeat("1", 64),
		Outcome:            want.Outcome,
		TaskRevision:       spec.taskRevision,
		AttemptRevision:    want.Attempt,
		CodeReviewID:       want.CodeReviewID,
		TestsReviewID:      want.TestsReviewID,
		RebaseReviewID:     want.RebaseReviewID,
		StartedAt:          time.Now().UTC().Add(-time.Hour),
		CompletedAt:        time.Now().UTC(),
	}
	for _, gateID := range want.RequiredGates {
		receipt.Gates = append(receipt.Gates, model.CompletionGateResult{ID: gateID, ExitCode: 0, Execution: "executed", TreeID: spec.implTree})
	}
	if want.Outcome == model.TaskExecutionVerificationFailed {
		receipt.Error = "verification failed"
	}
	if mutate != nil {
		mutate(&receipt)
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func tsk688InsertReceipt(t *testing.T, db *sqlitestore.Databases, taskID, operationID string, attempt int, outcome, receiptJSON string) {
	t.Helper()
	_, err := db.Local.Batch(context.Background(), []upstream.Statement{{
		SQL:  `INSERT INTO local_task_execution_verifications(project_id,task_id,operation_id,attempt_revision,outcome,receipt_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		Args: []any{config.GTWProjectID, taskID, operationID, attempt, outcome, receiptJSON, time.Now().UTC().Format(time.RFC3339Nano)},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func tsk688SeedReceipts(t *testing.T, db *sqlitestore.Databases, spec taskReconcileSpec, task model.TaskAuthoring) {
	t.Helper()
	for _, want := range spec.receipts {
		tsk688InsertReceipt(t, db, spec.taskID, want.OperationID, want.Attempt, want.Outcome, tsk688ReceiptJSON(t, spec, want, task, nil))
	}
}

func tsk688Journals(spec taskReconcileSpec) taskPreExecutionJournalEvidence {
	digest := strings.Repeat("b", 64)
	return taskPreExecutionJournalEvidence{
		PlannerKey:    spec.plannerKey,
		PlannerDigest: digest,
		GateKeys:      []string{"GTW-JRN20"},
		GateDigests:   []string{digest},
	}
}

// TestTSK688AllowlistIsExact proves the action accepts only the four
// authorized Tasks and rejects near-miss keys.
func TestTSK688AllowlistIsExact(t *testing.T) {
	for _, key := range reconcileAllowlist {
		if _, ok := reconcileSpecFor(key); !ok {
			t.Fatalf("authorized Task %s is not reconcilable", key)
		}
	}
	for _, key := range []string{"GTW-TSK434", "GTW-TSK677", "GTW-TSK590", "GTW-TSK595", "GTW-TSK688", "GTW-TSK5890"} {
		if _, ok := reconcileSpecFor(key); ok {
			t.Fatalf("unauthorized Task %s is reconcilable", key)
		}
	}
}

// TestTSK688PlannerAuthorizationContentIsExact pins the GTW-JRN18 shared
// authorization for all three reconciliations.
func TestTSK688PlannerAuthorizationContentIsExact(t *testing.T) {
	valid := taskBootstrapPlannerJournalData{
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
	if !tsk688PlannerAuthorizationValid(valid) {
		t.Fatal("GTW-JRN18 exact content was rejected")
	}
	drifted := valid
	drifted.Decisions = append(append([]string(nil), valid.Decisions...), "Also reconcile any other Task.")
	if tsk688PlannerAuthorizationValid(drifted) {
		t.Fatal("drifted Planner authorization was accepted")
	}
	driftedFacts := valid
	driftedFacts.Facts = append([]string(nil), valid.Facts[:len(valid.Facts)-1]...)
	if tsk688PlannerAuthorizationValid(driftedFacts) {
		t.Fatal("truncated Planner facts were accepted")
	}
}

// TestTSK688ExistingLifecycleAccepted proves each authorized spec's exact
// durable evidence — phase set, receipt set, integrated state — validates and
// reconciles into a correct completion contract.
func TestTSK688ExistingLifecycleAccepted(t *testing.T) {
	for _, spec := range tsk688Specs {
		t.Run(spec.taskID, func(t *testing.T) {
			task := tsk688Task(spec)
			phases := tsk688Phases(spec, task)
			phasesSHA, err := reconcilePhasesDigest(phases)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sqlitestore.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tsk688SeedReceipts(t, db, spec, task)
			s := &Service{
				Config:     config.Config{StateDir: t.TempDir()},
				Durability: db,
			}
			verification, err := s.readLegacyVerification(context.Background(), spec, task)
			if err != nil {
				t.Fatalf("authorized receipt set rejected: %v", err)
			}
			if verification.OperationID != spec.successReceipt || verification.CandidateHead != spec.stateHead || len(verification.ReceiptSHA256) != 64 || len(verification.ReceiptsSHA256) != 64 {
				t.Fatalf("unexpected verification proof: %#v", verification)
			}
			state := tsk688IntegratedState(spec, task)
			journals := tsk688Journals(spec)
			_, replayed, err := validateReconcileState(spec, task, state, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals)
			if err != nil {
				t.Fatalf("exact existing lifecycle rejected: %v", err)
			}
			if replayed {
				t.Fatal("fresh lifecycle reported already reconciled")
			}
			// Completion contract + replay path.
			contract, err := reconcileCompletionContract(spec, task, journals, verification, phasesSHA, strings.Repeat("d", 40))
			if err != nil {
				t.Fatal(err)
			}
			doneTask := task
			doneTask.Status = model.TaskAuthoringDone
			doneState := state
			doneState.Status = model.TaskExecutionDone
			doneState.ExecutionRevision = spec.completedRevision
			recorded := time.Now().UTC().Truncate(time.Nanosecond)
			doneState.UpdatedAt = recorded
			event := sqlitestore.TaskLifecycleEvent{
				OperationID: "task-complete-test", ProjectID: config.GTWProjectID, TaskID: spec.taskID,
				Revision: int64(spec.taskRevision), EventKind: sqlitestore.TaskLifecycleEventKindComplete,
				FromStatus: model.TaskAuthoringPlanned, ToStatus: model.TaskAuthoringDone,
				Actor: "HOM_GTW_L_test", Reason: spec.reason, Contract: contract, RecordedAt: recorded,
			}
			evidence, replayed, err := validateReconcileState(spec, doneTask, doneState, true, phases, phasesSHA, verification, event, true, false, journals)
			if err != nil {
				t.Fatalf("completed lifecycle replay rejected: %v", err)
			}
			if !replayed || evidence.TaskID != spec.taskID || evidence.ExecutionRevision != spec.integratedRevision {
				t.Fatalf("unexpected replay evidence: %#v replayed=%v", evidence, replayed)
			}
			if evidence.ExecutionStateMinted || evidence.PhaseMinted || evidence.VerificationReceiptMinted {
				t.Fatal("reconciliation contract claims minted evidence")
			}
		})
	}
}

// TestTSK688NearMissRejections covers the fail-closed mutations each spec
// must reject: wrong revision/status/head/branch, missing or extra receipts,
// and undecodable or non-passing receipt rows.
func TestTSK688NearMissRejections(t *testing.T) {
	for _, spec := range tsk688Specs {
		t.Run(spec.taskID, func(t *testing.T) {
			task := tsk688Task(spec)
			phases := tsk688Phases(spec, task)
			phasesSHA, err := reconcilePhasesDigest(phases)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sqlitestore.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tsk688SeedReceipts(t, db, spec, task)
			s := &Service{
				Config:     config.Config{StateDir: t.TempDir()},
				Durability: db,
			}
			verification, err := s.readLegacyVerification(context.Background(), spec, task)
			if err != nil {
				t.Fatal(err)
			}
			journals := tsk688Journals(spec)
			state := tsk688IntegratedState(spec, task)

			for _, mutate := range []struct {
				name  string
				state func(*model.TaskExecutionState)
			}{
				{"wrong execution revision", func(st *model.TaskExecutionState) { st.ExecutionRevision-- }},
				{"wrong status", func(st *model.TaskExecutionState) { st.Status = model.TaskExecutionVerifying }},
				{"wrong head", func(st *model.TaskExecutionState) { st.Head = strings.Repeat("9", 40) }},
				{"wrong branch", func(st *model.TaskExecutionState) { st.Branch = "task/other" }},
				{"wrong stage", func(st *model.TaskExecutionState) { st.Stage = "rebase" }},
			} {
				mutated := state
				mutate.state(&mutated)
				if _, _, err := validateReconcileState(spec, task, mutated, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
					t.Fatalf("%s accepted", mutate.name)
				}
			}
			// Missing phase row (truncate the code stage) must fail.
			truncated := phases
			truncated.code = truncated.code[:len(truncated.code)-1]
			if _, _, err := validateReconcileState(spec, task, state, true, truncated, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
				t.Fatal("truncated phase history accepted")
			}
			// Extra phase row must fail.
			extra := phases
			extra.integration = append(extra.integration, extra.integration[0])
			if _, _, err := validateReconcileState(spec, task, state, true, extra, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
				t.Fatal("extra integration phase accepted")
			}
			// Tampered authoring revision must fail.
			wrongTask := task
			wrongTask.Revision++
			if _, _, err := validateReconcileState(spec, wrongTask, state, true, phases, phasesSHA, verification, sqlitestore.TaskLifecycleEvent{}, false, false, journals); err == nil {
				t.Fatal("wrong authoring revision accepted")
			}
		})
	}
}

// TestTSK688ReceiptSetRejections proves each spec's receipt set contract:
// missing rows, extra rows, wrong operation, wrong outcome, undecodable rows,
// and non-passing gates all fail closed.
func TestTSK688ReceiptSetRejections(t *testing.T) {
	for _, spec := range tsk688Specs {
		t.Run(spec.taskID, func(t *testing.T) {
			task := tsk688Task(spec)
			cases := []struct {
				name    string
				wantErr bool
				setup   func(t *testing.T, db *sqlitestore.Databases)
			}{
				{name: "missing receipt fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
					tsk688SeedReceipts(t, db, spec, task)
					// drop the last authorized receipt
					if _, err := db.Local.Batch(context.Background(), []upstream.Statement{{
						SQL:  `DELETE FROM local_task_execution_verifications WHERE task_id=? AND operation_id=?`,
						Args: []any{spec.taskID, spec.receipts[len(spec.receipts)-1].OperationID},
					}}); err != nil {
						t.Fatal(err)
					}
				}},
				{name: "extra receipt fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
					tsk688SeedReceipts(t, db, spec, task)
					tsk688InsertReceipt(t, db, spec.taskID, "GTW-OPR9999", 99, model.TaskExecutionVerificationSucceeded, tsk688ReceiptJSON(t, spec, taskReconcileReceiptSpec{
						OperationID:   "GTW-OPR9999",
						Attempt:       99,
						Outcome:       model.TaskExecutionVerificationSucceeded,
						CandidateHead: spec.stateHead,
						CodeReviewID:  1,
						RequiredGates: []string{"format", "check", "test"},
					}, task, func(r *model.TaskExecutionVerification) { r.OperationID = "GTW-OPR9999" }))
				}},
				{name: "undecodable receipt fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
					tsk688SeedReceipts(t, db, spec, task)
					if _, err := db.Local.Batch(context.Background(), []upstream.Statement{{
						SQL:  `UPDATE local_task_execution_verifications SET receipt_json=? WHERE task_id=? AND operation_id=?`,
						Args: []any{"{not-json", spec.taskID, spec.successReceipt},
					}}); err != nil {
						t.Fatal(err)
					}
				}},
				{name: "failing gate on succeeded receipt fails", wantErr: true, setup: func(t *testing.T, db *sqlitestore.Databases) {
					tsk688SeedReceipts(t, db, spec, task)
					row := tsk688ReceiptJSON(t, spec, spec.receipts[len(spec.receipts)-1], task, func(r *model.TaskExecutionVerification) { r.Gates[0].ExitCode = 1 })
					if _, err := db.Local.Batch(context.Background(), []upstream.Statement{{
						SQL:  `UPDATE local_task_execution_verifications SET receipt_json=? WHERE task_id=? AND operation_id=?`,
						Args: []any{row, spec.taskID, spec.successReceipt},
					}}); err != nil {
						t.Fatal(err)
					}
				}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					db, err := sqlitestore.Open(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					tc.setup(t, db)
					s := &Service{
						Config:     config.Config{StateDir: t.TempDir()},
						Durability: db,
					}
					if _, err := s.readLegacyVerification(context.Background(), spec, task); tc.wantErr != (err != nil) {
						t.Fatalf("receipt set validation mismatch: err=%v", err)
					}
				})
			}
		})
	}
}

// tsk688GuideFixture returns the semantic guide text TSK594 requires: each
// projected field names the Lead authority and the Planner boundary.
func tsk688GuideFields(authority string) map[string]string {
	fields, ok := model.GuideProjectionFields("agent")
	if !ok {
		panic("agent guide projection is unavailable")
	}
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		values[field] = authority
	}
	return values
}

// tsk688SeedGuideAuthority installs the Shared configuration binding agent→
// GTW-RUL7 and task→GTW-RUL8 plus the two accepted guide Rules.
func tsk688SeedGuideAuthority(t *testing.T, db *sqlitestore.Databases, agentValues map[string]string, taskValues map[string]string, agentRevision int) {
	t.Helper()
	ctx := context.Background()
	configuration := model.DefaultProjectConfiguration(config.GTWProjectID, time.Now())
	configuration.GuideBindings = map[string]string{"agent": tsk594GuideRule, "task": tsk594TaskRule}
	configPayload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rulePayload := func(id, name string, revision int, value json.RawMessage) []byte {
		raw, err := json.Marshal(model.Rule{
			SchemaVersion: model.SchemaVersion, ID: id, ProjectID: config.GTWProjectID,
			Revision: revision, Title: "Guide " + name, Status: model.RuleStatusAccepted,
			Name: name, Value: value,
			CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	agentValue, err := json.Marshal(agentValues)
	if err != nil {
		t.Fatal(err)
	}
	taskValue, err := json.Marshal(taskValues)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{config.GTWProjectID, configuration.Revision, configPayload, now.Format(time.RFC3339Nano)}},
		{SQL: `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{tsk594GuideRule, agentRevision, rulePayload(tsk594GuideRule, "guide.agent", agentRevision, agentValue), now.Format(time.RFC3339Nano)}},
		{SQL: `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{tsk594TaskRule, tsk594TaskRuleRevision, rulePayload(tsk594TaskRule, "guide.task", tsk594TaskRuleRevision, taskValue), now.Format(time.RFC3339Nano)}},
	}); err != nil {
		t.Fatal(err)
	}
}

func tsk688GuideService(t *testing.T, db *sqlitestore.Databases) *Service {
	t.Helper()
	dir := t.TempDir()
	return &Service{
		Config: config.Config{StateDir: t.TempDir(), Projects: map[string]config.ProjectConfig{
			config.GTWProjectID: {Root: filepath.Join(dir, "root"), Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "gtw"},
		}},
		Durability: db,
	}
}

// TestTSK594GuidePolicyProof verifies the semantic proof accepts the accepted
// RUL7/RUL8 projections and rejects stale/unbound/removed-authority guides.
func TestTSK594GuidePolicyProof(t *testing.T) {
	agentText := "Lead owns dispatch, review, verification and integration; Planner owns architecture, scope, acceptance and final Track review."
	taskFields, _ := model.GuideProjectionFields("task")
	taskValues := make(map[string]string, len(taskFields))
	for _, field := range taskFields {
		taskValues[field] = "Lead executes the Task workflow under Planner authority."
	}
	t.Run("accepted RUL7/RUL8 projection passes", func(t *testing.T) {
		db, err := sqlitestore.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		tsk688SeedGuideAuthority(t, db, tsk688GuideFields(agentText), taskValues, tsk594GuideRuleRevision)
		if err := proveTSK594GuidePolicy(context.Background(), tsk688GuideService(t, db), config.ProjectConfig{}, ""); err != nil {
			t.Fatalf("accepted guide projection rejected: %v", err)
		}
	})
	t.Run("stale guide Rule revision fails", func(t *testing.T) {
		db, err := sqlitestore.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		tsk688SeedGuideAuthority(t, db, tsk688GuideFields(agentText), taskValues, tsk594GuideRuleRevision+1)
		if err := proveTSK594GuidePolicy(context.Background(), tsk688GuideService(t, db), config.ProjectConfig{}, ""); err == nil {
			t.Fatal("stale guide revision was accepted")
		}
	})
	t.Run("guide without Lead authority fails", func(t *testing.T) {
		db, err := sqlitestore.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		tsk688SeedGuideAuthority(t, db, tsk688GuideFields("Generic guidance with no role ownership."), taskValues, tsk594GuideRuleRevision)
		if err := proveTSK594GuidePolicy(context.Background(), tsk688GuideService(t, db), config.ProjectConfig{}, ""); err == nil {
			t.Fatal("guide missing Lead/Planner authority was accepted")
		}
	})
	t.Run("missing bindings fail", func(t *testing.T) {
		db, err := sqlitestore.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := proveTSK594GuidePolicy(context.Background(), tsk688GuideService(t, db), config.ProjectConfig{}, ""); err == nil {
			t.Fatal("unbound guide projection was accepted")
		}
	})
}

// tsk688TrackFixture builds the accepted TRK2 orchestration evidence shape.
func tsk688TrackFixture() model.Track {
	tasks := make([]string, tsk593TrackTaskCount)
	reviewTasks := make([]model.TrackTaskSnapshot, tsk593TrackTaskCount)
	for i := range tasks {
		tasks[i] = fmt.Sprintf("GTW-TSK%d", 600+i)
		reviewTasks[i] = model.TrackTaskSnapshot{Key: tasks[i], Revision: 1, RevisionSHA256: strings.Repeat("c", 64)}
	}
	return model.Track{
		SchemaVersion: model.SchemaVersion, ID: tsk593TrackKey, ProjectID: config.GTWProjectID,
		Revision: tsk593TrackRevision, Milestone: "GTW-MIL1", Title: "TRK2",
		Tasks: tasks, DispatchedTasks: append([]string(nil), tasks...), Status: model.TrackAccepted,
		Review: &model.TrackReview{
			Head: tsk593TrackReviewHead, Tree: tsk593TrackReviewTree, Digest: tsk593TrackReviewDigest,
			TrackRevision: tsk593TrackRevision, Tasks: reviewTasks,
			SubmittedAt: time.Now().UTC(), SubmittedBy: tsk593TrackSubmittedBy,
		},
		CreatedBy: "planner", CreatedAt: time.Now().UTC(), UpdatedBy: "planner", UpdatedAt: time.Now().UTC(),
	}
}

// TestTSK593TrackEvidenceProof pins the durable autonomous-orchestration
// evidence shape: accepted rev, bound review, and complete dispatch log.
func TestTSK593TrackEvidenceProof(t *testing.T) {
	if err := validateTSK593TrackEvidence(tsk688TrackFixture()); err != nil {
		t.Fatalf("authorized TRK2 evidence rejected: %v", err)
	}
	for name, mutate := range map[string]func(*model.Track){
		"wrong revision":       func(tr *model.Track) { tr.Revision = tsk593TrackRevision - 1 },
		"not accepted":         func(tr *model.Track) { tr.Status = model.TrackReviewPending },
		"wrong review head":    func(tr *model.Track) { tr.Review.Head = strings.Repeat("9", 40) },
		"wrong review digest":  func(tr *model.Track) { tr.Review.Digest = strings.Repeat("9", 64) },
		"missing dispatch log": func(tr *model.Track) { tr.DispatchedTasks = nil },
		"incomplete dispatch":  func(tr *model.Track) { tr.DispatchedTasks = tr.DispatchedTasks[:5] },
		"unbound member":       func(tr *model.Track) { tr.Review.Tasks = tr.Review.Tasks[:5] },
		"nil review":           func(tr *model.Track) { tr.Review = nil },
	} {
		track := tsk688TrackFixture()
		mutate(&track)
		if err := validateTSK593TrackEvidence(track); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// TestTSK589MessageDomainProof verifies the live capability proof requires an
// authenticated PLAW Session and answers for the GTW project.
func TestTSK589MessageDomainProof(t *testing.T) {
	db, err := sqlitestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Service{
		Config:     config.Config{StateDir: t.TempDir()},
		Durability: db,
	}
	if err := proveTSK589MessageDomain(context.Background(), s, config.ProjectConfig{}, ""); err == nil {
		t.Fatal("message domain proof passed without an authenticated Session")
	}
	ref := "lead-ref"
	record, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
		ProjectID: config.GTWProjectID, ProjectCode: "GTW", Role: durableSession.RoleLead, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAgentSessionID(context.Background(), record.ID)
	if err := proveTSK589MessageDomain(ctx, s, config.ProjectConfig{}, ""); err != nil {
		t.Fatalf("live message/* authority rejected: %v", err)
	}
}
