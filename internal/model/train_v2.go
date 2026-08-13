package model

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	TrainV2SchemaVersion        = 1
	MaxTrainV2Items             = 32
	TrainV2CutoverSchemaVersion = 1
	TrainV2Planned              = "planned"
	TrainV2Running              = "running"
	TrainV2Paused               = "paused"
	TrainV2Blocked              = "blocked"
	TrainV2ReadyForIntegration  = "ready_for_integration"
	TrainV2Completed            = "completed"
	TrainV2RecoveryQuarantined  = "recovery_quarantined"
	TrainV2ItemQueued           = "queued"
	TrainV2ItemRunning          = "running"
	TrainV2ItemFinalized        = "finalized"
	TrainV2ItemReviewed         = "reviewed"
	TrainV2ItemBlocked          = "blocked"
	TrainV2StartSchemaVersion   = 1
	TrainV2StartActive          = "active"
	TrainV2AttemptSchemaVersion = 1
	TrainV2AttemptRunning       = "running"
	TrainV2AttemptSucceeded     = "succeeded"
	TrainV2AttemptFailed        = "failed"
	TrainV2AttemptAborted       = "aborted"
	TrainV2AttemptRecovered     = "recovered"
)

// TrainV2CutoverReceipt is the durable proof that the project's execution
// authority was switched once, after the legacy graph and runtime were
// reconciled. It is deliberately separate from Plan history.
type TrainV2CutoverReceipt struct {
	SchemaVersion               int       `json:"schema_version"`
	ProjectID                   string    `json:"project_id"`
	ExecutionModel              string    `json:"execution_model"`
	ConfigurationRevision       int       `json:"configuration_revision"`
	SourceHead                  string    `json:"source_head"`
	RuntimeHead                 string    `json:"runtime_head"`
	ActionSchemaRevision        int       `json:"action_schema_revision"`
	HistoricalCompatibility     string    `json:"historical_compatibility"`
	MaterializationAcknowledged bool      `json:"materialization_acknowledged"`
	PlanRetirementAcknowledged  bool      `json:"plan_retirement_acknowledged"`
	NextAction                  string    `json:"next_action"`
	UpdatedBy                   string    `json:"updated_by"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

func ValidateTrainV2CutoverReceipt(v TrainV2CutoverReceipt) error {
	if v.SchemaVersion != TrainV2CutoverSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.ExecutionModel != "train_v2" || v.ConfigurationRevision < 1 {
		return fmt.Errorf("invalid train v2 cutover identity")
	}
	if !shaRE.MatchString(v.SourceHead) || !shaRE.MatchString(v.RuntimeHead) {
		return fmt.Errorf("invalid train v2 cutover heads")
	}
	if v.ActionSchemaRevision < 1 || v.HistoricalCompatibility != "preserved" || !v.MaterializationAcknowledged || !v.PlanRetirementAcknowledged {
		return fmt.Errorf("invalid train v2 cutover evidence")
	}
	if strings.TrimSpace(v.NextAction) == "" || strings.ContainsAny(v.NextAction, "\x00\r\n") || v.UpdatedBy == "" || strings.ContainsAny(v.UpdatedBy, "\x00\r\n") || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid train v2 cutover metadata")
	}
	return nil
}

var trainV2SHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TrainV2Item is the immutable admission snapshot for one ready Task. It has
// no repository, branch, worktree, Agent, or session identity; those belong to
// later train-start execution state.
type TrainV2Item struct {
	Position                int                         `json:"position"`
	TaskID                  string                      `json:"task_id"`
	TaskRevision            int                         `json:"task_revision"`
	TaskRevisionSHA256      string                      `json:"task_revision_sha256"`
	Status                  string                      `json:"status"`
	AddedAt                 time.Time                   `json:"added_at"`
	Attempts                []TrainV2Attempt            `json:"attempts,omitempty"`
	ActiveAttemptNumber     uint64                      `json:"active_attempt_number,omitempty"`
	SuccessfulAttemptNumber uint64                      `json:"successful_attempt_number,omitempty"`
	Proof                   *TrainV2ImplementationProof `json:"proof,omitempty"`
	Review                  *TrainV2ItemReview          `json:"review,omitempty"`
}

// TrainV2LegacyRunRef retains an exact, immutable reference to a pre-attempt
// Run record. The path and digest are both required because Run IDs were
// historically reusable across protocol lineages.
type TrainV2LegacyRunRef struct {
	RunID        string `json:"run_id"`
	RecordSHA256 string `json:"record_sha256"`
	Path         string `json:"path"`
}

// TrainV2Attempt is scoped to one TrainItem. It is not a project-global
// entity and must never be allocated as a durable Run ID.
type TrainV2Attempt struct {
	Number            uint64               `json:"number"`
	Status            string               `json:"status"`
	AgentID           string               `json:"agent_id"`
	AirelaySessionKey string               `json:"airelay_session_key"`
	GatewayID         string               `json:"gateway_id"`
	StartHead         string               `json:"start_head"`
	StartedAt         time.Time            `json:"started_at"`
	DispatchedAt      *time.Time           `json:"dispatched_at,omitempty"`
	FinishedAt        *time.Time           `json:"finished_at,omitempty"`
	CompletionPath    string               `json:"completion_path,omitempty"`
	ReportID          string               `json:"report_id,omitempty"`
	ReviewID          string               `json:"review_id,omitempty"`
	LegacyRunRef      *TrainV2LegacyRunRef `json:"legacy_run_ref,omitempty"`
}

// TrainV2AttemptIdentity addresses an execution attempt without a project
// global execution identifier.
type TrainV2AttemptIdentity struct {
	TrainID       string `json:"train_id"`
	ItemPosition  int    `json:"item_position"`
	TaskID        string `json:"task_id"`
	AttemptNumber uint64 `json:"attempt_number"`
}

type TrainV2AttemptMigrationItem struct {
	TaskID                string              `json:"task_id"`
	AttemptNumber         uint64              `json:"attempt_number"`
	AttemptStatus         string              `json:"attempt_status"`
	LegacyRunRef          TrainV2LegacyRunRef `json:"legacy_run_ref"`
	OriginalRunJSONBase64 string              `json:"original_run_json_base64"`
}

type TrainV2AttemptMigrationReceipt struct {
	SchemaVersion int                           `json:"schema_version"`
	ProjectID     string                        `json:"project_id"`
	TrainID       string                        `json:"train_id"`
	State         string                        `json:"state"`
	HubBefore     string                        `json:"hub_before"`
	HubAfter      string                        `json:"hub_after,omitempty"`
	Items         []TrainV2AttemptMigrationItem `json:"items"`
	Reason        string                        `json:"reason"`
	CreatedAt     time.Time                     `json:"created_at"`
	UpdatedAt     time.Time                     `json:"updated_at"`
}

// TrainV2RunRetirementRecord is immutable migration evidence for a pre-cutover
// Run record. It is not a runtime Run representation and is never resolved by
// task, train, or run ID alone.
type TrainV2RunRetirementRecord struct {
	SchemaVersion      int    `json:"schema_version"`
	ProjectID          string `json:"project_id"`
	SourcePath         string `json:"source_path"`
	SourceSHA256       string `json:"source_sha256"`
	OriginalRunID      string `json:"original_run_id"`
	OriginalRunTaskID  string `json:"original_run_task_id"`
	OriginalRunStatus  string `json:"original_run_status"`
	OriginalRunJSONB64 string `json:"original_run_json_base64"`
}

type TrainV2RunRetirementReceipt struct {
	SchemaVersion int                          `json:"schema_version"`
	ProjectID     string                       `json:"project_id"`
	State         string                       `json:"state"`
	HubBefore     string                       `json:"hub_before"`
	HubAfter      string                       `json:"hub_after"`
	Records       []TrainV2RunRetirementRecord `json:"records"`
	Reason        string                       `json:"reason"`
	CreatedAt     time.Time                    `json:"created_at"`
	UpdatedAt     time.Time                    `json:"updated_at"`
}

type TrainV2ImplementationProof struct {
	CheckpointHead    string                 `json:"checkpoint_head"`
	ImplementationSHA string                 `json:"implementation_sha"`
	ReportID          string                 `json:"report_id"`
	GateResults       []CompletionGateResult `json:"gate_results"`
	RecordedAt        time.Time              `json:"recorded_at"`
}

type TrainV2ItemReview struct {
	Outcome    string    `json:"outcome"`
	ReportID   string    `json:"report_id"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

type TrainV2AttemptReview struct {
	SchemaVersion int                   `json:"schema_version"`
	ID            string                `json:"id"`
	TrainID       string                `json:"train_id"`
	TaskID        string                `json:"task_id"`
	ItemPosition  int                   `json:"item_position"`
	AttemptNumber uint64                `json:"attempt_number"`
	Outcome       string                `json:"outcome"`
	ReviewedHead  string                `json:"reviewed_head"`
	Findings      []ReviewFinding       `json:"findings"`
	ScopeCoverage []ReviewScopeCoverage `json:"scope_coverage"`
	ReviewedAt    time.Time             `json:"reviewed_at"`
}

// TrainV2 is a non-running, ordered execution admission record. A later
// train/start transition owns Git and Agent execution identity.
type TrainV2 struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	Revision      int               `json:"revision"`
	Items         []TrainV2Item     `json:"items"`
	Status        string            `json:"status"`
	CreatedBy     string            `json:"created_by"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	FullProof     *TrainV2FullProof `json:"full_proof,omitempty"`
}

type TrainV2FullProof struct {
	CandidateHead string                 `json:"candidate_head"`
	GateResults   []CompletionGateResult `json:"gate_results"`
	RecordedAt    time.Time              `json:"recorded_at"`
}

// TrainV2StartRecord is the portable, safe execution checkpoint. Host-local
// WorktreePath and SessionKey deliberately do not appear here.
type TrainV2StartRecord struct {
	SchemaVersion             int       `json:"schema_version"`
	ProjectID                 string    `json:"project_id"`
	TrainID                   string    `json:"train_id"`
	Status                    string    `json:"status"`
	IntegrationBranch         string    `json:"integration_branch"`
	BaseRevision              string    `json:"base_revision"`
	LaneBranch                string    `json:"lane_branch"`
	CurrentItemPosition       int       `json:"current_item_position"`
	CurrentAttemptNumber      uint64    `json:"current_attempt_number"`
	CurrentTaskID             string    `json:"current_task_id"`
	CurrentTaskRevision       int       `json:"current_task_revision"`
	CurrentTaskRevisionSHA256 string    `json:"current_task_revision_sha256"`
	StartedAt                 time.Time `json:"started_at"`
}

func ValidateTrainV2StartRecord(v TrainV2StartRecord) error {
	if v.SchemaVersion != TrainV2StartSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Status != TrainV2StartActive || ValidateBranch(v.IntegrationBranch) != nil || !shaRE.MatchString(v.BaseRevision) || ValidateBranch(v.LaneBranch) != nil || ValidateCanonicalTaskID(v.CurrentTaskID) != nil || v.CurrentItemPosition < 0 || v.CurrentAttemptNumber < 1 || v.CurrentTaskRevision < 1 || !trainV2SHA256RE.MatchString(v.CurrentTaskRevisionSHA256) || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid train v2 start record")
	}
	if _, _, err := ParseTrainV2ID(v.TrainID); err != nil {
		return fmt.Errorf("invalid train v2 start train ID")
	}
	return nil
}

func ValidateTrainV2(v TrainV2) error {
	code, number, err := ParseTrainV2ID(v.ID)
	if err != nil || code == "" || number < 1 || v.SchemaVersion != TrainV2SchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Revision < 1 || v.CreatedBy == "" || strings.ContainsAny(v.CreatedBy, "\x00\r\n") || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid train v2 identity")
	}
	switch v.Status {
	case TrainV2Planned, TrainV2Running, TrainV2Paused, TrainV2Blocked, TrainV2ReadyForIntegration, TrainV2Completed, TrainV2RecoveryQuarantined:
	default:
		return fmt.Errorf("invalid train v2 status")
	}
	if len(v.Items) < 1 || len(v.Items) > MaxTrainV2Items {
		return fmt.Errorf("invalid train v2 item count")
	}
	if v.FullProof != nil {
		if err := validateTrainV2FullProof(*v.FullProof); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for position, item := range v.Items {
		if item.Position != position || ValidateCanonicalTaskID(item.TaskID) != nil || item.TaskRevision < 1 || !trainV2SHA256RE.MatchString(item.TaskRevisionSHA256) || item.AddedAt.IsZero() {
			return fmt.Errorf("invalid train v2 item %d", position)
		}
		if err := validateTrainV2ItemExecution(item); err != nil {
			return fmt.Errorf("item %d: %w", position, err)
		}
		if seen[item.TaskID] {
			return fmt.Errorf("duplicate train v2 task %q", item.TaskID)
		}
		seen[item.TaskID] = true
	}
	return nil
}

func validateTrainV2FullProof(proof TrainV2FullProof) error {
	if !shaRE.MatchString(proof.CandidateHead) || proof.RecordedAt.IsZero() || len(proof.GateResults) == 0 {
		return fmt.Errorf("invalid train v2 full proof")
	}
	return ValidateServerGateEvidence(proof.GateResults)
}

func validateTrainV2ItemExecution(item TrainV2Item) error {
	if len(item.Attempts) > 0 {
		if item.ActiveAttemptNumber > uint64(len(item.Attempts)) || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
			return fmt.Errorf("attempt pointer is outside item attempts")
		}
		for i, attempt := range item.Attempts {
			if err := ValidateTrainV2Attempt(attempt); err != nil {
				return fmt.Errorf("attempt %d: %w", i, err)
			}
			if attempt.Number != uint64(i+1) {
				return fmt.Errorf("attempt numbers must be contiguous and item-local")
			}
		}
		return nil
	}
	if item.Status != TrainV2ItemQueued {
		return fmt.Errorf("non-queued Train item requires canonical Attempts")
	}
	switch item.Status {
	case TrainV2ItemQueued:
		if item.Proof != nil || item.Review != nil {
			return fmt.Errorf("queued item has execution state")
		}
	case TrainV2ItemRunning, TrainV2ItemFinalized, TrainV2ItemReviewed, TrainV2ItemBlocked:
		return fmt.Errorf("non-queued Train item requires canonical Attempts")
	default:
		return fmt.Errorf("invalid train v2 item status")
	}
	return nil
}

func ValidateTrainV2Attempt(v TrainV2Attempt) error {
	if v.Number == 0 || v.Status == "" || ValidateObjectIdentifier(v.AgentID) != nil || strings.TrimSpace(v.AirelaySessionKey) == "" || strings.ContainsAny(v.AirelaySessionKey, "\x00\r\n") || ValidateObjectIdentifier(v.GatewayID) != nil || !shaRE.MatchString(v.StartHead) || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid train v2 attempt identity")
	}
	switch v.Status {
	case TrainV2AttemptRunning:
		if v.FinishedAt != nil {
			return fmt.Errorf("running attempt is finished")
		}
	case TrainV2AttemptSucceeded, TrainV2AttemptFailed, TrainV2AttemptAborted, TrainV2AttemptRecovered:
		if v.FinishedAt == nil || v.FinishedAt.IsZero() {
			return fmt.Errorf("terminal attempt requires finished_at")
		}
	default:
		return fmt.Errorf("invalid train v2 attempt status")
	}
	if v.LegacyRunRef != nil {
		if ValidateCanonicalRunID(v.LegacyRunRef.RunID) != nil || !trainV2SHA256RE.MatchString(v.LegacyRunRef.RecordSHA256) || strings.TrimSpace(v.LegacyRunRef.Path) == "" || strings.HasPrefix(v.LegacyRunRef.Path, "/") || strings.Contains(v.LegacyRunRef.Path, "..") {
			return fmt.Errorf("invalid legacy Run evidence reference")
		}
	}
	if v.ReviewID != "" && ValidateObjectIdentifier(v.ReviewID) != nil {
		return fmt.Errorf("invalid train v2 attempt review_id")
	}
	return nil
}

func ValidateTrainV2AttemptReview(v TrainV2AttemptReview) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateObjectIdentifier(v.ID) != nil {
		return fmt.Errorf("invalid train v2 Attempt review identity")
	}
	if _, _, err := ParseTrainV2ID(v.TrainID); err != nil || ValidateCanonicalTaskID(v.TaskID) != nil || v.ItemPosition < 0 || v.AttemptNumber == 0 || ValidateReviewOutcome(v.Outcome) != nil || !shaRE.MatchString(v.ReviewedHead) || v.ReviewedAt.IsZero() {
		return fmt.Errorf("invalid train v2 Attempt review identity")
	}
	if len(v.Findings) > 128 || len(v.ScopeCoverage) > 128 {
		return fmt.Errorf("train v2 Attempt review is too large")
	}
	return nil
}

func ValidateTrainV2RunRetirementRecord(v TrainV2RunRetirementRecord) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || strings.TrimSpace(v.SourcePath) == "" || strings.HasPrefix(v.SourcePath, "/") || strings.Contains(v.SourcePath, "..") || !strings.HasSuffix(v.SourcePath, "/run.json") || !strings.Contains(v.SourcePath, "/runs/") {
		return fmt.Errorf("invalid Train-v2 Run retirement source")
	}
	if !trainV2SHA256RE.MatchString(v.SourceSHA256) || ValidateObjectIdentifier(v.OriginalRunID) != nil || (v.OriginalRunTaskID != "" && ValidateObjectIdentifier(v.OriginalRunTaskID) != nil) || strings.TrimSpace(v.OriginalRunStatus) == "" {
		return fmt.Errorf("invalid Train-v2 Run retirement identity")
	}
	raw, err := base64.StdEncoding.DecodeString(v.OriginalRunJSONB64)
	if err != nil || len(raw) == 0 {
		return fmt.Errorf("invalid Train-v2 Run retirement bytes")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != v.SourceSHA256 || path.Base(v.SourcePath) != "run.json" {
		return fmt.Errorf("Train-v2 Run retirement digest/path mismatch")
	}
	return nil
}

func ValidateTrainV2RunRetirementReceipt(v TrainV2RunRetirementReceipt) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.State != "completed" || !shaRE.MatchString(v.HubBefore) || !shaRE.MatchString(v.HubAfter) || strings.TrimSpace(v.Reason) == "" || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || len(v.Records) > 4096 {
		return fmt.Errorf("invalid Train-v2 Run retirement receipt")
	}
	seen := make(map[string]struct{}, len(v.Records))
	for _, record := range v.Records {
		if err := ValidateTrainV2RunRetirementRecord(record); err != nil {
			return err
		}
		if _, ok := seen[record.SourcePath]; ok {
			return fmt.Errorf("duplicate Train-v2 Run retirement source")
		}
		seen[record.SourcePath] = struct{}{}
	}
	return nil
}

func validateTrainV2ImplementationProof(proof TrainV2ImplementationProof) error {
	if !shaRE.MatchString(proof.CheckpointHead) || !shaRE.MatchString(proof.ImplementationSHA) || proof.ReportID == "" || proof.RecordedAt.IsZero() {
		return fmt.Errorf("invalid train v2 implementation proof")
	}
	if err := ValidateServerGateEvidence(proof.GateResults); err != nil {
		return err
	}
	return nil
}
