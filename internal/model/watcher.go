package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	WatcherGuideSchemaVersion       = 1
	WatcherObservationSchemaVersion = 1
	WatcherStatusSchemaVersion      = 1
	WatcherMaxGuideBytes            = 64 << 10
	WatcherMaxSeenDigests           = 256
	WatcherMaxDigestBytes           = 64
	WatcherDefaultCadenceSeconds    = 30
	WatcherDefaultTailLines         = 100
	WatcherMaxTailLines             = 200
)

// WatcherGuide is the single revisioned behavioral authority for a project.
// Technical settings belong to config.ProjectConfig.Watcher and must not be
// copied into this content field.
type WatcherGuide struct {
	SchemaVersion int       `json:"schema_version"`
	ProjectID     string    `json:"project_id"`
	Revision      int       `json:"revision"`
	Content       string    `json:"content"`
	UpdatedBy     string    `json:"updated_by"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// WatcherObservationState is local hot state. It is deliberately independent
// of the Hub and is safe to discard and rebuild from the active Run.
type WatcherObservationState struct {
	SchemaVersion  int       `json:"schema_version"`
	ProjectID      string    `json:"project_id"`
	TaskID         string    `json:"task_id,omitempty"`
	RunID          string    `json:"run_id,omitempty"`
	SessionKey     string    `json:"session_key,omitempty"`
	Cursor         string    `json:"cursor,omitempty"`
	SnapshotDigest string    `json:"snapshot_digest,omitempty"`
	SeenDigests    []string  `json:"seen_digests"`
	LastTail       string    `json:"last_tail,omitempty"`
	LastTickAt     time.Time `json:"last_tick_at"`
	LastUsefulAt   time.Time `json:"last_useful_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
}

// WatcherObservation is the bounded result of one authoritative watch tick.
type WatcherObservation struct {
	SchemaVersion   int       `json:"schema_version"`
	ProjectID       string    `json:"project_id"`
	TaskID          string    `json:"task_id,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	TargetSession   string    `json:"target_session,omitempty"`
	RunStatus       string    `json:"run_status,omitempty"`
	Terminal        bool      `json:"terminal"`
	IdentityChanged bool      `json:"identity_changed"`
	Useful          bool      `json:"useful"`
	Lines           int       `json:"lines"`
	Tail            string    `json:"tail,omitempty"`
	SnapshotDigest  string    `json:"snapshot_digest,omitempty"`
	NewDigests      []string  `json:"new_digests,omitempty"`
	ObservedAt      time.Time `json:"observed_at"`
	Error           string    `json:"error,omitempty"`
}

// WatcherStatus is intentionally a compact projection. Scheduler fields are
// present so TSK161 can extend this contract without adding another status
// authority.
type WatcherStatus struct {
	SchemaVersion    int       `json:"schema_version"`
	ProjectID        string    `json:"project_id"`
	Mode             string    `json:"mode"`
	CadenceSeconds   int       `json:"cadence_seconds"`
	TailLines        int       `json:"tail_lines"`
	NudgeEnabled     bool      `json:"nudge_enabled"`
	WatcherAgentID   string    `json:"watcher_agent_id,omitempty"`
	WatcherSession   string    `json:"watcher_session,omitempty"`
	GuideRevision    int       `json:"guide_revision"`
	ActiveTaskID     string    `json:"active_task_id,omitempty"`
	ActiveRunID      string    `json:"active_run_id,omitempty"`
	TargetSession    string    `json:"target_session,omitempty"`
	RunStatus        string    `json:"run_status,omitempty"`
	LastTickAt       time.Time `json:"last_tick_at,omitempty"`
	LastUsefulAt     time.Time `json:"last_useful_at,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	ObservationReset bool      `json:"observation_reset"`
	Desired          string    `json:"desired"`
	Runtime          string    `json:"runtime"`
	InstanceID       string    `json:"instance_id,omitempty"`
	LeaseID          string    `json:"lease_id,omitempty"`
	LastNudgeAt      time.Time `json:"last_nudge_at,omitempty"`
	RestartCount     int       `json:"restart_count"`
}

type WatcherSupervisorState struct {
	SchemaVersion  int       `json:"schema_version"`
	ProjectID      string    `json:"project_id"`
	Desired        string    `json:"desired"`
	Runtime        string    `json:"runtime"`
	InstanceID     string    `json:"instance_id,omitempty"`
	LeaseID        string    `json:"lease_id,omitempty"`
	WatcherAgentID string    `json:"watcher_agent_id,omitempty"`
	WatcherSession string    `json:"watcher_session,omitempty"`
	ActiveTaskID   string    `json:"active_task_id,omitempty"`
	ActiveRunID    string    `json:"active_run_id,omitempty"`
	TargetSession  string    `json:"target_session,omitempty"`
	LastTickAt     time.Time `json:"last_tick_at,omitempty"`
	LastUsefulAt   time.Time `json:"last_useful_at,omitempty"`
	LastNudgeAt    time.Time `json:"last_nudge_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	RestartCount   int       `json:"restart_count"`
	StartedAt      time.Time `json:"started_at,omitempty"`
}

func ValidateWatcherGuide(v WatcherGuide) error {
	if v.SchemaVersion != WatcherGuideSchemaVersion {
		return fmt.Errorf("unsupported watcher guide schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return fmt.Errorf("invalid watcher guide project_id: %w", err)
	}
	if v.Revision < 1 || uint64(v.Revision) > MaxSafeInteger {
		return fmt.Errorf("invalid watcher guide revision")
	}
	if strings.TrimSpace(v.Content) == "" || len([]byte(v.Content)) > WatcherMaxGuideBytes || strings.ContainsRune(v.Content, 0) {
		return fmt.Errorf("invalid watcher guide content")
	}
	if strings.TrimSpace(v.UpdatedBy) == "" || strings.ContainsAny(v.UpdatedBy, "\x00\r\n") {
		return fmt.Errorf("invalid watcher guide updated_by")
	}
	if v.UpdatedAt.IsZero() || v.UpdatedAt.Location() == nil {
		return fmt.Errorf("invalid watcher guide updated_at")
	}
	return nil
}

func ValidateWatcherObservationState(v WatcherObservationState) error {
	if v.SchemaVersion != WatcherObservationSchemaVersion {
		return fmt.Errorf("unsupported watcher observation schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return fmt.Errorf("invalid watcher observation project_id: %w", err)
	}
	if v.TaskID != "" && ValidateObjectIdentifier(v.TaskID) != nil {
		return fmt.Errorf("invalid watcher observation task_id")
	}
	if v.RunID != "" && ValidateObjectIdentifier(v.RunID) != nil {
		return fmt.Errorf("invalid watcher observation run_id")
	}
	if strings.ContainsAny(v.SessionKey, "\x00\r\n") || len(v.SessionKey) > 256 || len(v.Cursor) > 4096 {
		return fmt.Errorf("invalid watcher observation session_key")
	}
	if len(v.SeenDigests) > WatcherMaxSeenDigests {
		return fmt.Errorf("watcher observation seen_digests exceeds limit")
	}
	for _, digest := range v.SeenDigests {
		if len(digest) != WatcherMaxDigestBytes || strings.Trim(digest, "0123456789abcdef") != "" {
			return fmt.Errorf("invalid watcher observation digest")
		}
	}
	if len([]byte(v.LastTail)) > 64<<10 || len([]byte(v.LastError)) > 4096 {
		return fmt.Errorf("watcher observation text exceeds limit")
	}
	if v.LastTickAt.IsZero() {
		return fmt.Errorf("watcher observation last_tick_at is required")
	}
	return nil
}

func ValidateWatcherObservation(v WatcherObservation) error {
	if v.SchemaVersion != WatcherObservationSchemaVersion {
		return fmt.Errorf("unsupported watcher observation schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if v.TaskID != "" && ValidateObjectIdentifier(v.TaskID) != nil {
		return fmt.Errorf("invalid watcher observation task_id")
	}
	if v.RunID != "" && ValidateObjectIdentifier(v.RunID) != nil {
		return fmt.Errorf("invalid watcher observation run_id")
	}
	if v.Lines < 1 || v.Lines > WatcherMaxTailLines {
		return fmt.Errorf("invalid watcher observation lines")
	}
	if len([]byte(v.Tail)) > 64<<10 {
		return fmt.Errorf("watcher observation tail exceeds limit")
	}
	if v.SnapshotDigest != "" && (len(v.SnapshotDigest) != WatcherMaxDigestBytes || strings.Trim(v.SnapshotDigest, "0123456789abcdef") != "") {
		return fmt.Errorf("invalid watcher observation snapshot_digest")
	}
	if v.ObservedAt.IsZero() {
		return fmt.Errorf("watcher observation observed_at is required")
	}
	return nil
}

func ValidateWatcherStatus(v WatcherStatus) error {
	if v.SchemaVersion != WatcherStatusSchemaVersion {
		return fmt.Errorf("unsupported watcher status schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if v.Mode != "disabled" && v.Mode != "observe" && v.Mode != "require" {
		return fmt.Errorf("invalid watcher mode")
	}
	if v.WatcherAgentID != "" && ValidateObjectIdentifier(v.WatcherAgentID) != nil {
		return fmt.Errorf("invalid watcher agent_id")
	}
	if strings.ContainsAny(v.WatcherSession, "\x00\r\n") || len(v.WatcherSession) > 256 {
		return fmt.Errorf("invalid watcher session binding")
	}
	if v.CadenceSeconds < 1 || v.CadenceSeconds > 3600 || v.TailLines < 1 || v.TailLines > WatcherMaxTailLines {
		return fmt.Errorf("invalid watcher bounds")
	}
	if v.Desired != "stopped" && v.Desired != "running" {
		return fmt.Errorf("invalid watcher desired state")
	}
	if v.Runtime != "stopped" && v.Runtime != "starting" && v.Runtime != "running" && v.Runtime != "degraded" {
		return fmt.Errorf("invalid watcher runtime state")
	}
	if v.RestartCount < 0 {
		return fmt.Errorf("invalid watcher restart count")
	}
	return nil
}

func ValidateWatcherSupervisorState(v WatcherSupervisorState) error {
	if v.SchemaVersion != WatcherStatusSchemaVersion {
		return fmt.Errorf("unsupported watcher supervisor schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if v.Desired != "stopped" && v.Desired != "running" {
		return fmt.Errorf("invalid watcher supervisor desired state")
	}
	if v.WatcherAgentID != "" && ValidateObjectIdentifier(v.WatcherAgentID) != nil {
		return fmt.Errorf("invalid watcher supervisor agent_id")
	}
	if strings.ContainsAny(v.WatcherSession, "\x00\r\n") || len(v.WatcherSession) > 256 {
		return fmt.Errorf("invalid watcher supervisor session")
	}
	if v.Runtime != "stopped" && v.Runtime != "starting" && v.Runtime != "running" && v.Runtime != "degraded" {
		return fmt.Errorf("invalid watcher supervisor runtime state")
	}
	if v.RestartCount < 0 || len([]byte(v.LastError)) > 4096 {
		return fmt.Errorf("invalid watcher supervisor state")
	}
	return nil
}

func ValidateWatcherNudgeReceipt(v WatcherNudgeReceipt) error {
	if v.SchemaVersion != WatcherObservationSchemaVersion {
		return fmt.Errorf("unsupported watcher nudge schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if ValidateObjectIdentifier(v.TaskID) != nil || ValidateObjectIdentifier(v.RunID) != nil {
		return fmt.Errorf("invalid watcher nudge identity")
	}
	if v.StartedAt.IsZero() || v.FinishedAt.IsZero() {
		return fmt.Errorf("watcher nudge timestamps are required")
	}
	if len([]byte(v.Error)) > 4096 {
		return fmt.Errorf("watcher nudge error exceeds limit")
	}
	return nil
}
