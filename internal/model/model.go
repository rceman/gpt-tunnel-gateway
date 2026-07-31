package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1
const PlanSchemaVersion = 2

var (
	idRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	adrIDRE = regexp.MustCompile(`^ADR-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	shaRE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
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
	Sections         []string  `json:"sections"`
	ActiveTaskID     string    `json:"active_task_id,omitempty"`
	ActiveRunID      string    `json:"active_run_id,omitempty"`
	UpdatedBy        string    `json:"updated_by"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (p Plan) StatusView() PlanStatus {
	sections := make([]string, 0, len(p.Sections))
	for _, section := range p.Sections {
		sections = append(sections, fmt.Sprintf("* %s - %s", section.Title, section.ShortDescription))
	}
	return PlanStatus{SchemaVersion: p.SchemaVersion, ProjectID: p.ProjectID, Revision: p.Revision, Title: p.Title, Summary: p.Summary, CurrentObjective: p.CurrentObjective, Queue: append([]string{}, p.Queue...), Sections: sections, ActiveTaskID: p.ActiveTaskID, ActiveRunID: p.ActiveRunID, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt}
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
}

type TaskState struct {
	SchemaVersion int       `json:"schema_version"`
	TaskID        string    `json:"task_id"`
	TaskSHA256    string    `json:"task_sha256"`
	Status        string    `json:"status"`
	SupersededBy  string    `json:"superseded_by,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Run struct {
	SchemaVersion    int    `json:"schema_version"`
	ID               string `json:"id"`
	TaskID           string `json:"task_id"`
	TaskSHA256       string `json:"task_sha256"`
	ProjectID        string `json:"project_id"`
	GatewayID        string `json:"gateway_id"`
	SessionKey       string `json:"session_key"`
	Branch           string `json:"branch"`
	BaseRevision     string `json:"base_revision"`
	HubRevision      string `json:"hub_revision"`
	Status           string `json:"status"`
	DispatchMessage  string `json:"dispatch_message,omitempty"`
	DispatchExitCode *int   `json:"dispatch_exit_code,omitempty"`
	DispatchStdout   string `json:"dispatch_stdout,omitempty"`
	DispatchStderr   string `json:"dispatch_stderr,omitempty"`
	CompletionPath   string `json:"completion_path"`
	// Deprecated fields are retained only so immutable pre-2.0 local records can
	// be decoded by maintenance tooling. They are never populated or serialized.
	ResultPath     string     `json:"-"`
	EvidencePath   string     `json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	DispatchedAt   *time.Time `json:"dispatched_at,omitempty"`
	RepromptCount  int        `json:"reprompt_count,omitempty"`
	LastRepromptAt *time.Time `json:"last_reprompt_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type CommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Result   string `json:"result"`
}

type AgentResult struct {
	SchemaVersion      int             `json:"schema_version"`
	TaskID             string          `json:"task_id"`
	TaskSHA256         string          `json:"task_sha256"`
	RunID              string          `json:"run_id"`
	Status             string          `json:"status"`
	Summary            string          `json:"summary"`
	Commits            []string        `json:"commits"`
	ChangedFiles       []string        `json:"changed_files"`
	Commands           []CommandResult `json:"commands"`
	AcceptanceCoverage []string        `json:"acceptance_coverage"`
	Deviations         []string        `json:"deviations"`
	RemainingRisks     []string        `json:"remaining_risks"`
	FinishedAt         time.Time       `json:"finished_at"`
}

type Evidence struct {
	SchemaVersion int       `json:"schema_version"`
	TaskID        string    `json:"task_id"`
	RunID         string    `json:"run_id"`
	ProjectHead   string    `json:"project_head"`
	Branch        string    `json:"branch"`
	WorktreeClean bool      `json:"worktree_clean"`
	Notes         []string  `json:"notes,omitempty"`
	RecordedAt    time.Time `json:"recorded_at"`
}

type CompletionGateResult struct {
	ID       string `json:"id"`
	ExitCode int    `json:"exit_code"`
}

type Completion struct {
	SchemaVersion      int                    `json:"schema_version"`
	RunID              string                 `json:"run_id"`
	TaskSHA256         string                 `json:"task_sha256"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
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
	ProjectID          string                 `json:"project_id"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
	Repository         RepositoryProof        `json:"repository"`
	HubCommit          string                 `json:"hub_commit,omitempty"`
	FinishedAt         time.Time              `json:"finished_at"`
	// Deprecated projection fields are not part of the canonical report.
	Commits      []string        `json:"-"`
	ChangedFiles []string        `json:"-"`
	Commands     []CommandResult `json:"-"`
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
	if err := ValidateADRIdentifier(v.ID); err != nil {
		return err
	}
	if v.Supersedes != "" {
		if err := ValidateADRIdentifier(v.Supersedes); err != nil {
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
	for _, s := range append(append([]string{}, v.AcceptanceCriteria...), v.Constraints...) {
		if len(s) > 20000 {
			return fmt.Errorf("task entry too large")
		}
	}
	h, err := HashTask(v)
	if err != nil {
		return err
	}
	if v.SHA256 != "" && v.SHA256 != h {
		return fmt.Errorf("task sha256 mismatch")
	}
	return nil
}
func ValidateTaskState(v TaskState, task Task) error {
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.TaskSHA256 != task.SHA256 || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid task state identity")
	}
	switch v.Status {
	case "created", "ready", "dispatched", "cancelled", "superseded", "completed":
	default:
		return fmt.Errorf("invalid task state status")
	}
	return nil
}

func ValidateRun(v Run) error {
	if v.SchemaVersion != SchemaVersion || v.ID == "" || v.TaskID == "" || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid run identity")
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
	return nil
}
func ValidateAgentResult(v AgentResult, task Task, run Run) error {
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.TaskSHA256 != task.SHA256 || v.RunID != run.ID {
		return fmt.Errorf("result identity mismatch")
	}
	switch v.Status {
	case "succeeded", "failed", "blocked", "cancelled", "timed_out":
	default:
		return fmt.Errorf("invalid result status")
	}
	if len(v.Summary) < 1 || len(v.Summary) > 20000 || len(v.Commits) > 100 || len(v.ChangedFiles) > 5000 || len(v.Commands) > 500 {
		return fmt.Errorf("result bounds exceeded")
	}
	for _, sha := range v.Commits {
		if !shaRE.MatchString(sha) {
			return fmt.Errorf("invalid commit SHA %q", sha)
		}
	}
	for _, p := range v.ChangedFiles {
		if err := ValidateRelativePath(p); err != nil {
			return fmt.Errorf("changed file: %w", err)
		}
	}
	if v.FinishedAt.IsZero() {
		return fmt.Errorf("finished_at is required")
	}
	if v.Status == "succeeded" {
		covered := map[string]bool{}
		for _, c := range v.AcceptanceCoverage {
			covered[c] = true
		}
		for _, c := range task.AcceptanceCriteria {
			if !covered[c] {
				return fmt.Errorf("acceptance criterion not covered: %s", c)
			}
		}
		gates := map[string]int{}
		for _, cmd := range v.Commands {
			gates[cmd.Command] = cmd.ExitCode
		}
		for _, gate := range task.RequiredGates {
			code, ok := gates[gate]
			if !ok {
				return fmt.Errorf("required gate not reported: %s", gate)
			}
			if code != 0 {
				return fmt.Errorf("required gate failed: %s", gate)
			}
		}
	}
	return nil
}
func ValidateEvidence(v Evidence, task Task, run Run) error {
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.RunID != run.ID {
		return fmt.Errorf("evidence identity mismatch")
	}
	if !shaRE.MatchString(v.ProjectHead) || v.Branch != run.Branch || v.RecordedAt.IsZero() {
		return fmt.Errorf("invalid evidence")
	}
	return nil
}

func ValidateReport(v Report, task Task, run Run) error {
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.RunID != run.ID || v.ProjectID != run.ProjectID || v.FinishedAt.IsZero() {
		return fmt.Errorf("report identity mismatch")
	}
	if v.Status != "succeeded" && v.Status != "failed" && v.Status != "needs_gpt_revision" {
		return fmt.Errorf("invalid report status")
	}
	if err := utf8Bounded(v.Summary, 4096, "report summary"); err != nil {
		return err
	}
	if len(v.GateResults) > 128 || len(v.AcceptanceCoverage) > 128 || len(v.Deviations) > 64 || len(v.RemainingRisks) > 64 {
		return fmt.Errorf("report bounds exceeded")
	}
	if ValidateBranch(v.Repository.Branch) != nil || !shaRE.MatchString(v.Repository.Head) || v.Repository.DiffScope == "" {
		return fmt.Errorf("invalid repository proof")
	}
	if v.Status == "succeeded" && !v.Repository.BaseAncestor {
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
