package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// ResolveAgent is the single service-side authority for selecting a usable
// project agent. Agent records are portable; the returned session binding is
// host-local and is never written to project configuration.
func (s *Service) ResolveAgent(ctx context.Context, in AgentResolveInput) (ResolvedAgent, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return ResolvedAgent{}, err
	}
	if in.Role != model.AgentRoleCoding && in.Role != model.AgentRoleWatcher {
		return ResolvedAgent{}, fmt.Errorf("invalid agent role")
	}
	if in.AgentID != "" {
		if err := model.ValidateObjectIdentifier(in.AgentID); err != nil {
			return ResolvedAgent{}, err
		}
	}
	if in.RecommendedReasoning != "" && !validRoutingReasoning(in.RecommendedReasoning) {
		return ResolvedAgent{}, fmt.Errorf("invalid recommended reasoning")
	}
	configuration, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return ResolvedAgent{}, fmt.Errorf("read project configuration: %w", err)
	}
	recommended := in.RecommendedReasoning
	if recommended == "" {
		if in.Role == model.AgentRoleWatcher {
			recommended = model.ReasoningBestAvailable
		} else {
			recommended = configuration.AgentRouting.SingletonRecommendedReasoning
		}
	}
	agents, err := s.AgentList(ctx, in.ProjectID)
	if err != nil {
		return ResolvedAgent{}, err
	}
	type candidate struct {
		agent   model.Agent
		session string
		profile string
		score   int
	}
	candidates := make([]candidate, 0, len(agents))
	for _, agent := range agents {
		if agent.Role != in.Role || !agent.Enabled || (in.AgentID != "" && agent.AgentID != in.AgentID) {
			continue
		}
		binding, ok := s.Config.ResolveAgentBinding(in.ProjectID, agent.AgentID)
		if !ok || binding.Validate() != nil {
			continue
		}
		if in.RequireUsable {
			status, statusErr := s.Airelay.Status(ctx, binding.SessionKey)
			if statusErr != nil || !status.ControllerReachable || status.State != "idle" {
				continue
			}
		}
		candidates = append(candidates, candidate{agent: agent, session: binding.SessionKey, profile: binding.Profile, score: routingScore(agent.RecommendedReasoning)})
	}
	if len(candidates) == 0 {
		if in.AgentID != "" {
			return ResolvedAgent{}, fmt.Errorf("agent %q is not currently usable", in.AgentID)
		}
		return ResolvedAgent{}, fmt.Errorf("no usable %s agent for project %q", in.Role, in.ProjectID)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].agent.AgentID < candidates[j].agent.AgentID
	})
	selected := candidates[0]
	fallback := false
	fallbackReason := ""
	if in.AgentID == "" && recommended != model.ReasoningBestAvailable {
		exact := make([]candidate, 0, len(candidates))
		for _, item := range candidates {
			if item.agent.RecommendedReasoning == recommended {
				exact = append(exact, item)
			}
		}
		if len(exact) > 0 {
			selected = exact[0]
		} else {
			fallback = true
			fallbackReason = "preferred_reasoning_unavailable"
		}
	}
	return ResolvedAgent{
		ProjectID:          in.ProjectID,
		AgentID:            selected.agent.AgentID,
		Role:               selected.agent.Role,
		RequestedReasoning: recommended,
		ResolvedReasoning:  selected.agent.RecommendedReasoning,
		SessionKey:         selected.session,
		Profile:            selected.profile,
		Fallback:           fallback,
		FallbackReason:     fallbackReason,
	}, nil
}

func validRoutingReasoning(value string) bool {
	switch value {
	case model.ReasoningLow, model.ReasoningMedium, model.ReasoningHigh, model.ReasoningMax, model.ReasoningBestAvailable:
		return true
	default:
		return false
	}
}

func routingScore(value string) int {
	switch value {
	case model.ReasoningMax:
		return 4
	case model.ReasoningHigh:
		return 3
	case model.ReasoningMedium:
		return 2
	case model.ReasoningLow:
		return 1
	case model.ReasoningBestAvailable:
		return 0
	default:
		return -1
	}
}
