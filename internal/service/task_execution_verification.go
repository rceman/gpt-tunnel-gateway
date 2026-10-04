package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/publicprojection"
)

type TaskExecutionTestInput struct {
	ProjectID string
	Key       string
}

type TaskExecutionVerificationPublic struct {
	OperationID   string `json:"operation_id"`
	TaskRevision  int    `json:"task_revision"`
	CandidateHead string `json:"candidate_head"`
	CandidateTree string `json:"candidate_tree"`
	MainBase      string `json:"main_base"`
	VerifiedAt    string `json:"verified_at"`
}

type TaskExecutionTestReceipt struct {
	OperationID string                     `json:"operation_id"`
	Status      string                     `json:"status"`
	Result      *TaskExecutionPublicOutput `json:"result,omitempty"`
	Error       string                     `json:"error,omitempty"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

type taskExecutionVerificationReviews struct {
	code   int64
	tests  int64
	rebase int64
}

type taskExecutionTestIdentity struct {
	ProjectID          string `json:"project_id"`
	TaskID             string `json:"task_id"`
	TaskRevision       int    `json:"task_revision"`
	TaskRevisionSHA256 string `json:"task_revision_sha256"`
	Stage              string `json:"stage"`
	BaseHead           string `json:"base_head"`
	Head               string `json:"head"`
	Branch             string `json:"branch"`
	Agent              string `json:"agent"`
	Worktree           string `json:"worktree"`
	CodeReviewID       int64  `json:"code_review_id"`
	TestsReviewID      int64  `json:"tests_review_id"`
	RebaseReviewID     int64  `json:"rebase_review_id"`
	GateProfileSHA256  string `json:"gate_profile_sha256"`
	CanonicalHead      string `json:"canonical_head"`
	LaneHead           string `json:"lane_head"`
	LaneContent        string `json:"lane_content"`
}

type taskExecutionVerificationAdmission struct {
	identity  taskExecutionTestIdentity
	reviews   taskExecutionVerificationReviews
	names     []string
	profile   string
	canonical string
	project   config.ProjectConfig
	lane      config.ProjectConfig
	snapshot  verificationGateSnapshot
}

func boundedTaskVerificationError(text string) string {
	const max = 2048
	if len(text) <= max {
		return text
	}
	text = text[:max]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func taskExecutionTestReceipt(operation durableMutationOperation) TaskExecutionTestReceipt {
	receipt := TaskExecutionTestReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       boundedTaskVerificationError(operation.Error),
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result TaskExecutionPublicOutput
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Task verification result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) TaskExecutionTestAsync(ctx context.Context, in TaskExecutionTestInput) (TaskExecutionTestReceipt, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskExecutionTestReceipt{}, err
	}
	if err := model.ValidateCanonicalTaskID(in.Key); err != nil {
		return TaskExecutionTestReceipt{}, err
	}
	s.taskExecutionMu.Lock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		s.taskExecutionMu.Unlock()
		if err != nil {
			return TaskExecutionTestReceipt{}, err
		}
		return TaskExecutionTestReceipt{}, fmt.Errorf("Task has not been dispatched")
	}
	admission, err := s.taskExecutionVerificationAdmissionFor(ctx, in.ProjectID, state)
	if err == nil {
		if receipt, reused, reuseErr := s.reuseCurrentTaskExecutionVerification(ctx, state, admission); reuseErr != nil {
			s.taskExecutionMu.Unlock()
			return TaskExecutionTestReceipt{}, reuseErr
		} else if reused {
			s.taskExecutionMu.Unlock()
			return receipt, nil
		}
	}
	s.taskExecutionMu.Unlock()
	if err != nil {
		return TaskExecutionTestReceipt{}, err
	}
	operation, err := s.enqueueTaskVerificationAttempt(ctx, "task-execution-test", in.ProjectID, in, admission.identity)
	if err != nil {
		return TaskExecutionTestReceipt{}, err
	}
	return taskExecutionTestReceipt(operation), nil
}

func (s *Service) reuseCurrentTaskExecutionVerification(ctx context.Context, state model.TaskExecutionState, admission taskExecutionVerificationAdmission) (TaskExecutionTestReceipt, bool, error) {
	if state.Status != model.TaskExecutionVerified || !admission.snapshot.clean || admission.snapshot.head != state.Head {
		return TaskExecutionTestReceipt{}, false, nil
	}
	receipt, current, _, _, err := s.taskExecutionVerificationProofCurrent(ctx, state)
	if err != nil {
		return TaskExecutionTestReceipt{}, false, err
	}
	if !current || model.ValidateOperationID(receipt.OperationID) != nil || receipt.BaseHead != admission.canonical || receipt.GateProfileSHA256 != admission.profile || receipt.CandidateHead != admission.snapshot.head || receipt.CandidateTree != admission.snapshot.tree || receipt.CodeReviewID != admission.reviews.code || receipt.TestsReviewID != admission.reviews.tests || receipt.RebaseReviewID != admission.reviews.rebase {
		return TaskExecutionTestReceipt{}, false, nil
	}
	result := taskExecutionPublicOutput(state)
	projection, err := taskExecutionVerificationProjection(receipt)
	if err != nil {
		return TaskExecutionTestReceipt{}, false, err
	}
	result.Verification = &projection
	return TaskExecutionTestReceipt{
		OperationID: receipt.OperationID,
		Status:      "completed",
		Result:      &result,
		CreatedAt:   receipt.StartedAt,
		UpdatedAt:   receipt.CompletedAt,
	}, true, nil
}

func (s *Service) taskExecutionVerificationAdmissionFor(ctx context.Context, projectID string, state model.TaskExecutionState) (taskExecutionVerificationAdmission, error) {
	var admission taskExecutionVerificationAdmission
	if s.Durability == nil {
		return admission, fmt.Errorf("shared durability is unavailable")
	}
	if state.Status != model.TaskExecutionReadyForVerification && state.Status != model.TaskExecutionVerifying && state.Status != model.TaskExecutionVerified {
		return admission, fmt.Errorf("Task is not ready for verification")
	}
	return s.computeTaskExecutionVerificationAdmission(ctx, projectID, state)
}

// computeTaskExecutionVerificationAdmission validates the shared authority
// coordinates — frozen Task, review phases, gate profile, canonical head, and
// exact lane state — without gating the execution status; callers gate their
// own statuses before asking for the admitted coordinates.
func (s *Service) computeTaskExecutionVerificationAdmission(ctx context.Context, projectID string, state model.TaskExecutionState) (taskExecutionVerificationAdmission, error) {
	var admission taskExecutionVerificationAdmission
	if s.Durability == nil {
		return admission, fmt.Errorf("shared durability is unavailable")
	}
	task, err := s.readSharedTask(ctx, projectID, state.TaskID)
	if err != nil {
		return admission, err
	}
	if task.Revision != state.TaskRevision || task.RevisionSHA256 != state.TaskRevisionSHA256 {
		return admission, fmt.Errorf("frozen Task content changed after dispatch")
	}
	reviews, err := s.taskExecutionVerificationReviews(ctx, projectID, state.TaskID, state)
	if err != nil {
		return admission, err
	}
	names, profile, err := s.taskExecutionGateProfile(ctx, projectID)
	if err != nil {
		return admission, err
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return admission, err
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, projectID, state.TaskID)
	if err != nil {
		return admission, err
	}
	lane := project
	lane.Root = lanePath
	snapshot, err := s.captureVerificationSnapshot(ctx, lane)
	if err != nil {
		return admission, err
	}
	if !snapshot.clean || snapshot.head != state.Head || snapshot.branch != state.Branch || model.ValidateCommitSHA(snapshot.tree) != nil {
		return admission, fmt.Errorf("Task candidate lane is not the exact clean reviewed head")
	}
	canonical, err := s.Git.RefreshDefaultBranch(ctx, project)
	if err != nil {
		return admission, err
	}
	admission.reviews = reviews
	admission.names = names
	admission.profile = profile
	admission.canonical = canonical
	admission.project = project
	admission.lane = lane
	admission.snapshot = snapshot
	admission.identity = taskExecutionTestIdentity{
		ProjectID:          projectID,
		TaskID:             state.TaskID,
		TaskRevision:       state.TaskRevision,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		Stage:              state.Stage,
		BaseHead:           state.BaseHead,
		Head:               state.Head,
		Branch:             state.Branch,
		Agent:              state.Agent,
		Worktree:           state.Worktree,
		CodeReviewID:       reviews.code,
		TestsReviewID:      reviews.tests,
		RebaseReviewID:     reviews.rebase,
		GateProfileSHA256:  profile,
		CanonicalHead:      canonical,
		LaneHead:           snapshot.head,
		LaneContent:        snapshot.contentID,
	}
	return admission, nil
}

func (s *Service) taskExecutionVerificationReviews(ctx context.Context, projectID, key string, state model.TaskExecutionState) (taskExecutionVerificationReviews, error) {
	var reviews taskExecutionVerificationReviews
	for _, stage := range []string{"code", "tests", "rebase"} {
		if (stage == "tests" && state.Stage != "tests") || (stage == "rebase" && state.Stage != "rebase") {
			continue
		}
		phase, found, err := s.Durability.ReadLatestAcceptedTaskExecutionPhase(ctx, projectID, key, stage)
		if err != nil {
			return taskExecutionVerificationReviews{}, err
		}
		if !found || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 {
			return taskExecutionVerificationReviews{}, fmt.Errorf("accepted %s review is required for verification", stage)
		}
		if state.Stage == stage && phase.Head != state.Head {
			return taskExecutionVerificationReviews{}, fmt.Errorf("accepted %s review does not match the candidate head", stage)
		}
		switch stage {
		case "code":
			reviews.code = phase.ID
		case "tests":
			reviews.tests = phase.ID
		case "rebase":
			reviews.rebase = phase.ID
		}
	}
	return reviews, nil
}

func (s *Service) taskExecutionGateProfile(ctx context.Context, projectID string) ([]string, string, error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return nil, "", err
	}
	procedureName, bound := configuration.Hooks[model.HookPreTaskVerify]
	if !bound {
		return nil, "", fmt.Errorf("ProjectConfiguration has no pre_task_verify Procedure")
	}
	definition, found := configuration.Procedures[procedureName]
	if !found {
		return nil, "", fmt.Errorf("pre_task_verify Procedure is unavailable")
	}
	names, err := taskVerificationProcedureGateIDs(definition.Output)
	if err != nil {
		return nil, "", err
	}
	profile := struct {
		Version    string                           `json:"version"`
		Hook       string                           `json:"hook"`
		Procedure  string                           `json:"procedure"`
		Definition model.ProjectProcedureDefinition `json:"definition"`
	}{
		Version:    "task-execution-verification/v4",
		Hook:       model.HookPreTaskVerify,
		Procedure:  procedureName,
		Definition: definition,
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return names, hex.EncodeToString(sum[:]), nil
}

func taskVerificationProcedureGateIDs(output map[string]any) ([]string, error) {
	properties, ok := output["properties"].(map[string]any)
	if !ok || len(output) != 4 || output["type"] != "object" || output["additionalProperties"] != false || !taskVerificationSchemaRequired(output["required"], "gates") || len(properties) != 1 {
		return nil, fmt.Errorf("pre_task_verify Procedure output contract is not canonical")
	}
	gatesSchema, ok := properties["gates"].(map[string]any)
	if !ok || gatesSchema["type"] != "array" {
		return nil, fmt.Errorf("pre_task_verify Procedure output contract is not canonical")
	}
	minimum, minOK := taskVerificationSchemaInteger(gatesSchema["minItems"])
	maximum, maxOK := taskVerificationSchemaInteger(gatesSchema["maxItems"])
	itemSchema, ok := gatesSchema["items"].(map[string]any)
	if !minOK || !maxOK || minimum < 1 || minimum > model.MaxTaskVerificationProcedureGates || minimum != maximum || !ok || itemSchema["type"] != "object" || itemSchema["additionalProperties"] != false {
		return nil, fmt.Errorf("pre_task_verify Procedure output contract is not canonical")
	}
	itemProperties, ok := itemSchema["properties"].(map[string]any)
	if !ok || len(itemProperties) != 3 || len(itemSchema) != 4 || !taskVerificationSchemaRequired(itemSchema["required"], "id", "exit_code", "duration_ms") {
		return nil, fmt.Errorf("pre_task_verify Procedure output contract is not canonical")
	}
	idSchema, ok := itemProperties["id"].(map[string]any)
	if !ok || idSchema["type"] != "string" {
		return nil, fmt.Errorf("pre_task_verify Procedure gate ids are invalid")
	}
	enum, ok := idSchema["enum"].([]any)
	if !ok || len(enum) != minimum {
		return nil, fmt.Errorf("pre_task_verify Procedure gate ids are invalid")
	}
	exitSchema, exitOK := itemProperties["exit_code"].(map[string]any)
	durationSchema, durationOK := itemProperties["duration_ms"].(map[string]any)
	exitMin, exitMinOK := taskVerificationSchemaInteger(exitSchema["minimum"])
	exitMax, exitMaxOK := taskVerificationSchemaInteger(exitSchema["maximum"])
	durationMin, durationMinOK := taskVerificationSchemaInteger(durationSchema["minimum"])
	durationMax, durationMaxOK := taskVerificationSchemaInteger(durationSchema["maximum"])
	if !exitOK || !durationOK || exitSchema["type"] != "integer" || durationSchema["type"] != "integer" || !exitMinOK || !exitMaxOK || exitMin != 0 || exitMax != 255 || !durationMinOK || !durationMaxOK || durationMin != 0 || durationMax != model.MaxTaskVerificationGateDurationMS {
		return nil, fmt.Errorf("pre_task_verify Procedure gate result contract is invalid")
	}
	names := make([]string, len(enum))
	seen := map[string]bool{}
	for index, value := range enum {
		name, ok := value.(string)
		if !ok || model.ValidateProcedureName(name) != nil || seen[name] {
			return nil, fmt.Errorf("pre_task_verify Procedure gate ids are invalid")
		}
		seen[name] = true
		names[index] = name
	}
	return names, nil
}

func taskVerificationSchemaRequired(value any, names ...string) bool {
	required, ok := value.([]any)
	if !ok || len(required) != len(names) {
		return false
	}
	for index, name := range names {
		if required[index] != name {
			return false
		}
	}
	return true
}

func taskVerificationSchemaInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case float64:
		converted := int(number)
		return converted, float64(converted) == number
	default:
		return 0, false
	}
}

func taskExecutionProcedureGateEvidence(raw json.RawMessage, names []string, candidateTree, profile string) ([]model.CompletionGateResult, error) {
	var output struct {
		Gates []struct {
			ID         string `json:"id"`
			ExitCode   int    `json:"exit_code"`
			DurationMS int64  `json:"duration_ms"`
		} `json:"gates"`
	}
	if err := json.Unmarshal(raw, &output); err != nil || len(output.Gates) != len(names) || model.ValidateCommitSHA(candidateTree) != nil || model.ValidateSHA256(profile) != nil {
		return nil, fmt.Errorf("pre_task_verify Procedure returned invalid gate evidence")
	}
	digest := sha256.Sum256(raw)
	receiptDigest := hex.EncodeToString(digest[:])
	results := make([]model.CompletionGateResult, len(output.Gates))
	for index, gate := range output.Gates {
		if gate.ID != names[index] || gate.ExitCode < 0 || gate.ExitCode > 255 || gate.DurationMS < 0 || gate.DurationMS > model.MaxTaskVerificationGateDurationMS {
			return nil, fmt.Errorf("pre_task_verify Procedure returned invalid or reordered gate evidence")
		}
		results[index] = model.CompletionGateResult{
			ID: gate.ID, ExitCode: gate.ExitCode, Execution: "executed", TreeID: candidateTree,
			ContractDigest: profile, ReceiptDigest: receiptDigest, DurationMS: gate.DurationMS,
		}
		if gate.ExitCode != 0 {
			return results, fmt.Errorf("Task verification gate %q failed", gate.ID)
		}
	}
	return results, nil
}

func validateTaskVerificationGateResults(gates []model.CompletionGateResult, expected []string, candidateTree, profile string) error {
	if len(gates) == 0 || len(gates) != len(expected) || model.ValidateCommitSHA(candidateTree) != nil || model.ValidateSHA256(profile) != nil {
		return fmt.Errorf("Task verification gate coverage is invalid")
	}
	for index, gate := range gates {
		if gate.ID != expected[index] || gate.Execution != "executed" || gate.ExitCode != 0 || gate.TreeID != candidateTree || gate.ContractDigest != profile || model.ValidateSHA256(gate.ReceiptDigest) != nil || gate.DurationMS < 0 || gate.DurationMS > model.MaxTaskVerificationGateDurationMS {
			return fmt.Errorf("Task verification gate evidence is not a passing Procedure receipt")
		}
	}
	return nil
}

func taskExecutionVerificationBinds(receipt model.TaskExecutionVerification, state model.TaskExecutionState) bool {
	return receipt.ProjectID == state.ProjectID && receipt.TaskID == state.TaskID && receipt.TaskRevision == state.TaskRevision && receipt.TaskRevisionSHA256 == state.TaskRevisionSHA256 && receipt.BaseHead == state.BaseHead && receipt.CandidateHead == state.Head && receipt.Branch == state.Branch && receipt.AttemptRevision < state.ExecutionRevision
}

func (s *Service) taskExecutionVerificationProofCurrent(ctx context.Context, state model.TaskExecutionState) (model.TaskExecutionVerification, bool, bool, string, error) {
	receipt, found, legacy, err := s.Durability.ReadLatestTaskExecutionVerificationTolerant(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return model.TaskExecutionVerification{}, false, false, "", err
	}
	if legacy {
		return receipt, false, true, "Task verification receipt predates the current gate contract", nil
	}
	if !found {
		return model.TaskExecutionVerification{}, false, true, "no Task verification receipt", nil
	}
	if receipt.Outcome != model.TaskExecutionVerificationSucceeded {
		return receipt, false, false, "receipt outcome is not succeeded", nil
	}
	if !taskExecutionVerificationBinds(receipt, state) {
		return receipt, false, false, "receipt does not bind the integrated execution state", nil
	}
	names, profile, err := s.taskExecutionGateProfile(ctx, state.ProjectID)
	if err != nil {
		return model.TaskExecutionVerification{}, false, false, "", err
	}
	if receipt.GateProfileSHA256 != profile {
		return receipt, false, true, "verification Procedure changed after the receipt", nil
	}
	if err := validateTaskVerificationGateResults(receipt.Gates, names, receipt.CandidateTree, profile); err != nil {
		return receipt, false, false, "receipt gate evidence is invalid", nil
	}
	latestPhaseRev := 0
	for _, stage := range []string{"code", "tests", "rebase"} {
		phase, phaseFound, phaseErr := s.Durability.ReadLatestTaskExecutionPhase(ctx, state.ProjectID, state.TaskID, stage)
		if phaseErr != nil {
			return model.TaskExecutionVerification{}, false, false, "", phaseErr
		}
		if phaseFound && phase.ExecutionRevision > latestPhaseRev {
			latestPhaseRev = phase.ExecutionRevision
		}
	}
	if latestPhaseRev > receipt.AttemptRevision+1 {
		return receipt, false, false, "a review phase postdates the verification attempt", nil
	}
	return receipt, true, false, "", nil
}

func taskExecutionVerificationProjection(receipt model.TaskExecutionVerification) (TaskExecutionVerificationPublic, error) {
	compact := func(commit string) (string, error) {
		if model.ValidateCommitSHA(commit) != nil {
			return "", fmt.Errorf("invalid Git object ID in verification receipt")
		}
		return publicprojection.CompactGitFingerprint(receipt.ProjectID, commit)
	}
	candidateHead, err := compact(receipt.CandidateHead)
	if err != nil {
		return TaskExecutionVerificationPublic{}, err
	}
	candidateTree, err := compact(receipt.CandidateTree)
	if err != nil {
		return TaskExecutionVerificationPublic{}, err
	}
	mainBase, err := compact(receipt.BaseHead)
	if err != nil {
		return TaskExecutionVerificationPublic{}, err
	}
	return TaskExecutionVerificationPublic{
		OperationID:   receipt.OperationID,
		TaskRevision:  receipt.TaskRevision,
		CandidateHead: candidateHead,
		CandidateTree: candidateTree,
		MainBase:      mainBase,
		VerifiedAt:    receipt.CompletedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (s *Service) taskExecutionVerificationProjectionFor(ctx context.Context, state model.TaskExecutionState) (TaskExecutionVerificationPublic, bool, error) {
	if s.Durability == nil {
		return TaskExecutionVerificationPublic{}, false, nil
	}
	receipt, current, _, _, err := s.taskExecutionVerificationProofCurrent(ctx, state)
	if err != nil || !current {
		return TaskExecutionVerificationPublic{}, false, err
	}
	projection, err := taskExecutionVerificationProjection(receipt)
	if err != nil {
		return TaskExecutionVerificationPublic{}, false, err
	}
	return projection, true, nil
}

func taskExecutionCapturedIdentity(raw string) (taskExecutionTestIdentity, error) {
	var captured taskExecutionTestIdentity
	if err := json.Unmarshal([]byte(raw), &captured); err != nil {
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid: %w", err)
	}
	if err := model.ValidateProjectIdentifier(captured.ProjectID); err != nil {
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid: %w", err)
	}
	if err := model.ValidateCanonicalTaskID(captured.TaskID); err != nil {
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid: %w", err)
	}
	if captured.TaskRevision < 1 || len(captured.TaskRevisionSHA256) != 64 || len(captured.GateProfileSHA256) != 64 {
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid")
	}
	switch captured.Stage {
	case "code", "tests", "rebase":
	default:
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid")
	}
	for _, sha := range []string{captured.TaskRevisionSHA256, captured.GateProfileSHA256} {
		if _, err := hex.DecodeString(sha); err != nil {
			return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid: %w", err)
		}
	}
	for _, commit := range []string{captured.LaneContent, captured.BaseHead, captured.Head, captured.CanonicalHead, captured.LaneHead} {
		if err := model.ValidateCommitSHA(commit); err != nil {
			return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid: %w", err)
		}
	}
	if err := model.ValidateBranch(captured.Branch); err != nil {
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid: %w", err)
	}
	if !strings.HasPrefix(captured.Worktree, "WT-TSK") || !strings.HasSuffix(captured.Worktree, "-"+strings.ToLower(captured.Head[:8])) {
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid")
	}
	if captured.Branch == "" || captured.Agent == "" || captured.Worktree == "" || captured.CodeReviewID < 1 || captured.TestsReviewID < 0 || captured.RebaseReviewID < 0 || (captured.Stage == "tests" && captured.TestsReviewID < 1) {
		return taskExecutionTestIdentity{}, fmt.Errorf("durable Task verification snapshot is invalid")
	}
	return captured, nil
}

func (s *Service) taskExecutionTestRun(ctx context.Context, in TaskExecutionTestInput, capturedJSON string) (TaskExecutionPublicOutput, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if err := model.ValidateCanonicalTaskID(in.Key); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if s.Durability == nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("shared durability is unavailable")
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task verification requires a durable operation identity")
	}
	startedAt := time.Now().UTC()

	s.taskExecutionMu.Lock()
	state, found, err := s.readExecutionForMutation(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		s.taskExecutionMu.Unlock()
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has not been dispatched")
	}
	admission, err := s.taskExecutionVerificationAdmissionFor(ctx, in.ProjectID, state)
	if err != nil {
		s.taskExecutionMu.Unlock()
		return TaskExecutionPublicOutput{}, err
	}
	captured, err := taskExecutionCapturedIdentity(capturedJSON)
	if err != nil {
		s.taskExecutionMu.Unlock()
		return TaskExecutionPublicOutput{}, err
	}
	if captured != admission.identity {
		withoutCanonical := captured
		withoutCanonical.CanonicalHead = admission.identity.CanonicalHead
		if withoutCanonical != admission.identity {
			s.taskExecutionMu.Unlock()
			return TaskExecutionPublicOutput{}, fmt.Errorf("admitted Task verification authority changed while queued")
		}
		if reconcileErr := s.reconcileTaskExecutionBase(ctx, state, admission.project, admission.canonical); reconcileErr != nil {
			s.taskExecutionMu.Unlock()
			return TaskExecutionPublicOutput{}, reconcileErr
		}
		s.taskExecutionMu.Unlock()
		return TaskExecutionPublicOutput{}, fmt.Errorf("canonical default branch advanced beyond the admitted Task base; controlled rebase is required")
	}
	if state.Status == model.TaskExecutionVerified {
		receipt, current, _, _, err := s.taskExecutionVerificationProofCurrent(ctx, state)
		if err != nil {
			s.taskExecutionMu.Unlock()
			return TaskExecutionPublicOutput{}, err
		}
		live := current && receipt.BaseHead == admission.canonical && receipt.GateProfileSHA256 == admission.profile && admission.snapshot.clean && admission.snapshot.head == receipt.CandidateHead && admission.snapshot.tree == receipt.CandidateTree
		if live {
			out := taskExecutionPublicOutput(state)
			projection, err := taskExecutionVerificationProjection(receipt)
			if err != nil {
				s.taskExecutionMu.Unlock()
				return TaskExecutionPublicOutput{}, err
			}
			out.Verification = &projection
			s.taskExecutionMu.Unlock()
			return out, nil
		}
		if admission.canonical != state.BaseHead {
			reconcileErr := s.reconcileTaskExecutionBase(ctx, state, admission.project, admission.canonical)
			s.taskExecutionMu.Unlock()
			if reconcileErr != nil {
				return TaskExecutionPublicOutput{}, reconcileErr
			}
			return TaskExecutionPublicOutput{}, fmt.Errorf("canonical default branch advanced beyond verified Task base; controlled rebase is required")
		}
		s.taskExecutionMu.Unlock()
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task verification proof is stale under current authority; Planner rework is required")
	}
	if admission.canonical != state.BaseHead {
		reconcileErr := s.reconcileTaskExecutionBase(ctx, state, admission.project, admission.canonical)
		s.taskExecutionMu.Unlock()
		if reconcileErr != nil {
			return TaskExecutionPublicOutput{}, reconcileErr
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("canonical default branch advanced beyond Task base; controlled rebase is required")
	}
	if s.taskExecutionVerifyInFlight == nil {
		s.taskExecutionVerifyInFlight = make(map[string]string)
	}
	if owner, active := s.taskExecutionVerifyInFlight[in.Key]; active && owner != operationID {
		s.taskExecutionMu.Unlock()
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task verification is already in flight")
	}
	s.taskExecutionVerifyInFlight[in.Key] = operationID
	verificationRevision := state.ExecutionRevision
	if state.Status == model.TaskExecutionReadyForVerification {
		verificationRevision++
	}
	s.taskExecutionMu.Unlock()

	beforePayload := taskLifecycleHookPayload(in.ProjectID, in.Key, AgentSessionID(ctx), operationID, "", state.TaskRevision, verificationRevision, state.Head, admission.snapshot.tree, state.BaseHead, "", "", "")
	procedureOutput, runErr := s.runTaskLifecycleProcedureHookWithOutput(ctx, model.HookPreTaskVerify, "before", beforePayload, "", nil, "", admission.lane.Root)
	var gateResults []model.CompletionGateResult
	if runErr == nil {
		gateResults, runErr = taskExecutionProcedureGateEvidence(procedureOutput, admission.names, admission.snapshot.tree, admission.profile)
		if runErr != nil && len(procedureOutput) > 0 {
			if evidenceErr := s.failTaskVerificationHookAttempt(operationID); evidenceErr != nil {
				runErr = fmt.Errorf("Task verification Procedure failed and its evidence could not be updated: %w", evidenceErr)
			}
		}
	}
	if runErr != nil {
		s.releaseTaskVerificationInFlight(in.Key, operationID)
		return TaskExecutionPublicOutput{}, runErr
	}

	s.taskExecutionMu.Lock()
	defer func() {
		if s.taskExecutionVerifyInFlight[in.Key] == operationID {
			delete(s.taskExecutionVerifyInFlight, in.Key)
		}
		s.taskExecutionMu.Unlock()
	}()
	current, foundCurrent, readErr := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key)
	if readErr != nil {
		return TaskExecutionPublicOutput{}, readErr
	}
	if !foundCurrent || current.Status != state.Status || current.ExecutionRevision != state.ExecutionRevision {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task execution state changed during pre_task_verify Procedure")
	}
	after, err := s.taskExecutionVerificationAdmissionFor(ctx, in.ProjectID, current)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if after.identity != admission.identity {
		if after.identity.CanonicalHead != admission.identity.CanonicalHead {
			if err := s.reconcileTaskExecutionBase(ctx, current, admission.project, after.identity.CanonicalHead); err != nil {
				return TaskExecutionPublicOutput{}, err
			}
			return TaskExecutionPublicOutput{}, fmt.Errorf("canonical default branch advanced during Task verification")
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task verification authority changed during pre_task_verify Procedure")
	}
	if err := ctx.Err(); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	state = current
	if state.Status == model.TaskExecutionReadyForVerification {
		state.Status = model.TaskExecutionVerifying
		state.ExecutionRevision++
		state.UpdatedAt = s.durableNow()
		if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
			return TaskExecutionPublicOutput{}, err
		}
	}
	attempt := state.ExecutionRevision
	completedAt := time.Now().UTC()
	if !completedAt.After(startedAt) {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task verification timing is not ordered; proof cannot be recorded")
	}
	receipt := model.TaskExecutionVerification{
		ProjectID: in.ProjectID, TaskID: in.Key, OperationID: operationID,
		TaskRevision: state.TaskRevision, TaskRevisionSHA256: state.TaskRevisionSHA256,
		BaseHead: state.BaseHead, CandidateHead: state.Head, CandidateTree: admission.snapshot.tree,
		Branch: state.Branch, GateProfileSHA256: admission.profile,
		Outcome: model.TaskExecutionVerificationSucceeded, AttemptRevision: attempt,
		CodeReviewID: admission.reviews.code, TestsReviewID: admission.reviews.tests, RebaseReviewID: admission.reviews.rebase,
		Gates: gateResults, StartedAt: startedAt, CompletedAt: completedAt,
	}
	next := state
	next.ExecutionRevision = attempt + 1
	next.Status = model.TaskExecutionVerified
	next.UpdatedAt = completedAt
	finishCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		finishCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
	}
	if err := s.Durability.FinishTaskExecutionVerification(finishCtx, next, attempt, receipt); err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task verification finish could not be recorded: %w", err)
	}
	postPayload := taskLifecycleHookPayload(in.ProjectID, in.Key, AgentSessionID(ctx), operationID, "", receipt.TaskRevision, receipt.AttemptRevision, receipt.CandidateHead, receipt.CandidateTree, receipt.BaseHead, string(receipt.Outcome), "", "")
	out := taskExecutionPublicOutput(next)
	projection, err := taskExecutionVerificationProjection(receipt)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	out.Verification = &projection
	result, _ := json.Marshal(out)
	_ = s.runTaskLifecycleProcedureHook(ctx, model.HookPostTaskVerify, "after", postPayload, "completed", result, "")
	return out, nil
}

func (s *Service) releaseTaskVerificationInFlight(key, operationID string) {
	s.taskExecutionMu.Lock()
	if s.taskExecutionVerifyInFlight[key] == operationID {
		delete(s.taskExecutionVerifyInFlight, key)
	}
	s.taskExecutionMu.Unlock()
}

func (s *Service) failTaskVerificationHookAttempt(operationID string) error {
	attempt, err := s.readTaskLifecycleHookAttempt(operationID, model.HookPreTaskVerify)
	if err != nil {
		return err
	}
	if attempt.Outcome != "completed" || !json.Valid(attempt.Output) {
		return fmt.Errorf("completed pre_task_verify evidence is unavailable")
	}
	attempt.Outcome = "failed"
	attempt.Reason = "Project verification Procedure reported unsuccessful or invalid gate evidence"
	return s.writeTaskLifecycleHookAttempt(operationID, attempt)
}
