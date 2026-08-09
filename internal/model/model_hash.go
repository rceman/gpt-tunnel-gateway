package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

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
