package service

type DispatchInput struct {
	TaskID               string `json:"task_id"`
	TrainID              string `json:"train_id,omitempty"`
	LaneBranch           string `json:"lane_branch,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	RecommendedReasoning string `json:"recommended_reasoning,omitempty"`
	ResolvedReasoning    string `json:"resolved_reasoning,omitempty"`
	AgentFallback        bool   `json:"agent_fallback,omitempty"`
	AgentFallbackReason  string `json:"agent_fallback_reason,omitempty"`
	WriteOptions
}

type AgentResolveInput struct {
	ProjectID            string
	Role                 string
	AgentID              string
	RecommendedReasoning string
	RequireUsable        bool
}

type ResolvedAgent struct {
	ProjectID          string
	AgentID            string
	Role               string
	RequestedReasoning string
	ResolvedReasoning  string
	SessionKey         string
	Profile            string
	Fallback           bool
	FallbackReason     string
}
