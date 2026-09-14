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
	MutationID     string          `json:"mutation_id,omitempty"`
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

func canonicalizeAdoptedOperationJSON(raw json.RawMessage, legacyID, operationID string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if !canonicalizeAdoptedOperationValue(value, legacyID, operationID) {
		return append(json.RawMessage(nil), raw...), nil
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func canonicalizeAdoptedOperationValue(value any, legacyID, operationID string) bool {
	changed := false
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if (key == "operation_id" || key == "gateway_operation_id") && child == legacyID {
				value[key] = operationID
				changed = true
				continue
			}
			if canonicalizeAdoptedOperationValue(child, legacyID, operationID) {
				changed = true
			}
		}
	case []any:
		for _, child := range value {
			if canonicalizeAdoptedOperationValue(child, legacyID, operationID) {
				changed = true
			}
		}
	}
	return changed
}
