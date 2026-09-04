package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const hotfixExecutionReceiptMaxBytes = 1 << 20

type hotfixExecutionReceipt struct {
	ProjectID    string    `json:"project_id"`
	HotfixRef    string    `json:"hotfix_ref"`
	TaskID       string    `json:"task_id"`
	TaskRevision int       `json:"task_revision"`
	Head         string    `json:"head"`
	AgentID      string    `json:"agent_id"`
	SessionKey   string    `json:"session_key"`
	Profile      string    `json:"profile,omitempty"`
	WorktreePath string    `json:"worktree_path"`
	Message      string    `json:"message"`
	Delivered    bool      `json:"delivered"`
	CreatedAt    time.Time `json:"created_at"`
}

func hotfixExecutionReceiptPath(stateDir, projectID, taskID, generation string) string {
	return filepath.Join(stateDir, "hotfix-execution", projectID, taskID, generation+".json")
}

func hotfixExecutionGeneration(projectID, ref, taskID string, revision int, head, agentID, sessionKey, profile, worktree, message string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{projectID, ref, taskID, fmt.Sprint(revision), head, agentID, sessionKey, profile, worktree, message}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func readHotfixExecutionReceipt(path string) (hotfixExecutionReceipt, error) {
	var receipt hotfixExecutionReceipt
	if err := fsutil.ReadJSONBounded(path, hotfixExecutionReceiptMaxBytes, &receipt); err != nil {
		return hotfixExecutionReceipt{}, err
	}
	if !receipt.Delivered || model.ValidateProjectIdentifier(receipt.ProjectID) != nil || model.ValidateCanonicalTaskID(receipt.TaskID) != nil || model.ValidateCommitSHA(receipt.Head) != nil || receipt.HotfixRef == "" || receipt.SessionKey == "" || receipt.WorktreePath == "" || receipt.Message == "" || receipt.CreatedAt.IsZero() {
		return hotfixExecutionReceipt{}, fmt.Errorf("invalid hotfix execution receipt")
	}
	return receipt, nil
}

func writeHotfixExecutionReceipt(path string, receipt hotfixExecutionReceipt) error {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if len(payload) > hotfixExecutionReceiptMaxBytes {
		return fmt.Errorf("hotfix execution receipt exceeds bound")
	}
	return fsutil.WriteJSONAtomic(path, receipt, 0o600)
}
