package service

type AgentResolveInput struct {
	ProjectID            string
	Role                 string
	AgentID              string
	RecommendedReasoning string
	RequireUsable        bool
	RequireUnique        bool
	RequireAttached      bool
}

type ResolvedAgent struct {
	ProjectID          string
	AgentID            string
	Role               string
	RequestedReasoning string
	ResolvedReasoning  string
	SessionKey         string
	Fallback           bool
	FallbackReason     string
}
