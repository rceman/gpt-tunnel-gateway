package service

import (
	"encoding/json"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// ProjectProgress is the bounded, Run-free progress projection used by
// project_status and agent_status. Train-v2 execution details are exposed by
// the TrainV2 projection; this type contains only the session snapshot.
type ProjectProgress struct {
	AgentState                       string     `json:"agent_state"`
	ControllerReachable              bool       `json:"controller_reachable"`
	AirelayVersion                   string     `json:"airelay_version,omitempty"`
	ProtocolVersion                  string     `json:"protocol_version,omitempty"`
	CapacityWarnings                 []string   `json:"capacity_warnings"`
	ExitCode                         int        `json:"exit_code"`
	Error                            string     `json:"error,omitempty"`
	LastMeaningfulActivity           *time.Time `json:"last_meaningful_activity,omitempty"`
	LastMeaningfulActivityAgeSeconds int64      `json:"last_meaningful_activity_age_seconds"`
	Tail                             string     `json:"tail"`
	BlockerClassification            string     `json:"blocker_classification"`
	RecommendedNextAction            string     `json:"recommended_next_action"`
	ComponentErrors                  []string   `json:"component_errors"`
}

func (p ProjectProgress) MarshalJSON() ([]byte, error) {
	type alias ProjectProgress
	if p.CapacityWarnings == nil {
		p.CapacityWarnings = []string{}
	}
	if p.AgentState == "" {
		p.AgentState = model.AgentStateUnknown
	}
	if p.ComponentErrors == nil {
		p.ComponentErrors = []string{}
	}
	return json.Marshal(alias(p))
}
