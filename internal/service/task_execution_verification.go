package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskExecutionTestInput struct {
	ProjectID string
	Key       string
}

type TaskExecutionVerificationPublic struct {
	OperationID   string `json:"operation_id"`
	TaskRevision  int    `json:"task_revision"`
	TaskDigest    string `json:"task_digest"`
	CandidateHead string `json:"candidate_head"`
	CandidateTree string `json:"candidate_tree"`
	MainBase      string `json:"main_base"`
	GateProfile   string `json:"gate_profile"`
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
	s.taskExecutionMu.Unlock()
	if err != nil {
		return TaskExecutionTestReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutationWithIdentity(ctx, "task-execution-test", in.ProjectID, in, admission.identity)
	if err != nil {
		return TaskExecutionTestReceipt{}, err
	}
	return taskExecutionTestReceipt(operation), nil
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
		if stage == "rebase" && state.Stage != "rebase" {
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

func taskExecutionVerificationAllowedTestFlag(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	name := strings.TrimLeft(arg, "-")
	value := ""
	hasValue := false
	if idx := strings.Index(name, "="); idx >= 0 {
		name, value, hasValue = name[:idx], name[idx+1:], true
	}
	switch name {
	case "count", "parallel", "p":
		if !hasValue {
			return false
		}
		n, err := strconv.Atoi(value)
		return err == nil && n >= 1
	case "timeout":
		if !hasValue {
			return false
		}
		d, err := time.ParseDuration(value)
		return err == nil && d > 0
	case "v", "race", "cover", "failfast", "json", "fullpath", "shuffle":
		return true
	case "covermode", "coverprofile", "tags":
		return hasValue && value != ""
	default:
		return false
	}
}

// taskExecutionVerificationFullSuiteArgv requires the test gate to run the
// full repository suite uncached: ./... coverage plus an explicit positive
// -count so no prior Task/Train receipt or Go result cache can stand in for
// fresh execution on this exact candidate.
func taskExecutionVerificationFullSuiteArgv(argv []string) bool {
	if len(argv) < 3 || argv[0] != "go" || argv[1] != "test" {
		return false
	}
	full := false
	fresh := false
	for _, arg := range argv[2:] {
		switch {
		case arg == "./...":
			full = true
		case strings.HasPrefix(strings.TrimLeft(arg, "-"), "count="):
			value := strings.TrimPrefix(strings.TrimLeft(arg, "-"), "count=")
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return false
			}
			fresh = true
		case taskExecutionVerificationAllowedTestFlag(arg):
		default:
			return false
		}
	}
	return full && fresh
}

func taskExecutionVerificationEnvironmentClean(goflags string) bool {
	for _, flag := range strings.Fields(goflags) {
		if !taskExecutionVerificationAllowedTestFlag(flag) {
			return false
		}
	}
	return true
}

// executeTaskVerificationGates runs the effective integration-class gate set
// for a Task verification attempt. Every new attempt executes fresh: a prior
// matching pass receipt is never substituted for execution.
func (s *Service) executeTaskVerificationGates(ctx context.Context, projectID, root string, names []string) ([]model.CompletionGateResult, error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return nil, err
	}
	results, err := s.executeProjectTaskGatesFresh(ctx, projectID, root, names, configuration.Workflow.GateCommands, gates.FullTestScope())
	if err != nil {
		return results, err
	}
	if err := validateProjectGateEvidence(results, names); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Service) taskExecutionEffectiveGOFLAGS(ctx context.Context) (string, error) {
	if s.effectiveGOFLAGS != nil {
		return s.effectiveGOFLAGS(ctx)
	}
	return gates.EffectiveGOFLAGS(ctx)
}

func (s *Service) taskExecutionGateProfile(ctx context.Context, projectID string) ([]string, string, error) {
	goflags, err := s.taskExecutionEffectiveGOFLAGS(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("Task verification cannot resolve effective GOFLAGS: %w", err)
	}
	names, err := s.ResolveProjectGates(ctx, projectID, "integration")
	if err != nil {
		return nil, "", err
	}
	if !containsGate(names, model.WorkflowGateTest) {
		return nil, "", fmt.Errorf("Task verification requires full-suite test gate coverage")
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return nil, "", err
	}
	normalized, err := gates.FullTestScope().Normalize()
	if err != nil {
		return nil, "", err
	}
	commands := make(map[string]string, len(names))
	for _, name := range names {
		argv, err := gates.ProjectGateCommandArgs(configuration.Workflow.GateCommands, name, "task", normalized)
		if err != nil {
			return nil, "", err
		}
		digest, err := gates.ProjectGateCommandDigest(configuration.Workflow.GateCommands, name, "task", normalized)
		if err != nil {
			return nil, "", err
		}
		if name == model.WorkflowGateTest && !taskExecutionVerificationFullSuiteArgv(argv) {
			return nil, "", fmt.Errorf("Task verification requires the configured test gate to target the full repository suite")
		}
		if name == model.WorkflowGateTest && !taskExecutionVerificationEnvironmentClean(goflags) {
			return nil, "", fmt.Errorf("Task verification requires the effective test environment to be unmodified by GOFLAGS")
		}
		commands[name] = digest
	}
	env := append([]string{}, os.Environ()...)
	sort.Strings(env)
	envSum := sha256.Sum256([]byte(strings.Join(env, "\x00")))
	goflagsSum := sha256.Sum256([]byte(goflags))
	profile := struct {
		Version        string            `json:"version"`
		Mode           string            `json:"mode"`
		Gates          []string          `json:"gates"`
		Commands       map[string]string `json:"commands"`
		Scope          string            `json:"scope"`
		Packages       []string          `json:"packages,omitempty"`
		RunnerContract string            `json:"runner_contract"`
		EnvSHA256      string            `json:"env_sha256"`
		GOFLAGSSHA256  string            `json:"goflags_sha256"`
	}{
		Version:        "task-execution-verification/v2",
		Mode:           "task",
		Gates:          names,
		Commands:       commands,
		Scope:          normalized.Mode,
		Packages:       normalized.Packages,
		RunnerContract: gates.TestGateRunnerContractVersion,
		EnvSHA256:      hex.EncodeToString(envSum[:]),
		GOFLAGSSHA256:  hex.EncodeToString(goflagsSum[:]),
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return names, hex.EncodeToString(sum[:]), nil
}

func taskExecutionVerificationBinds(receipt model.TaskExecutionVerification, state model.TaskExecutionState) bool {
	return receipt.ProjectID == state.ProjectID && receipt.TaskID == state.TaskID && receipt.TaskRevision == state.TaskRevision && receipt.TaskRevisionSHA256 == state.TaskRevisionSHA256 && receipt.BaseHead == state.BaseHead && receipt.CandidateHead == state.Head && receipt.Branch == state.Branch && receipt.AttemptRevision < state.ExecutionRevision
}

func (s *Service) taskExecutionVerificationProofCurrent(ctx context.Context, state model.TaskExecutionState) (model.TaskExecutionVerification, bool, error) {
	receipt, found, err := s.Durability.ReadLatestTaskExecutionVerification(ctx, state.ProjectID, state.TaskID)
	if err != nil || !found {
		return model.TaskExecutionVerification{}, false, err
	}
	if receipt.Outcome != model.TaskExecutionVerificationSucceeded || !taskExecutionVerificationBinds(receipt, state) {
		return model.TaskExecutionVerification{}, false, nil
	}
	latestPhaseRev := 0
	for _, stage := range []string{"code", "tests", "rebase"} {
		phase, phaseFound, phaseErr := s.Durability.ReadLatestTaskExecutionPhase(ctx, state.ProjectID, state.TaskID, stage)
		if phaseErr != nil {
			return model.TaskExecutionVerification{}, false, phaseErr
		}
		if phaseFound && phase.ExecutionRevision > latestPhaseRev {
			latestPhaseRev = phase.ExecutionRevision
		}
	}
	if latestPhaseRev > receipt.AttemptRevision+1 {
		return model.TaskExecutionVerification{}, false, nil
	}
	return receipt, true, nil
}

func taskExecutionVerificationProjection(receipt model.TaskExecutionVerification) TaskExecutionVerificationPublic {
	short := func(hash string) string {
		if len(hash) >= 8 {
			return strings.ToLower(hash[:8])
		}
		return ""
	}
	return TaskExecutionVerificationPublic{
		OperationID:   receipt.OperationID,
		TaskRevision:  receipt.TaskRevision,
		TaskDigest:    short(receipt.TaskRevisionSHA256),
		CandidateHead: short(receipt.CandidateHead),
		CandidateTree: short(receipt.CandidateTree),
		MainBase:      short(receipt.BaseHead),
		GateProfile:   short(receipt.GateProfileSHA256),
		VerifiedAt:    receipt.CompletedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (s *Service) taskExecutionVerificationProjectionFor(ctx context.Context, state model.TaskExecutionState) (TaskExecutionVerificationPublic, bool, error) {
	if s.Durability == nil {
		return TaskExecutionVerificationPublic{}, false, nil
	}
	receipt, current, err := s.taskExecutionVerificationProofCurrent(ctx, state)
	if err != nil || !current {
		return TaskExecutionVerificationPublic{}, false, err
	}
	return taskExecutionVerificationProjection(receipt), true, nil
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
	if captured.Branch == "" || captured.Agent == "" || captured.Worktree == "" || captured.CodeReviewID < 1 || captured.TestsReviewID < 1 || captured.RebaseReviewID < 0 {
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
		receipt, current, err := s.taskExecutionVerificationProofCurrent(ctx, state)
		if err != nil {
			s.taskExecutionMu.Unlock()
			return TaskExecutionPublicOutput{}, err
		}
		live := current && receipt.BaseHead == admission.canonical && receipt.GateProfileSHA256 == admission.profile && admission.snapshot.clean && admission.snapshot.head == receipt.CandidateHead && admission.snapshot.tree == receipt.CandidateTree
		if live {
			out := taskExecutionPublicOutput(state)
			projection := taskExecutionVerificationProjection(receipt)
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
	if state.Status == model.TaskExecutionReadyForVerification {
		state.Status = model.TaskExecutionVerifying
		state.ExecutionRevision++
		state.UpdatedAt = s.durableNow()
		if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
			delete(s.taskExecutionVerifyInFlight, in.Key)
			s.taskExecutionMu.Unlock()
			return TaskExecutionPublicOutput{}, err
		}
	}
	attempt := state.ExecutionRevision
	s.taskExecutionMu.Unlock()

	gateResults, gateErr := s.executeTaskVerificationGates(ctx, in.ProjectID, admission.lane.Root, admission.names)

	s.taskExecutionMu.Lock()
	defer func() { delete(s.taskExecutionVerifyInFlight, in.Key); s.taskExecutionMu.Unlock() }()
	completedAt := time.Now().UTC()
	runErr := gateErr
	if runErr == nil {
		for _, gate := range gateResults {
			if gate.ExitCode != 0 {
				runErr = fmt.Errorf("Task verification gate %q failed", gate.ID)
				break
			}
		}
	}
	current, foundCurrent, readErr := s.Durability.ReadTaskExecutionState(ctx, in.ProjectID, in.Key)
	var advancedCanonical string
	if runErr == nil {
		switch {
		case readErr != nil:
			runErr = readErr
		case !foundCurrent || current.Status != model.TaskExecutionVerifying || current.ExecutionRevision != attempt:
			runErr = fmt.Errorf("Task execution state changed during verification")
		default:
			after, admErr := s.taskExecutionVerificationAdmissionFor(ctx, in.ProjectID, current)
			if admErr != nil {
				runErr = admErr
			} else if after.identity != admission.identity {
				if after.identity.CanonicalHead != admission.identity.CanonicalHead {
					advancedCanonical = after.identity.CanonicalHead
					runErr = fmt.Errorf("canonical default branch advanced during Task verification")
				} else {
					runErr = fmt.Errorf("Task verification authority changed during execution")
				}
			}
		}
	}
	outcome := model.TaskExecutionVerificationSucceeded
	receiptError := ""
	if runErr == nil {
		if err := ctx.Err(); err != nil {
			runErr = err
		}
	}
	if runErr != nil {
		outcome = model.TaskExecutionVerificationFailed
		if asyncMutationOutcomeUnknown(runErr) || ctx.Err() != nil {
			outcome = model.TaskExecutionVerificationInterrupted
		}
		receiptError = boundedTaskVerificationError(runErr.Error())
	}
	if outcome == model.TaskExecutionVerificationSucceeded && !completedAt.After(startedAt) {
		outcome = model.TaskExecutionVerificationInterrupted
		runErr = fmt.Errorf("Task verification timing is not ordered; proof cannot be recorded")
		receiptError = boundedTaskVerificationError(runErr.Error())
	}
	receipt := model.TaskExecutionVerification{
		ProjectID: in.ProjectID, TaskID: in.Key, OperationID: operationID,
		TaskRevision: state.TaskRevision, TaskRevisionSHA256: state.TaskRevisionSHA256,
		BaseHead: state.BaseHead, CandidateHead: state.Head, CandidateTree: admission.snapshot.tree,
		Branch: state.Branch, GateProfileSHA256: admission.profile,
		Outcome: outcome, Error: receiptError, AttemptRevision: attempt,
		CodeReviewID: admission.reviews.code, TestsReviewID: admission.reviews.tests, RebaseReviewID: admission.reviews.rebase,
		Gates: gateResults, StartedAt: startedAt, CompletedAt: completedAt,
	}
	next := state
	next.ExecutionRevision = attempt + 1
	next.UpdatedAt = completedAt
	if outcome == model.TaskExecutionVerificationSucceeded {
		next.Status = model.TaskExecutionVerified
	} else {
		next.Status = model.TaskExecutionReadyForVerification
	}
	finishCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		finishCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
	}
	if err := s.Durability.FinishTaskExecutionVerification(finishCtx, next, attempt, receipt); err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task verification finish could not be recorded: %w", err)
	}
	if outcome == model.TaskExecutionVerificationSucceeded {
		out := taskExecutionPublicOutput(next)
		projection := taskExecutionVerificationProjection(receipt)
		out.Verification = &projection
		return out, nil
	}
	if advancedCanonical != "" {
		if reconcileErr := s.reconcileTaskExecutionBase(finishCtx, next, admission.project, advancedCanonical); reconcileErr != nil {
			return TaskExecutionPublicOutput{}, fmt.Errorf("%w (controlled rebase coordination: %v)", runErr, reconcileErr)
		}
	}
	return TaskExecutionPublicOutput{}, runErr
}
