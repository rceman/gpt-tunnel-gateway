package model

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 1
const PlanSchemaVersion = 2
const MaxDeferredReasonBytes = 1024
const MaxSafeInteger uint64 = 9007199254740991

var (
	idRE              = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	adrIDRE           = regexp.MustCompile(`^ADR-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	projectCodeRE     = regexp.MustCompile(`^[A-Z]{3}$`)
	canonicalTaskIDRE = regexp.MustCompile(`^([A-Z]{3})-TSK(` + OperatorJournalNumberPattern + `)$`)
	canonicalRunIDRE  = regexp.MustCompile(`^([A-Z]{3}-TSK(` + OperatorJournalNumberPattern + `))-RUN(` + OperatorJournalNumberPattern + `)$`)
	canonicalADRIDRE  = regexp.MustCompile(`^([A-Z]{3})-ADR(` + OperatorJournalNumberPattern + `)$`)
	legacyTaskIDRE    = regexp.MustCompile(`^([A-Z]{3})-T([1-9][0-9]*)$`)
	legacyRunIDRE     = regexp.MustCompile(`^([A-Z]{3}-T[1-9][0-9]*)-R([1-9][0-9]*)$`)
	legacyADRIDRE     = regexp.MustCompile(`^([A-Z]{3})-A(` + OperatorJournalNumberPattern + `)$`)
	shaRE             = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Project struct {
	SchemaVersion      int       `json:"schema_version"`
	ID                 string    `json:"id"`
	RepositoryURL      string    `json:"repository_url"`
	DefaultBranch      string    `json:"default_branch"`
	WorkflowRepository string    `json:"workflow_repository"`
	WorkflowCommit     string    `json:"workflow_commit"`
	Status             string    `json:"status"`
	ActiveTaskID       string    `json:"active_task_id,omitempty"`
	ActiveRunID        string    `json:"active_run_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ProjectIdentifiers struct {
	SchemaVersion  int    `json:"schema_version"`
	ProjectID      string `json:"project_id"`
	ProjectCode    string `json:"project_code"`
	NextTaskNumber uint64 `json:"next_task_number"`
	NextADRNumber  uint64 `json:"next_adr_number"`
}

// TaskRunCounter is stored next to one canonical task and is the only
// allocator for that task's run numbers.  It is deliberately separate from
// project-wide identifiers so concurrent tasks cannot consume each other's
// run sequence.
type TaskRunCounter struct {
	SchemaVersion int    `json:"schema_version"`
	ProjectID     string `json:"project_id"`
	TaskID        string `json:"task_id"`
	NextRunNumber uint64 `json:"next_run_number"`
}

func (c *TaskRunCounter) UnmarshalJSON(data []byte) error {
	fields, err := decodeProjectIdentifiersObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "schema_version", "project_id", "task_id", "next_run_number":
		default:
			return fmt.Errorf("unknown task run counter field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "project_id", "task_id", "next_run_number"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("task run counter field %q is required", key)
		}
	}
	schemaVersion, err := parseJSONInteger(fields["schema_version"])
	if err != nil || schemaVersion > uint64(^uint(0)>>1) {
		if err == nil {
			err = fmt.Errorf("overflows int")
		}
		return fmt.Errorf("schema_version: %w", err)
	}
	var projectID, taskID string
	if err := json.Unmarshal(fields["project_id"], &projectID); err != nil {
		return fmt.Errorf("project_id: %w", err)
	}
	if err := json.Unmarshal(fields["task_id"], &taskID); err != nil {
		return fmt.Errorf("task_id: %w", err)
	}
	next, err := parseJSONInteger(fields["next_run_number"])
	if err != nil {
		return fmt.Errorf("next_run_number: %w", err)
	}
	*c = TaskRunCounter{SchemaVersion: int(schemaVersion), ProjectID: projectID, TaskID: taskID, NextRunNumber: next}
	return nil
}

func (p *ProjectIdentifiers) UnmarshalJSON(data []byte) error {
	fields, err := decodeProjectIdentifiersObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "schema_version", "project_id", "project_code", "next_task_number", "next_adr_number":
		default:
			return fmt.Errorf("unknown project identifiers field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "project_id", "project_code", "next_task_number", "next_adr_number"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("project identifiers field %q is required", key)
		}
	}

	schemaVersion, err := parseJSONInteger(fields["schema_version"])
	if err != nil {
		return fmt.Errorf("schema_version: %w", err)
	}
	if schemaVersion > uint64(^uint(0)>>1) {
		return fmt.Errorf("schema_version overflows int")
	}
	var projectID, projectCode string
	if err := json.Unmarshal(fields["project_id"], &projectID); err != nil {
		return fmt.Errorf("project_id: %w", err)
	}
	if err := json.Unmarshal(fields["project_code"], &projectCode); err != nil {
		return fmt.Errorf("project_code: %w", err)
	}
	nextTaskNumber, err := parseJSONInteger(fields["next_task_number"])
	if err != nil {
		return fmt.Errorf("next_task_number: %w", err)
	}
	nextADRNumber, err := parseJSONInteger(fields["next_adr_number"])
	if err != nil {
		return fmt.Errorf("next_adr_number: %w", err)
	}
	*p = ProjectIdentifiers{SchemaVersion: int(schemaVersion), ProjectID: projectID, ProjectCode: projectCode, NextTaskNumber: nextTaskNumber, NextADRNumber: nextADRNumber}
	return nil
}

func decodeProjectIdentifiersObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("project identifiers must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("project identifiers object key must be a string")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate project identifiers field %q", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("project identifiers object is not closed")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("project identifiers JSON has trailing content")
	}
	return fields, nil
}

type Plan struct {
	SchemaVersion    int                `json:"schema_version"`
	ProjectID        string             `json:"project_id"`
	Revision         int                `json:"revision"`
	Title            string             `json:"title"`
	Summary          string             `json:"summary"`
	CurrentObjective string             `json:"current_objective"`
	Queue            []string           `json:"queue"`
	Sections         []PlanSectionIndex `json:"sections"`
	ActiveTaskID     string             `json:"active_task_id,omitempty"`
	ActiveRunID      string             `json:"active_run_id,omitempty"`
	UpdatedBy        string             `json:"updated_by"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

type PlanSectionIndex struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	ShortDescription string `json:"short_description"`
	Revision         int    `json:"revision"`
}

type PlanSection struct {
	SchemaVersion    int       `json:"schema_version"`
	ProjectID        string    `json:"project_id"`
	ID               string    `json:"id"`
	Revision         int       `json:"revision"`
	Title            string    `json:"title"`
	ShortDescription string    `json:"short_description"`
	Description      string    `json:"description"`
	UpdatedBy        string    `json:"updated_by"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type PlanRender struct {
	SchemaVersion    int    `json:"schema_version"`
	ProjectID        string `json:"project_id"`
	Revision         int    `json:"revision"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
	CurrentObjective string `json:"current_objective"`
	Text             string `json:"text"`
}

type PlanStatus struct {
	SchemaVersion    int       `json:"schema_version"`
	ProjectID        string    `json:"project_id"`
	Revision         int       `json:"revision"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	CurrentObjective string    `json:"current_objective"`
	Queue            []string  `json:"queue"`
	Sections         []string  `json:"-"`
	ActiveTaskID     string    `json:"active_task_id,omitempty"`
	ActiveRunID      string    `json:"active_run_id,omitempty"`
	UpdatedBy        string    `json:"updated_by"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (p Plan) StatusView() PlanStatus {
	return PlanStatus{SchemaVersion: p.SchemaVersion, ProjectID: p.ProjectID, Revision: p.Revision, Title: p.Title, Summary: p.Summary, CurrentObjective: p.CurrentObjective, Queue: append([]string{}, p.Queue...), ActiveTaskID: p.ActiveTaskID, ActiveRunID: p.ActiveRunID, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt}
}

type ADR struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	Context       string    `json:"context"`
	Decision      string    `json:"decision"`
	Consequences  string    `json:"consequences"`
	Supersedes    string    `json:"supersedes,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Task struct {
	SchemaVersion          int       `json:"schema_version"`
	ID                     string    `json:"id"`
	SHA256                 string    `json:"sha256"`
	ProjectID              string    `json:"project_id"`
	Title                  string    `json:"title"`
	Objective              string    `json:"objective"`
	Branch                 string    `json:"branch"`
	BaseRevision           string    `json:"base_revision"`
	AcceptanceCriteria     []string  `json:"acceptance_criteria"`
	Constraints            []string  `json:"constraints"`
	RequiredGates          []string  `json:"required_gates,omitempty"`
	WorkflowPolicyRevision int       `json:"workflow_policy_revision"`
	OperationClass         string    `json:"operation_class"`
	EffectiveCIField       string    `json:"effective_ci_field"`
	EffectiveCIMode        string    `json:"effective_ci_mode"`
	WaitForCI              bool      `json:"wait_for_ci"`
	CIBlocking             bool      `json:"ci_blocking"`
	AgentMayWait           bool      `json:"agent_may_wait"`
	Status                 string    `json:"status"`
	Supersedes             string    `json:"supersedes,omitempty"`
	CreatedBy              string    `json:"created_by"`
	CreatedAt              time.Time `json:"created_at"`
}

type TaskState struct {
	SchemaVersion     int       `json:"schema_version"`
	TaskID            string    `json:"task_id"`
	TaskSHA256        string    `json:"task_sha256"`
	Status            string    `json:"status"`
	SupersededBy      string    `json:"superseded_by,omitempty"`
	ReviewedHead      string    `json:"reviewed_head,omitempty"`
	DeferredReason    string    `json:"deferred_reason,omitempty"`
	IntegrationBranch string    `json:"integration_branch,omitempty"`
	IntegrationHead   string    `json:"integration_head,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Run struct {
	SchemaVersion      int        `json:"schema_version"`
	ID                 string     `json:"id"`
	TaskID             string     `json:"task_id"`
	TaskSHA256         string     `json:"task_sha256"`
	TaskRevision       int        `json:"task_revision,omitempty"`
	TaskRevisionSHA256 string     `json:"task_revision_sha256,omitempty"`
	TaskRunNumber      uint64     `json:"task_run_number,omitempty"`
	ProjectID          string     `json:"project_id"`
	GatewayID          string     `json:"gateway_id"`
	SessionKey         string     `json:"session_key"`
	Branch             string     `json:"branch"`
	BaseRevision       string     `json:"base_revision"`
	HubRevision        string     `json:"hub_revision"`
	Status             string     `json:"status"`
	DispatchMessage    string     `json:"dispatch_message,omitempty"`
	DispatchExitCode   *int       `json:"dispatch_exit_code,omitempty"`
	DispatchStdout     string     `json:"dispatch_stdout,omitempty"`
	DispatchStderr     string     `json:"dispatch_stderr,omitempty"`
	CompletionPath     string     `json:"completion_path"`
	Historical         bool       `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	DispatchedAt       *time.Time `json:"dispatched_at,omitempty"`
	RepromptCount      int        `json:"reprompt_count,omitempty"`
	LastRepromptAt     *time.Time `json:"last_reprompt_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
}

type CompletionGateResult struct {
	ID       string `json:"id"`
	ExitCode int    `json:"exit_code"`
}

type Completion struct {
	SchemaVersion      int                    `json:"schema_version"`
	RunID              string                 `json:"run_id"`
	TaskSHA256         string                 `json:"task_sha256"`
	TaskRevision       int                    `json:"task_revision,omitempty"`
	TaskRevisionSHA256 string                 `json:"task_revision_sha256,omitempty"`
	TaskRunNumber      uint64                 `json:"task_run_number,omitempty"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
	AgentFeedback      *AgentFeedback         `json:"agent_feedback,omitempty"`
}

type RepositoryProof struct {
	Branch        string   `json:"branch"`
	Head          string   `json:"head"`
	WorktreeClean bool     `json:"worktree_clean"`
	BaseAncestor  bool     `json:"base_ancestor"`
	Commits       []string `json:"commits"`
	ChangedFiles  []string `json:"changed_files"`
	DiffScope     string   `json:"diff_scope"`
}

type Report struct {
	SchemaVersion      int                    `json:"schema_version"`
	TaskID             string                 `json:"task_id"`
	RunID              string                 `json:"run_id"`
	TaskRevision       int                    `json:"task_revision,omitempty"`
	TaskRevisionSHA256 string                 `json:"task_revision_sha256,omitempty"`
	TaskRunNumber      uint64                 `json:"task_run_number,omitempty"`
	ProjectID          string                 `json:"project_id"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
	AgentFeedback      *AgentFeedback         `json:"agent_feedback,omitempty"`
	Repository         RepositoryProof        `json:"repository"`
	HubCommit          string                 `json:"hub_commit,omitempty"`
	FinishedAt         time.Time              `json:"finished_at"`
}

func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func HashTask(t Task) (string, error) {
	t.SHA256 = ""
	data, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]), nil
}

func legacyTaskHash(t Task) (string, error) {
	t.SHA256 = ""
	legacy := struct {
		SchemaVersion      int       `json:"schema_version"`
		ID                 string    `json:"id"`
		SHA256             string    `json:"sha256"`
		ProjectID          string    `json:"project_id"`
		Title              string    `json:"title"`
		Objective          string    `json:"objective"`
		Branch             string    `json:"branch"`
		BaseRevision       string    `json:"base_revision"`
		AcceptanceCriteria []string  `json:"acceptance_criteria"`
		Constraints        []string  `json:"constraints"`
		RequiredGates      []string  `json:"required_gates,omitempty"`
		Status             string    `json:"status"`
		Supersedes         string    `json:"supersedes,omitempty"`
		CreatedBy          string    `json:"created_by"`
		CreatedAt          time.Time `json:"created_at"`
	}{t.SchemaVersion, t.ID, t.SHA256, t.ProjectID, t.Title, t.Objective, t.Branch, t.BaseRevision, t.AcceptanceCriteria, t.Constraints, t.RequiredGates, t.Status, t.Supersedes, t.CreatedBy, t.CreatedAt}
	data, err := json.Marshal(legacy)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]), nil
}

// ValidateTaskHash accepts the canonical task projection and the additive
// legacy projection used by historical tasks that predate workflow policy.
// The stored hash remains authoritative; this helper only validates it.
func ValidateTaskHash(t Task) error {
	if t.SHA256 == "" {
		return fmt.Errorf("task sha256 is empty")
	}
	h, err := HashTask(t)
	if err != nil {
		return err
	}
	if t.SHA256 == h {
		return nil
	}
	if t.OperationClass == "" && legacyWorkflowPolicyProjection(t) {
		legacy, legacyErr := legacyTaskHash(t)
		if legacyErr == nil && t.SHA256 == legacy {
			return nil
		}
	}
	return fmt.Errorf("task sha256 mismatch")
}

func legacyWorkflowPolicyProjection(t Task) bool {
	return t.WorkflowPolicyRevision == 0 &&
		t.OperationClass == "" &&
		t.EffectiveCIField == "" &&
		t.EffectiveCIMode == "" &&
		!t.WaitForCI &&
		!t.CIBlocking &&
		!t.AgentMayWait
}

func ValidateProject(v Project) error {
	if v.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported project schema_version")
	}
	if !idRE.MatchString(v.ID) {
		return fmt.Errorf("invalid project id")
	}
	if v.RepositoryURL == "" || len(v.RepositoryURL) > 2048 {
		return fmt.Errorf("invalid repository_url")
	}
	if err := ValidateBranch(v.DefaultBranch); err != nil {
		return err
	}
	return nil
}
func ValidatePlan(v Plan) error {
	if v.SchemaVersion != PlanSchemaVersion || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid plan identity")
	}
	if v.Revision < 1 || len(v.Title) < 1 || len(v.Title) > 300 || len(v.Summary) < 1 || len(v.Summary) > 500 || len(v.CurrentObjective) > 20000 {
		return fmt.Errorf("invalid plan content")
	}
	if len(v.Queue) > 200 || len(v.Sections) > 200 {
		return fmt.Errorf("plan bounds exceeded")
	}
	seen := map[string]bool{}
	for _, id := range v.Queue {
		if err := ValidateObjectIdentifier(id); err != nil {
			return fmt.Errorf("invalid plan queue item: %w", err)
		}
	}
	for _, section := range v.Sections {
		if err := ValidatePlanSectionIndex(section); err != nil {
			return err
		}
		if seen[section.ID] {
			return fmt.Errorf("duplicate plan section %q", section.ID)
		}
		seen[section.ID] = true
	}
	if v.ActiveTaskID != "" {
		if err := ValidateObjectIdentifier(v.ActiveTaskID); err != nil {
			return fmt.Errorf("invalid active task: %w", err)
		}
	}
	if v.ActiveRunID != "" {
		if err := ValidateObjectIdentifier(v.ActiveRunID); err != nil {
			return fmt.Errorf("invalid active run: %w", err)
		}
	}
	if v.UpdatedBy == "" || strings.ContainsAny(v.UpdatedBy, "\r\n\x00") || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid plan update metadata")
	}
	return nil
}

func ValidatePlanSectionIndex(v PlanSectionIndex) error {
	if err := ValidateObjectIdentifier(v.ID); err != nil {
		return fmt.Errorf("invalid plan section identity: %w", err)
	}
	if len(v.Title) < 1 || len(v.Title) > 300 || len(v.ShortDescription) < 1 || len(v.ShortDescription) > 500 || strings.ContainsAny(v.ShortDescription, "\r\n\x00") || v.Revision < 1 {
		return fmt.Errorf("invalid plan section index")
	}
	return nil
}

func ValidatePlanSection(v PlanSection) error {
	if v.SchemaVersion != PlanSchemaVersion || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid plan section identity")
	}
	if err := ValidatePlanSectionIndex(PlanSectionIndex{ID: v.ID, Title: v.Title, ShortDescription: v.ShortDescription, Revision: v.Revision}); err != nil {
		return err
	}
	if len(v.Description) > 200000 || strings.ContainsRune(v.Description, 0) || v.UpdatedBy == "" || strings.ContainsAny(v.UpdatedBy, "\r\n\x00") || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid plan section content")
	}
	return nil
}
func ValidateADR(v ADR) error {
	if v.SchemaVersion != SchemaVersion || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid ADR identity")
	}
	if err := validateAnyADRIdentifier(v.ID); err != nil {
		return err
	}
	if v.Supersedes != "" {
		if err := validateAnyADRIdentifier(v.Supersedes); err != nil {
			return fmt.Errorf("invalid supersedes: %w", err)
		}
	}
	if len(v.Title) < 3 || len(v.Title) > 300 || len(v.Context) > 100000 || len(v.Decision) > 100000 || len(v.Consequences) > 100000 {
		return fmt.Errorf("invalid ADR content")
	}
	if v.Status != "accepted" && v.Status != "superseded" {
		return fmt.Errorf("invalid ADR status")
	}
	return nil
}
func ValidateTask(v Task) error {
	if v.SchemaVersion != SchemaVersion || !idRE.MatchString(v.ProjectID) || v.ID == "" {
		return fmt.Errorf("invalid task identity")
	}
	if len(v.Title) < 3 || len(v.Title) > 300 || len(v.Objective) < 3 || len(v.Objective) > 200000 {
		return fmt.Errorf("invalid task content")
	}
	if err := ValidateBranch(v.Branch); err != nil {
		return err
	}
	if !shaRE.MatchString(v.BaseRevision) {
		return fmt.Errorf("base_revision must be a lowercase 40-character SHA")
	}
	if len(v.AcceptanceCriteria) > 200 || len(v.Constraints) > 200 || len(v.RequiredGates) > 100 {
		return fmt.Errorf("too many task entries")
	}
	if v.OperationClass != "" {
		effective := EffectiveWorkflowPolicy{WorkflowPolicyRevision: v.WorkflowPolicyRevision, OperationClass: v.OperationClass, EffectiveCIField: v.EffectiveCIField, EffectiveCIMode: v.EffectiveCIMode, WaitForCI: v.WaitForCI, CIBlocking: v.CIBlocking, AgentMayWait: v.AgentMayWait}
		if err := ValidateEffectiveWorkflowPolicy(effective); err != nil {
			return err
		}
	} else if !legacyWorkflowPolicyProjection(v) {
		return fmt.Errorf("mixed legacy and workflow-policy task projection")
	}
	for _, s := range append(append([]string{}, v.AcceptanceCriteria...), v.Constraints...) {
		if len(s) > 20000 {
			return fmt.Errorf("task entry too large")
		}
	}
	if v.SHA256 != "" {
		if err := ValidateTaskHash(v); err != nil {
			return err
		}
	}
	return nil
}
func ValidateTaskState(v TaskState, task Task) error {
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.TaskSHA256 != task.SHA256 || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid task state identity")
	}
	switch v.Status {
	case "created", "ready", "dispatched", "cancelled", "superseded", "completed":
		if v.ReviewedHead != "" || v.DeferredReason != "" || v.IntegrationBranch != "" || v.IntegrationHead != "" {
			return fmt.Errorf("task lifecycle fields are not valid for status %q", v.Status)
		}
	case "merge_ready", "deferred", "merged":
		if err := ValidateCommitSHA(v.ReviewedHead); err != nil {
			return fmt.Errorf("reviewed_head: %w", err)
		}
		if v.Status == "deferred" {
			if err := validateDeferredReason(v.DeferredReason); err != nil {
				return err
			}
		} else if v.DeferredReason != "" {
			return fmt.Errorf("deferred_reason is only valid for deferred tasks")
		}
		if v.Status == "merged" {
			if v.IntegrationBranch != "main" && v.IntegrationBranch != "develop" {
				return fmt.Errorf("integration_branch must be main or develop for merged tasks")
			}
			if err := ValidateCommitSHA(v.IntegrationHead); err != nil {
				return fmt.Errorf("integration_head: %w", err)
			}
		} else if v.IntegrationBranch != "" || v.IntegrationHead != "" {
			return fmt.Errorf("integration receipt is only valid for merged tasks")
		}
	default:
		return fmt.Errorf("invalid task state status")
	}
	return nil
}

func ValidateCommitSHA(s string) error {
	if !shaRE.MatchString(s) {
		return fmt.Errorf("must be a lowercase 40-character SHA")
	}
	return nil
}

func validateDeferredReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("deferred_reason must be non-empty")
	}
	if strings.ContainsRune(reason, '\x00') {
		return fmt.Errorf("deferred_reason must not contain NUL")
	}
	if len([]byte(reason)) > MaxDeferredReasonBytes {
		return fmt.Errorf("deferred_reason exceeds %d bytes", MaxDeferredReasonBytes)
	}
	return nil
}

func ValidateRun(v Run) error {
	if v.SchemaVersion != SchemaVersion || v.ID == "" || v.TaskID == "" || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid run identity")
	}
	switch v.Status {
	case "created", "dispatching", "dispatched", "awaiting_result", "cancel_requested", "succeeded", "failed", "needs_gpt_revision":
	default:
		return fmt.Errorf("invalid run status")
	}
	if len(v.DispatchMessage) > 512 {
		return fmt.Errorf("dispatch message too large")
	}
	if v.CompletionPath == "" {
		return fmt.Errorf("completion_path is required")
	}
	if !sha256RE(v.TaskSHA256) {
		return fmt.Errorf("invalid task hash")
	}
	if v.TaskRevision != 0 || v.TaskRevisionSHA256 != "" || v.TaskRunNumber != 0 {
		if v.TaskRevision < 1 || !sha256RE(v.TaskRevisionSHA256) || v.TaskRunNumber == 0 || v.TaskRunNumber > MaxSafeInteger {
			return fmt.Errorf("invalid revision-aware run binding")
		}
		revisionID, err := FormatTaskRevisionID(v.TaskID, v.TaskRevision)
		if err != nil {
			return err
		}
		want, err := FormatTaskRevisionRunID(revisionID, v.TaskRunNumber)
		if err != nil || v.ID != want {
			return fmt.Errorf("run id does not match revision-aware binding")
		}
	}
	return nil
}
func ValidateReport(v Report, task Task, run Run, limits ...int) error {
	limit := 10000
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.RunID != run.ID || v.ProjectID != run.ProjectID || v.FinishedAt.IsZero() {
		return fmt.Errorf("report identity mismatch")
	}
	if run.TaskRevision != 0 && (v.TaskRevision != run.TaskRevision || v.TaskRevisionSHA256 != run.TaskRevisionSHA256 || v.TaskRunNumber != run.TaskRunNumber) {
		return fmt.Errorf("report revision-aware binding mismatch")
	}
	if v.Status != "succeeded" && v.Status != "failed" && v.Status != "needs_gpt_revision" {
		return fmt.Errorf("invalid report status")
	}
	if v.AgentFeedback != nil {
		if err := ValidateAgentFeedback(*v.AgentFeedback); err != nil {
			return err
		}
	}
	if err := utf8Bounded(v.Summary, 4096, "report summary"); err != nil {
		return err
	}
	if strings.TrimSpace(v.Summary) == "" {
		return fmt.Errorf("report summary must be non-empty")
	}
	if len(v.GateResults) > 128 || len(v.AcceptanceCoverage) > 128 || len(v.Deviations) > 64 || len(v.RemainingRisks) > 64 {
		return fmt.Errorf("report bounds exceeded")
	}
	for i, gate := range v.GateResults {
		if gate.ID != fmt.Sprintf("G%d", i+1) {
			return fmt.Errorf("report gate results are not positional")
		}
	}
	if v.Status == "succeeded" {
		if len(v.GateResults) != len(task.RequiredGates) || len(v.AcceptanceCoverage) != len(task.AcceptanceCriteria) {
			return fmt.Errorf("report success receipts are incomplete")
		}
		for _, gate := range v.GateResults {
			if gate.ExitCode != 0 {
				return fmt.Errorf("report success contains failed gate")
			}
		}
		for i, id := range v.AcceptanceCoverage {
			if id != fmt.Sprintf("AC%d", i+1) {
				return fmt.Errorf("report success acceptance is not positional")
			}
		}
	} else {
		if len(v.GateResults) > len(task.RequiredGates) || len(v.AcceptanceCoverage) > len(task.AcceptanceCriteria) {
			return fmt.Errorf("report receipts exceed task bounds")
		}
		last := 0
		seen := map[string]bool{}
		for _, id := range v.AcceptanceCoverage {
			if !completionACRE.MatchString(id) || seen[id] {
				return fmt.Errorf("report acceptance is not ordered and unique")
			}
			var n int
			_, _ = fmt.Sscanf(id, "AC%d", &n)
			if n <= last || n > len(task.AcceptanceCriteria) {
				return fmt.Errorf("report acceptance is out of bounds")
			}
			seen[id] = true
			last = n
		}
	}
	for _, entry := range append(append([]string{}, v.Deviations...), v.RemainingRisks...) {
		if err := utf8Bounded(entry, 2048, "report entry"); err != nil {
			return err
		}
		if strings.TrimSpace(entry) == "" {
			return fmt.Errorf("report entry must be non-empty")
		}
	}
	if ValidateBranch(v.Repository.Branch) != nil || !shaRE.MatchString(v.Repository.Head) || v.Repository.DiffScope != run.BaseRevision+".."+v.Repository.Head {
		return fmt.Errorf("invalid repository proof")
	}
	if len(v.Repository.Commits) > limit || len(v.Repository.ChangedFiles) > limit {
		return fmt.Errorf("repository proof exceeds configured limit")
	}
	seenCommits := map[string]bool{}
	for _, sha := range v.Repository.Commits {
		if !shaRE.MatchString(sha) || seenCommits[sha] {
			return fmt.Errorf("invalid or duplicate repository commit")
		}
		seenCommits[sha] = true
	}
	if len(v.Repository.Commits) > 0 && v.Repository.Commits[len(v.Repository.Commits)-1] != v.Repository.Head {
		return fmt.Errorf("repository commit order does not end at HEAD")
	}
	canonicalFiles := CanonicalStrings(v.Repository.ChangedFiles)
	if strings.Join(canonicalFiles, "\x00") != strings.Join(v.Repository.ChangedFiles, "\x00") {
		return fmt.Errorf("repository changed files are not canonical")
	}
	seenFiles := map[string]bool{}
	for _, path := range v.Repository.ChangedFiles {
		if seenFiles[path] {
			return fmt.Errorf("repository changed files contain duplicates")
		}
		seenFiles[path] = true
	}
	if v.Status == "succeeded" && (!v.Repository.BaseAncestor || !v.Repository.WorktreeClean || v.Repository.Branch != run.Branch) {
		return fmt.Errorf("successful report lacks base ancestry proof")
	}
	for _, path := range v.Repository.ChangedFiles {
		if err := ValidateRelativePath(path); err != nil {
			return err
		}
	}
	for _, sha := range v.Repository.Commits {
		if !shaRE.MatchString(sha) {
			return fmt.Errorf("invalid repository commit")
		}
	}
	return nil
}
func ValidateProjectIdentifier(s string) error {
	if !idRE.MatchString(s) {
		return fmt.Errorf("invalid project identifier")
	}
	return nil
}
func ValidateProjectCode(s string) error {
	if !projectCodeRE.MatchString(s) {
		return fmt.Errorf("project_code must be exactly three uppercase letters")
	}
	return nil
}
func ValidateCompactIDNumber(n uint64) error {
	if n < 1 || n > MaxSafeInteger {
		return fmt.Errorf("compact identifier number must be between 1 and %d", MaxSafeInteger)
	}
	return nil
}
func ValidateProjectIdentifiers(v ProjectIdentifiers) error {
	if v.SchemaVersion != SchemaVersion {
		return fmt.Errorf("invalid project identifiers schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateProjectCode(v.ProjectCode); err != nil {
		return err
	}
	if err := ValidateCompactIDNumber(v.NextTaskNumber); err != nil {
		return fmt.Errorf("next_task_number: %w", err)
	}
	if err := ValidateCompactIDNumber(v.NextADRNumber); err != nil {
		return fmt.Errorf("next_adr_number: %w", err)
	}
	return nil
}

func ValidateTaskRunCounter(v TaskRunCounter) error {
	if v.SchemaVersion != SchemaVersion {
		return fmt.Errorf("invalid task run counter schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if _, _, err := ParseTaskID(v.TaskID); err != nil {
		return fmt.Errorf("task_id: %w", err)
	}
	if v.NextRunNumber == 0 || v.NextRunNumber > MaxSafeInteger {
		return fmt.Errorf("next_run_number must be between 1 and %d", MaxSafeInteger)
	}
	return nil
}

func FormatTaskID(projectCode string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-TSK%d", projectCode, number), nil
}
func ParseTaskID(value string) (string, uint64, error) {
	matches := canonicalTaskIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid canonical task ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

// ParseHistoricalTaskID is for exact read-only decoding of pre-cutover task
// records. Operational creation and mutation must use ParseTaskID.
func ParseHistoricalTaskID(value string) (string, uint64, error) {
	matches := legacyTaskIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid historical task ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}
func ValidateTaskIDForProject(value, expectedProjectCode string) error {
	_, err := ParseTaskIDForProject(value, expectedProjectCode)
	return err
}
func ParseTaskIDForProject(value, expectedProjectCode string) (uint64, error) {
	if err := ValidateProjectCode(expectedProjectCode); err != nil {
		return 0, fmt.Errorf("expected project code: %w", err)
	}
	projectCode, number, err := ParseTaskID(value)
	if err != nil {
		return 0, err
	}
	if projectCode != expectedProjectCode {
		return 0, fmt.Errorf("compact task ID project code %q does not match expected project code %q", projectCode, expectedProjectCode)
	}
	return number, nil
}
func FormatRunID(taskID string, number uint64) (string, error) {
	if _, _, err := ParseTaskID(taskID); err != nil {
		return "", fmt.Errorf("task ID: %w", err)
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-RUN%d", taskID, number), nil
}
func ParseRunID(value string) (string, uint64, error) {
	matches := canonicalRunIDRE.FindStringSubmatch(value)
	if len(matches) != 4 {
		return "", 0, fmt.Errorf("invalid canonical run ID")
	}
	if _, _, err := ParseTaskID(matches[1]); err != nil {
		return "", 0, err
	}
	number, err := parseCompactIDNumber(matches[3])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ParseHistoricalRunID(value string) (string, uint64, error) {
	matches := legacyRunIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid historical run ID")
	}
	if _, _, err := ParseHistoricalTaskID(matches[1]); err != nil {
		return "", 0, err
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}
func ValidateRunIDForProject(value, expectedProjectCode string) error {
	_, _, err := ParseRunIDForProject(value, expectedProjectCode)
	return err
}
func ParseRunIDForProject(value, expectedProjectCode string) (string, uint64, error) {
	if err := ValidateProjectCode(expectedProjectCode); err != nil {
		return "", 0, fmt.Errorf("expected project code: %w", err)
	}
	taskID, number, err := ParseRunID(value)
	if err != nil {
		return "", 0, err
	}
	projectCode, _, err := ParseTaskID(taskID)
	if err != nil {
		return "", 0, err
	}
	if projectCode != expectedProjectCode {
		return "", 0, fmt.Errorf("run ID project code %q does not match expected project code %q", projectCode, expectedProjectCode)
	}
	return taskID, number, nil
}
func FormatADRID(projectCode string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-ADR%d", projectCode, number), nil
}
func ParseADRID(value string) (string, uint64, error) {
	matches := canonicalADRIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid canonical ADR ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ParseHistoricalADRID(value string) (string, uint64, error) {
	matches := legacyADRIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid historical ADR ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}
func ValidateADRIDForProject(value, expectedProjectCode string) error {
	_, err := ParseADRIDForProject(value, expectedProjectCode)
	return err
}
func ParseADRIDForProject(value, expectedProjectCode string) (uint64, error) {
	if err := ValidateProjectCode(expectedProjectCode); err != nil {
		return 0, fmt.Errorf("expected project code: %w", err)
	}
	projectCode, number, err := ParseADRID(value)
	if err != nil {
		return 0, err
	}
	if projectCode != expectedProjectCode {
		return 0, fmt.Errorf("compact ADR ID project code %q does not match expected project code %q", projectCode, expectedProjectCode)
	}
	return number, nil
}
func parseCompactIDNumber(value string) (uint64, error) {
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid compact identifier number")
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return 0, err
	}
	return number, nil
}
func parseJSONInteger(data []byte) (uint64, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("must be a JSON number")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, fmt.Errorf("must contain one JSON value")
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("must be a JSON number")
	}
	return parseJSONNumberInteger(number.String())
}
func parseJSONNumberInteger(value string) (uint64, error) {
	if strings.HasPrefix(value, "-") {
		return 0, fmt.Errorf("must be non-negative")
	}
	if strings.HasPrefix(value, "+") {
		return 0, fmt.Errorf("must be a JSON number")
	}
	mantissa := value
	var exponent int64
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		exponentText := mantissa[index+1:]
		parsed, err := strconv.ParseInt(exponentText, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("number exponent overflows")
		}
		exponent = parsed
		mantissa = mantissa[:index]
	}
	parts := strings.SplitN(mantissa, ".", 2)
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	digits := whole + frac
	if strings.Trim(digits, "0") == "" {
		return 0, nil
	}
	scale := int64(len(frac)) - exponent
	if scale > int64(len(digits)) {
		return 0, fmt.Errorf("must be an integer")
	}
	integerDigits := digits
	if scale > 0 {
		cut := len(digits) - int(scale)
		if !strings.HasSuffix(digits, strings.Repeat("0", int(scale))) {
			return 0, fmt.Errorf("must be an integer")
		}
		integerDigits = digits[:cut]
	} else if scale < 0 {
		zeros := -scale
		if zeros > int64(len(strconv.FormatUint(MaxSafeInteger, 10))) {
			return 0, fmt.Errorf("overflows maximum safe integer")
		}
		integerDigits += strings.Repeat("0", int(zeros))
	}
	integerDigits = strings.TrimLeft(integerDigits, "0")
	if integerDigits == "" {
		return 0, nil
	}
	maxText := strconv.FormatUint(MaxSafeInteger, 10)
	if len(integerDigits) > len(maxText) || (len(integerDigits) == len(maxText) && integerDigits > maxText) {
		return 0, fmt.Errorf("overflows maximum safe integer")
	}
	number, err := strconv.ParseUint(integerDigits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid JSON integer")
	}
	return number, nil
}
func ValidateObjectIdentifier(s string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(s) {
		return fmt.Errorf("invalid object identifier")
	}
	return nil
}
func ValidateADRIdentifier(s string) error {
	if !adrIDRE.MatchString(s) {
		return fmt.Errorf("invalid ADR identifier")
	}
	return nil
}

func validateAnyADRIdentifier(s string) error {
	if ValidateADRIdentifier(s) == nil || ValidateCanonicalADRIdentifier(s) == nil {
		return nil
	}
	return fmt.Errorf("invalid ADR identifier")
}

func ValidateCanonicalTaskID(s string) error {
	_, _, err := ParseTaskID(s)
	return err
}

func ValidateCanonicalRunID(s string) error {
	_, _, err := ParseRunID(s)
	return err
}

func ValidateCanonicalADRIdentifier(s string) error {
	_, _, err := ParseADRID(s)
	return err
}

func ValidateTaskSlug(s string) error {
	if len(s) < 1 || len(s) > 80 || !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(s) {
		return fmt.Errorf("invalid task slug")
	}
	return nil
}

func ValidateDurableIdentifier(s string) error {
	if ValidateCanonicalTaskID(s) == nil || ValidateCanonicalRunID(s) == nil || ValidateCanonicalADRIdentifier(s) == nil || ValidateOperatorEventID(s) == nil {
		return nil
	}
	return fmt.Errorf("invalid canonical durable identifier")
}
func ValidateBranch(s string) error {
	if s == "" || len(s) > 255 || strings.ContainsAny(s, "\x00\r\n ~^:?*[\\") || strings.HasPrefix(s, "-") || strings.Contains(s, "..") || strings.HasSuffix(s, "/") {
		return fmt.Errorf("invalid branch")
	}
	return nil
}
func ValidateRevision(s string) error {
	if s == "" || len(s) > 255 || strings.ContainsAny(s, "\x00\r\n ~^:?*[\\") || strings.HasPrefix(s, "-") || strings.Contains(s, "..") {
		return fmt.Errorf("invalid revision")
	}
	return nil
}
func ValidateRelativePath(p string) error {
	if p == "" || len(p) > 4096 || filepath.IsAbs(p) || strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
		return fmt.Errorf("invalid relative path")
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	first := strings.Split(clean, "/")[0]
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.EqualFold(first, ".git") {
		return fmt.Errorf("path escapes root")
	}
	return nil
}
func CanonicalStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
func sha256RE(s string) bool { return len(s) == 64 && regexp.MustCompile(`^[0-9a-f]+$`).MatchString(s) }
