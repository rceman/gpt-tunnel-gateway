package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestProcedureExecutionUsesBoundedStructuredEnvelope(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	script := procedureTestScript(t, root, "#!/bin/sh\ngrep -q '\"project\":\"example\"' \"$GTW_PROCEDURE_INPUT_FILE\" || exit 31\ntest -n \"$GTW_PROCEDURE_OUTPUT_FILE\" || exit 32\nprintf '{\"ok\":true}' > \"$GTW_PROCEDURE_OUTPUT_FILE\"\n")
	definition := model.ProjectProcedureDefinition{
		Script:  script,
		Summary: "Check the project context",
		Guide:   "Validates that the project identity is provided as structured input.",
		Input: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"project": map[string]any{"$ref": "EntityKeyAndReference"}},
			"required":             []any{"project"},
			"additionalProperties": false,
		},
		Output: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
			"required":             []any{"ok"},
			"additionalProperties": false,
		},
	}
	created, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "structured_check",
		Definition: definition,
		Reason:     "Add the structured Procedure test.",
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ConfigProcedureList(ctx, "example")
	if err != nil || catalog.ConfigurationRevision != created.Revision || len(catalog.Procedures) != 1 || catalog.Procedures[0].Name != "structured_check" {
		t.Fatalf("authoritative Procedure catalogue=%#v err=%v", catalog, err)
	}
	stored, err := s.ConfigProcedureRead(ctx, "example", "structured_check")
	if err != nil || stored.Definition.Script != script {
		t.Fatalf("stored Procedure definition=%#v err=%v", stored, err)
	}
	receipt, err := s.ProcedureExecutionStart(ctx, ProcedureExecutionStartInput{
		ProjectID:             "example",
		Name:                  "structured_check",
		Definition:            definition,
		ConfigurationRevision: created.Revision,
		Input:                 json.RawMessage(`{"project":"example"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.Operation)
	var output map[string]any
	if err := json.Unmarshal(operation.Result, &output); err != nil || operation.Kind != procedureExecutionKind || operation.Status != "completed" || output["ok"] != true {
		t.Fatalf("Procedure Operation=%#v output=%#v err=%v", operation, output, err)
	}
}

func TestProcedureExecutionRunsNonGoProjectScript(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "scripts")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(directory, "procedure_echo.py")
	script := "scripts/procedure_echo.py"
	body := "#!" + python + "\nimport json, os\nwith open(os.environ['GTW_PROCEDURE_INPUT_FILE'], encoding='utf-8') as source:\n    envelope = json.load(source)\nwith open(os.environ['GTW_PROCEDURE_OUTPUT_FILE'], 'w', encoding='utf-8') as target:\n    json.dump({'echo': envelope['input']['text']}, target)\n"
	if err := os.WriteFile(scriptPath, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	definition := model.ProjectProcedureDefinition{
		Script: script, Summary: "Echo structured input from Python.", Guide: "Returns the text field as structured JSON.",
		Input: map[string]any{
			"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}},
			"required": []any{"text"}, "additionalProperties": false,
		},
		Output: map[string]any{
			"type": "object", "properties": map[string]any{"echo": map[string]any{"type": "string"}},
			"required": []any{"echo"}, "additionalProperties": false,
		},
	}
	created, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "python_echo",
		Definition: definition,
		Reason:     "Verify non-Go Procedure script execution.",
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ProcedureExecutionStart(ctx, ProcedureExecutionStartInput{
		ProjectID:             "example",
		Name:                  "python_echo",
		Definition:            definition,
		ConfigurationRevision: created.Revision,
		Input:                 json.RawMessage(`{"text":"portable"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.Operation)
	var output map[string]any
	if err := json.Unmarshal(operation.Result, &output); err != nil || operation.Status != "completed" || output["echo"] != "portable" {
		t.Fatalf("Python Procedure Operation=%#v output=%#v err=%v", operation, output, err)
	}
}

func TestProcedureExecutionRejectsInvalidStructuredOutput(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	script := procedureTestScript(t, root, "#!/bin/sh\nprintf '{\"ok\":\"not boolean\"}' > \"$GTW_PROCEDURE_OUTPUT_FILE\"\n")
	definition := model.ProjectProcedureDefinition{
		Script: script, Summary: "Return invalid structured output.", Guide: "Returns a value outside the declared result contract.",
		Input: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Output: map[string]any{
			"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
			"required": []any{"ok"}, "additionalProperties": false,
		},
	}
	created, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "invalid_output",
		Definition: definition,
		Reason:     "Test structured output validation.",
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ProcedureExecutionStart(ctx, ProcedureExecutionStartInput{
		ProjectID:             "example",
		Name:                  "invalid_output",
		Definition:            definition,
		ConfigurationRevision: created.Revision,
		Input:                 json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.Operation)
	if operation.Status != "outcome_unknown" || operation.Error == "" {
		t.Fatalf("invalid Procedure output was treated as a completed result: %#v", operation)
	}
}

func TestProcedureExecutionRejectsRetiredOrEscapingPaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideScript := filepath.Join(outside, "outside.sh")
	if err := os.WriteFile(outsideScript, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideScript, filepath.Join(root, "scripts", "escape.sh")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside.sh", outsideScript, "scripts/escape.sh", "scripts//check.sh", `scripts\\check.sh`} {
		if _, _, err := resolveProcedureScript(root, path); err == nil {
			t.Errorf("unsafe Procedure path %q was accepted", path)
		}
	}
}

func TestAgentWorkFinishedHookAllocatesOneObservableOperation(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	script := procedureTestScript(t, root, "#!/bin/sh\ngrep -q '\"hook\":\"post_agent_work_finished\"' \"$GTW_PROCEDURE_INPUT_FILE\" || exit 41\nprintf '{}' > \"$GTW_PROCEDURE_OUTPUT_FILE\"\n")
	definition := workFinishedProcedureDefinition()
	definition.Script = script
	definition.Input = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"epoch":   map[string]any{"type": "string", "minLength": 47, "maxLength": 47},
			"project": map[string]any{"$ref": "EntityKeyAndReference"},
		},
		"required":             []any{"epoch", "project"},
		"additionalProperties": false,
	}
	created, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "finished_notice",
		Definition: definition,
		Reason:     "Add Agent-work notification.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigHookBind(ctx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPostAgentWorkFinished,
		Procedure: "finished_notice",
		Reason:    "Bind Agent-work notification.",
	}); err != nil {
		t.Fatal(err)
	}
	epochID, err := newAgentWorkEpochID()
	if err != nil {
		t.Fatal(err)
	}
	armedAt := time.Now().UTC()
	if err := s.Durability.ArmCallbackEpoch(ctx, sqlitestore.CallbackEpoch{ID: epochID, ProjectID: "example", AgentID: "agent-one", SessionID: AgentSessionID(ctx), SessionKey: "agent-session", ArmedAt: armedAt}); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.Durability.ObserveCallbackEpoch(ctx, epochID, "running"); err != nil || ready {
		t.Fatalf("busy observation ready=%v err=%v", ready, err)
	}
	if ready, err := s.Durability.ObserveCallbackEpoch(ctx, epochID, "idle"); err != nil || ready {
		t.Fatalf("first idle observation ready=%v err=%v", ready, err)
	}
	if ready, err := s.Durability.ObserveCallbackEpoch(ctx, epochID, "idle"); err != nil || !ready {
		t.Fatalf("stable idle observation ready=%v err=%v", ready, err)
	}
	epoch, err := s.Durability.ReadCallbackEpoch(ctx, epochID)
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchAgentWorkFinishedHook(ctx, epoch)
	epoch, err = s.Durability.ReadCallbackEpoch(ctx, epochID)
	if err != nil || epoch.OperationID == "" || epoch.Outcome != "pending" {
		t.Fatalf("Agent-work epoch allocation=%#v err=%v", epoch, err)
	}
	operation := tsk585WaitOperation(t, s, epoch.OperationID)
	if operation.Kind != procedureExecutionKind || operation.Status != "completed" {
		t.Fatalf("Agent-work Hook Operation=%#v", operation)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		epoch, err = s.Durability.ReadCallbackEpoch(ctx, epochID)
		if err != nil || epoch.Outcome == "completed" || epoch.Outcome == "failed" || epoch.Outcome == "outcome_unknown" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || epoch.Outcome != "completed" {
		t.Fatalf("Agent-work Hook terminal epoch=%#v err=%v", epoch, err)
	}
	status, err := s.AgentWorkFinishedHookStatus(ctx, "example", "agent-one")
	if err != nil || status == nil || status.Epoch != epochID || status.Operation != operation.OperationID || status.Outcome != "completed" {
		t.Fatalf("Agent-work Hook public status=%#v err=%v", status, err)
	}
	failureDefinition := workFinishedProcedureDefinition()
	failureDefinition.Script = procedureTestScript(t, root, "#!/bin/sh\nexit 41\n")
	if _, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "finished_failure",
		Definition: failureDefinition,
		Reason:     "Cover Agent-work failure observability.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigHookBind(ctx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPostAgentWorkFinished,
		Procedure: "finished_failure",
		Reason:    "Cover Agent-work failure observability.",
	}); err != nil {
		t.Fatal(err)
	}
	failureEpochID, err := newAgentWorkEpochID()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.ArmCallbackEpoch(ctx, sqlitestore.CallbackEpoch{
		ID: failureEpochID, ProjectID: "example", AgentID: "agent-one", SessionID: AgentSessionID(ctx), SessionKey: "agent-session", ArmedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"running", "idle", "idle"} {
		if _, err := s.Durability.ObserveCallbackEpoch(ctx, failureEpochID, state); err != nil {
			t.Fatal(err)
		}
	}
	failureEpoch, err := s.Durability.ReadCallbackEpoch(ctx, failureEpochID)
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchAgentWorkFinishedHook(ctx, failureEpoch)
	failureEpoch, err = s.Durability.ReadCallbackEpoch(ctx, failureEpochID)
	if err != nil || failureEpoch.OperationID == "" {
		t.Fatalf("failed Agent-work Hook Operation was not allocated: %#v err=%v", failureEpoch, err)
	}
	failureOperation := tsk585WaitOperation(t, s, failureEpoch.OperationID)
	if failureOperation.Status != "failed" || failureOperation.Error == "" {
		t.Fatalf("failed Agent-work Hook Operation=%#v", failureOperation)
	}
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		failureEpoch, err = s.Durability.ReadCallbackEpoch(ctx, failureEpochID)
		if err != nil || failureEpoch.Outcome == "completed" || failureEpoch.Outcome == "failed" || failureEpoch.Outcome == "outcome_unknown" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, err = s.AgentWorkFinishedHookStatus(ctx, "example", "agent-one")
	if err != nil || failureEpoch.Outcome != "failed" || status == nil || status.Epoch != failureEpochID || status.Operation != failureOperation.OperationID || status.Outcome != "failed" {
		t.Fatalf("failed Agent-work Hook observability epoch=%#v status=%#v err=%v", failureEpoch, status, err)
	}
	if created.Revision < 2 {
		t.Fatal("test Procedure configuration was not established")
	}
}

func TestTaskSubmitHooksBlockBeforeAndDoNotRollBackAfter(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk602-submit-hook", "Task Hook semantics")
	worktree := tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	_ = worktree
	locals, err := db.ListLocalSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var workerSession string
	for _, local := range locals {
		var record durableSession.Record
		if json.Unmarshal(local.Payload, &record) == nil && record.Role == durableSession.RoleWorker && record.Status == durableSession.StatusActive && record.SessionRef != nil && *record.SessionRef == "example_master" {
			workerSession = local.ID
			break
		}
	}
	if workerSession == "" {
		t.Fatal("active Worker Session was not seeded")
	}
	workerCtx := WithAgentSessionID(ctx, workerSession)
	root := s.Config.Projects["example"].Root
	script := procedureTestScript(t, root, "#!/bin/sh\nexit 17\n")
	definition := model.ProjectProcedureDefinition{
		Script:  script,
		Summary: "Reject Task submission",
		Guide:   "Returns a failing status to block a pre-transition Hook or report a post-transition failure.",
		Input: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"project": map[string]any{"$ref": "EntityKeyAndReference"}},
			"required":             []any{"project"},
			"additionalProperties": false,
		},
		Output: map[string]any{
			"type": "object", "properties": map[string]any{}, "additionalProperties": false,
		},
	}
	plannerCtx := procedurePlannerContext(t, s)
	if _, err := s.ConfigProcedureCreate(plannerCtx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "reject_submit",
		Definition: definition,
		Reason:     "Test Task Hook failure semantics.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigHookBind(plannerCtx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPreTaskSubmit,
		Procedure: "reject_submit",
		Reason:    "Test pre-submit blocking.",
	}); err != nil {
		t.Fatal(err)
	}
	preReceipt, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil {
		t.Fatal(err)
	}
	preOperation := tsk585WaitOperation(t, s, preReceipt.OperationID)
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || preOperation.Status != "failed" || state.Status != model.TaskExecutionDispatched {
		t.Fatalf("pre Hook did not block submission: operation=%#v state=%#v found=%v err=%v", preOperation, state, found, err)
	}
	if len(taskLifecycleHookAttempts(preOperation.Result)) != 1 || taskLifecycleHookAttempts(preOperation.Result)[0].Outcome != "failed" {
		t.Fatalf("pre Hook evidence=%s", preOperation.Result)
	}
	if _, err := s.ConfigHookUnbind(plannerCtx, ConfigHookUnbindInput{
		ProjectID: "example",
		Hook:      model.HookPreTaskSubmit,
		Reason:    "Move the failure test to post-submit.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigHookBind(plannerCtx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPostTaskSubmit,
		Procedure: "reject_submit",
		Reason:    "Test post-submit non-rollback.",
	}); err != nil {
		t.Fatal(err)
	}
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil || configuration.Hooks[model.HookPostTaskSubmit] != "reject_submit" {
		t.Fatalf("post-submit Hook configuration=%#v err=%v", configuration.Hooks, err)
	}
	postReceipt, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil {
		t.Fatal(err)
	}
	postOperation := tsk585WaitOperation(t, s, postReceipt.OperationID)
	postHookOutcome := ""
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, attempt := range taskLifecycleHookAttempts(postOperation.Result) {
			if attempt.Hook == model.HookPostTaskSubmit {
				postHookOutcome = attempt.Outcome
			}
		}
		if postHookOutcome != "" && postHookOutcome != "pending" {
			break
		}
		postOperation, err = s.readDurableMutation(postReceipt.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, found, err = db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || postOperation.Status != "completed" || state.Status != model.TaskExecutionAwaitingReview {
		t.Fatalf("post Hook failure changed committed submission: operation=%#v state=%#v found=%v err=%v", postOperation, state, found, err)
	}
	postAttempts := 0
	for _, attempt := range taskLifecycleHookAttempts(postOperation.Result) {
		if attempt.Hook == model.HookPostTaskSubmit {
			postAttempts++
			if attempt.Outcome != "failed" {
				t.Fatalf("post Hook evidence=%s", postOperation.Result)
			}
		}
	}
	if postHookOutcome != "failed" || postAttempts != 1 {
		t.Fatalf("post Hook evidence=%s", postOperation.Result)
	}
}

func TestTaskVerifyAndIntegrateHooksEnforceBeforeAndPreserveAfter(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	workerSession := tsk622AssertWorkerSessionCount(t, db)
	workerCtx := WithAgentSessionID(context.Background(), workerSession)
	plannerCtx := procedurePlannerContext(t, s)
	task := tsk585Task(t, s, "procedure-lifecycle-hooks", "Procedure lifecycle Hooks")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)
	root := s.Config.Projects["example"].Root
	bindHook := func(hook, name, script string) {
		t.Helper()
		input, ok := model.ProjectHookPayloadSchema(hook)
		if !ok {
			t.Fatalf("missing payload schema for Hook %q", hook)
		}
		output := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		if hook == model.HookPreTaskVerify {
			var err error
			output, err = model.TaskVerificationProcedureOutputSchema([]string{"fixture_check"})
			if err != nil {
				t.Fatal(err)
			}
		}
		definition := model.ProjectProcedureDefinition{
			Script: procedureTestScript(t, root, script), Summary: "Exercise a Task lifecycle Hook.", Guide: "Accepts its canonical lifecycle payload.",
			Input: input, Output: output,
		}
		if _, err := s.ConfigProcedureCreate(plannerCtx, ConfigProcedureCreateInput{
			ProjectID:  "example",
			Name:       name,
			Definition: definition,
			Reason:     "Test Task lifecycle Hook semantics.",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConfigHookBind(plannerCtx, ConfigHookBindInput{
			ProjectID: "example",
			Hook:      hook,
			Procedure: name,
			Reason:    "Test Task lifecycle Hook semantics.",
		}); err != nil {
			t.Fatal(err)
		}
	}
	waitAttempt := func(operationID, hook string) (durableMutationOperation, procedureHookAttempt) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		var last durableMutationOperation
		for time.Now().Before(deadline) {
			operation, err := s.readDurableMutation(operationID)
			if err != nil {
				t.Fatal(err)
			}
			last = operation
			attempts := taskLifecycleHookAttempts(operation.Result)
			for _, attempt := range attempts {
				if attempt.Hook == hook && attempt.Outcome != "pending" && durableMutationTerminal(operation.Status) {
					return operation, attempt
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("Hook %s did not reach a terminal attempt: operation=%#v attempts=%#v", hook, last, taskLifecycleHookAttempts(last.Result))
		return durableMutationOperation{}, procedureHookAttempt{}
	}
	bindHook(model.HookPreTaskVerify, "reject_verify", "#!/bin/sh\nexit 41\n")
	preVerifyReceipt, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	preVerify, preVerifyAttempt := waitAttempt(preVerifyReceipt.OperationID, model.HookPreTaskVerify)
	state, found, err := db.ReadTaskExecutionState(context.Background(), "example", task.ID)
	if err != nil || !found || preVerify.Status != "failed" || state.Status != model.TaskExecutionReadyForVerification || preVerifyAttempt.Outcome != "failed" {
		t.Fatalf("pre-verify Hook did not block verification: operation=%#v attempt=%#v state=%#v found=%v err=%v", preVerify, preVerifyAttempt, state, found, err)
	}
	if _, err := s.ConfigHookBind(plannerCtx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPreTaskVerify,
		Procedure: "fixture_verify",
		Reason:    "Restore the non-Go verification Procedure for the post-Hook test.",
	}); err != nil {
		t.Fatal(err)
	}
	task = tsk585Task(t, s, "procedure-lifecycle-hooks-post", "Post Hook lifecycle semantics")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)
	bindHook(model.HookPostTaskVerify, "reject_post_verify", "#!/bin/sh\nexit 41\n")
	var verified durableMutationOperation
	for attempt := 0; attempt < 3; attempt++ {
		receipt, err := s.TaskExecutionTestAsync(workerCtx, TaskExecutionTestInput{
			ProjectID: "example",
			Key:       task.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		verified = tsk585WaitOperation(t, s, receipt.OperationID)
		if verified.Status != "failed" || !strings.Contains(verified.Error, "timing is not ordered") {
			break
		}
	}
	verified, postVerifyAttempt := waitAttempt(verified.OperationID, model.HookPostTaskVerify)
	if verified.Status != "completed" || postVerifyAttempt.Outcome != "failed" {
		t.Fatalf("post-verify Hook falsified the committed verification: operation=%#v attempt=%#v", verified, postVerifyAttempt)
	}
	bindHook(model.HookPostTaskIntegrate, "reject_post_integrate", "#!/bin/sh\nexit 41\n")
	postIntegrateReceipt, err := s.TaskExecutionIntegrateAsync(workerCtx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	postIntegrate, postIntegrateAttempt := waitAttempt(postIntegrateReceipt.OperationID, model.HookPostTaskIntegrate)
	state, found, err = db.ReadTaskExecutionState(context.Background(), "example", task.ID)
	if err != nil || !found || postIntegrate.Status != "completed" || state.Status != model.TaskExecutionIntegrated || postIntegrateAttempt.Outcome != "failed" {
		t.Fatalf("post-integrate Hook changed the committed integration: operation=%#v attempt=%#v state=%#v found=%v err=%v", postIntegrate, postIntegrateAttempt, state, found, err)
	}
	if _, err := s.ConfigHookUnbind(plannerCtx, ConfigHookUnbindInput{
		ProjectID: "example",
		Hook:      model.HookPostTaskVerify,
		Reason:    "Isolate the pre-integrate failure case.",
	}); err != nil {
		t.Fatal(err)
	}
	preIntegrateTask := tsk585Task(t, s, "procedure-lifecycle-hooks-pre-integrate", "Pre-integrate Hook semantics")
	tsk585Dispatch(t, s, preIntegrateTask.ID)
	tsk585DriveToVerified(t, s, preIntegrateTask.ID)
	bindHook(model.HookPreTaskIntegrate, "reject_integrate", "#!/bin/sh\nexit 41\n")
	preIntegrateReceipt, err := s.TaskExecutionIntegrateAsync(workerCtx, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       preIntegrateTask.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	preIntegrate := tsk585WaitOperation(t, s, preIntegrateReceipt.OperationID)
	state, found, err = db.ReadTaskExecutionState(context.Background(), "example", preIntegrateTask.ID)
	if err != nil || !found || preIntegrate.Status != "failed" || state.Status != model.TaskExecutionVerified {
		t.Fatalf("pre-integrate Hook did not block integration: operation=%#v state=%#v found=%v err=%v", preIntegrate, state, found, err)
	}
}

func procedurePlannerContext(t *testing.T, s *Service) context.Context {
	t.Helper()
	session, err := durableSession.NewStoreWithDurability(s.Durability).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: s.Config.Projects["example"].ProjectCode,
		Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	return WithAgentSessionID(trustedWorkflowPolicyContext(context.Background(), "planner"), session.ID)
}

func procedureTestScript(t *testing.T, root, body string) string {
	t.Helper()
	directory := filepath.Join(root, "scripts")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "procedure-test.sh")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return "scripts/procedure-test.sh"
}
