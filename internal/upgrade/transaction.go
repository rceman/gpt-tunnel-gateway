package upgrade

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

type UpgradeTransaction struct {
	TransactionID            string                    `json:"transaction_id"`
	SourceVersion            string                    `json:"source_version"`
	TargetVersion            string                    `json:"target_version"`
	SourceReleaseSHA         string                    `json:"source_release_sha"`
	TargetReleaseSHA         string                    `json:"target_release_sha,omitempty"`
	ArtifactChecksums        map[string]string         `json:"artifact_checksums,omitempty"`
	InstalledChecksumsBefore map[string]string         `json:"installed_checksums_before,omitempty"`
	InstalledChecksumsAfter  map[string]string         `json:"installed_checksums_after,omitempty"`
	OldHubSHA                string                    `json:"old_hub_sha,omitempty"`
	NewHubSHA                string                    `json:"new_hub_sha,omitempty"`
	MigrationOperations      []string                  `json:"migration_operations,omitempty"`
	MigrationPaths           []string                  `json:"migration_paths,omitempty"`
	ConfigSHABefore          string                    `json:"config_sha_before,omitempty"`
	ConfigSHAAfter           string                    `json:"config_sha_after,omitempty"`
	GatewayPIDBefore         int                       `json:"gateway_pid_before,omitempty"`
	GatewayPIDAfter          int                       `json:"gateway_pid_after,omitempty"`
	TunnelPIDBefore          int                       `json:"tunnel_pid_before,omitempty"`
	TunnelPIDAfter           int                       `json:"tunnel_pid_after,omitempty"`
	CurrentPhase             string                    `json:"current_phase"`
	StartedAt                time.Time                 `json:"started_at"`
	FinishedAt               *time.Time                `json:"finished_at,omitempty"`
	PrimaryError             string                    `json:"primary_error,omitempty"`
	RollbackAvailable        bool                      `json:"rollback_available"`
	FinalStatus              string                    `json:"final_status"`
	BackupPath               string                    `json:"backup_path,omitempty"`
	TargetStartup            *TargetStartupDiagnostics `json:"target_startup,omitempty"`
}

type TargetStartupDiagnostics struct {
	Phase                  string `json:"phase"`
	CaptureStatus          string `json:"capture_status"`
	TargetPID              int    `json:"target_pid,omitempty"`
	TargetProcessRunning   bool   `json:"target_process_running"`
	TargetProcessExited    bool   `json:"target_process_exited"`
	ProcessStateError      string `json:"process_state_error,omitempty"`
	AliveButUnready        bool   `json:"alive_but_unready"`
	ElapsedMilliseconds    int64  `json:"elapsed_ms"`
	ReadinessPassed        bool   `json:"readiness_passed"`
	Error                  string `json:"error,omitempty"`
	LogDelta               string `json:"log_delta,omitempty"`
	LogDeltaTruncated      bool   `json:"log_delta_truncated"`
	DiagnosticCaptureError string `json:"diagnostic_capture_error,omitempty"`
}

func transactionPath(c config.Config, id string) string {
	return filepath.Join(c.StateDir, "upgrade-transactions", id+".json")
}

func newTransaction(c config.Config, sourceVersion, targetVersion, sourceSHA string) (*UpgradeTransaction, error) {
	now := time.Now().UTC()
	id := fmt.Sprintf("upgrade-%s-%d", now.Format("20060102T150405Z"), now.UnixNano())
	tx := &UpgradeTransaction{TransactionID: id, SourceVersion: sourceVersion, TargetVersion: targetVersion, SourceReleaseSHA: sourceSHA, CurrentPhase: "inspect", StartedAt: now, FinalStatus: "pending", ArtifactChecksums: map[string]string{}, InstalledChecksumsBefore: map[string]string{}, InstalledChecksumsAfter: map[string]string{}, MigrationOperations: []string{}, MigrationPaths: []string{}}
	if err := writeTransaction(c, tx); err != nil {
		return nil, err
	}
	return tx, nil
}

func writeTransaction(c config.Config, tx *UpgradeTransaction) error {
	if err := fsutil.EnsureDir(filepath.Dir(transactionPath(c, tx.TransactionID)), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(transactionPath(c, tx.TransactionID), tx, 0o600)
}

func (tx *UpgradeTransaction) phase(c config.Config, phase string) error {
	tx.CurrentPhase = phase
	return writeTransaction(c, tx)
}

func (tx *UpgradeTransaction) fail(c config.Config, err error) error {
	tx.PrimaryError = sanitizeError(err)
	tx.FinalStatus = "failed"
	_ = writeTransaction(c, tx)
	return err
}

func (tx *UpgradeTransaction) complete(c config.Config, status string) error {
	now := time.Now().UTC()
	tx.CurrentPhase = "complete"
	tx.FinalStatus = status
	tx.FinishedAt = &now
	tx.RollbackAvailable = false
	return writeTransaction(c, tx)
}
