package model

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	AgentSchemaVersion    = 1
	MaxAgentCapabilities  = 32
	MaxAgentCapabilityLen = 64
)

const (
	AgentRoleCoding = "coding"
)

const (
	ReasoningLow           = "low"
	ReasoningMedium        = "medium"
	ReasoningHigh          = "high"
	ReasoningMax           = "max"
	ReasoningBestAvailable = "best_available"
)

// Agent is the portable project-owned identity. Provider, model, session and
// profile details are deliberately host-local Config data, not Hub metadata.
type Agent struct {
	SchemaVersion        int       `json:"schema_version"`
	ProjectID            string    `json:"project_id"`
	AgentID              string    `json:"agent_id"`
	Role                 string    `json:"role"`
	Enabled              bool      `json:"enabled"`
	RecommendedReasoning string    `json:"recommended_reasoning"`
	Capabilities         []string  `json:"capabilities"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// AgentAvailabilityStatus is a bounded projection and never includes local
// session keys, provider names, model names, or profile paths.
type AgentAvailabilityStatus struct {
	SchemaVersion  int    `json:"schema_version"`
	ProjectID      string `json:"project_id"`
	AgentID        string `json:"agent_id"`
	Role           string `json:"role"`
	Registered     bool   `json:"registered"`
	Enabled        bool   `json:"enabled"`
	Bound          bool   `json:"bound"`
	Usable         bool   `json:"usable"`
	State          string `json:"state"`
	Reason         string `json:"reason"`
	SessionState   string `json:"session_state,omitempty"`
	AttemptState   string `json:"attempt_state,omitempty"`
	TrainID        string `json:"train_id,omitempty"`
	ItemPosition   int    `json:"item_position,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	AttemptNumber  uint64 `json:"attempt_number,omitempty"`
	Recoverable    bool   `json:"recoverable,omitempty"`
	RecoveryReason string `json:"recovery_reason,omitempty"`
}

func ValidateAgent(v Agent) error {
	if v.SchemaVersion != AgentSchemaVersion {
		return fmt.Errorf("unsupported agent schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return fmt.Errorf("invalid agent project_id: %w", err)
	}
	if err := ValidateObjectIdentifier(v.AgentID); err != nil {
		return fmt.Errorf("invalid agent agent_id: %w", err)
	}
	if v.Role != AgentRoleCoding {
		return fmt.Errorf("invalid agent role")
	}
	switch v.RecommendedReasoning {
	case ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningMax, ReasoningBestAvailable:
	default:
		return fmt.Errorf("invalid agent recommended_reasoning")
	}
	if len(v.Capabilities) > MaxAgentCapabilities {
		return fmt.Errorf("agent capabilities exceed limit")
	}
	seen := map[string]bool{}
	for _, capability := range v.Capabilities {
		if !validAgentCapability(capability) {
			return fmt.Errorf("invalid agent capability %q", capability)
		}
		if seen[capability] {
			return fmt.Errorf("duplicate agent capability %q", capability)
		}
		seen[capability] = true
	}
	if v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
		return fmt.Errorf("invalid agent timestamps")
	}
	return nil
}

func NormalizeAgentCapabilities(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func validAgentCapability(value string) bool {
	if value == "" || len(value) > MaxAgentCapabilityLen || strings.TrimSpace(value) != value {
		return false
	}
	for i, r := range value {
		if i == 0 && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func ValidateAgentAvailabilityStatus(v AgentAvailabilityStatus) error {
	if v.SchemaVersion != AgentSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || ValidateObjectIdentifier(v.AgentID) != nil {
		return fmt.Errorf("invalid agent availability identity")
	}
	if v.Role != AgentRoleCoding {
		return fmt.Errorf("invalid agent availability role")
	}
	if v.State != "registered" && v.State != "disabled" && v.State != "unbound" && v.State != "unavailable" && v.State != "usable" {
		return fmt.Errorf("invalid agent availability state")
	}
	if v.Usable && (!v.Registered || !v.Enabled || !v.Bound || v.State != "usable") {
		return fmt.Errorf("usable agent availability is inconsistent")
	}
	return nil
}
