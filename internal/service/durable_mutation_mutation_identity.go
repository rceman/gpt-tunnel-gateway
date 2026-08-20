package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"time"
)

const durableMutationSchemaVersion = 1

type durableMutationOperation struct {
	SchemaVersion  int             `json:"schema_version"`
	OperationID    string          `json:"operation_id"`
	Kind           string          `json:"kind"`
	RequestSHA256  string          `json:"request_sha256"`
	SessionID      string          `json:"session_id,omitempty"`
	ProjectID      string          `json:"project_id"`
	Input          json.RawMessage `json:"input"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error,omitempty"`
	RecoveryReason string          `json:"recovery_reason,omitempty"`
	CapturedState  string          `json:"captured_state,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func durableMutationPath(stateDir, operationID string) string {
	return filepath.Join(stateDir, "operations", "mutations", operationID+".json")
}
func durableMutationDigest(kind, sessionID string, input []byte) string {
	return durableMutationDigestWithIdentity(kind, sessionID, input, nil)
}
func durableMutationDigestWithIdentity(kind, sessionID string, input, identity []byte) string {
	hash := sha256.New()
	hash.Write([]byte(kind))
	hash.Write([]byte{0})
	hash.Write([]byte(sessionID))
	hash.Write([]byte{0})
	hash.Write(input)
	if identity != nil {
		hash.Write([]byte{0})
		hash.Write(identity)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
