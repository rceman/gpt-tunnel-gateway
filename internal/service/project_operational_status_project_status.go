package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// ProjectOperationalStatus is the compact, session-bound operator projection.
// It deliberately contains identifiers and lifecycle facts, never full durable
// records, reports, histories, or Agent transcript output.
type ProjectOperationalStatus struct {
	Project               ProjectOperationalIdentity    `json:"project"`
	State                 string                        `json:"state"`
	TaskID                string                        `json:"task_id,omitempty"`
	TaskState             string                        `json:"task_state,omitempty"`
	Agent                 ProjectOperationalAgent       `json:"agent"`
	Operation             *ProjectOperationalOperation  `json:"operation,omitempty"`
	Integration           ProjectOperationalIntegration `json:"integration"`
	Rules                 ProjectOperationalRules       `json:"rules"`
	ReleaseCI             ProjectOperationalReleaseCI   `json:"release_ci"`
	SharedSync            sqlitestore.SharedSyncHealth  `json:"shared_sync"`
	Blocker               string                        `json:"blocker,omitempty"`
	RecommendedNextAction string                        `json:"recommended_next_action"`
}

type ProjectOperationalIdentity struct {
	ID   string `json:"project_id"`
	Code string `json:"project_code"`
}

type ProjectOperationalAgent struct {
	AgentID             string     `json:"agent_id,omitempty"`
	Expected            string     `json:"expected"`
	State               string     `json:"state"`
	SessionReady        bool       `json:"session_ready"`
	LastActivity        *time.Time `json:"last_activity,omitempty"`
	LastActivityAgeSecs int64      `json:"last_activity_age_seconds"`
}

type ProjectOperationalOperation struct {
	Kind        string `json:"kind"`
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
}

type ProjectOperationalIntegration struct {
	State            string `json:"state"`
	CandidateHead    string `json:"candidate_head,omitempty"`
	RuntimeSourceSHA string `json:"runtime_source_sha,omitempty"`
	Ready            bool   `json:"ready"`
	VersionMatch     bool   `json:"version_match"`
	ExactSourceMatch bool   `json:"exact_source_match"`
}

type ProjectOperationalRules struct {
	Digest       string `json:"-"`
	Acknowledged bool   `json:"acknowledged"`
	Fresh        bool   `json:"fresh"`
}

type ProjectOperationalReleaseCI struct {
	State  string `json:"state"`
	Tag    string `json:"tag,omitempty"`
	SHA    string `json:"sha,omitempty"`
	Status string `json:"status,omitempty"`
}

func (s *Service) ProjectOperationalStatus(ctx context.Context) (ProjectOperationalStatus, error) {
	sessionID := AgentSessionID(ctx)
	if sessionID == "" {
		return ProjectOperationalStatus{}, fmt.Errorf("project status requires a bound session")
	}
	session, err := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID)
	if err != nil || session.ProjectID == "" {
		return ProjectOperationalStatus{}, fmt.Errorf("project status session is invalid")
	}
	projectID := session.ProjectID
	var projectCode string
	var rulesDigest string
	var local config.ProjectConfig
	if s.Durability != nil {
		local, err = s.projectConfig(projectID)
		if err != nil {
			return ProjectOperationalStatus{}, err
		}
		if model.ValidateProjectCode(local.ProjectCode) != nil {
			return ProjectOperationalStatus{}, fmt.Errorf("project %q has no valid local project code", projectID)
		}
		projectCode = local.ProjectCode
		rulesDigest, err = s.ProjectRuleEffectiveDigest(ctx, projectID)
		if err != nil {
			return ProjectOperationalStatus{}, err
		}
	} else {
		project, projectErr := s.ProjectRead(ctx, projectID)
		if projectErr != nil {
			return ProjectOperationalStatus{}, projectErr
		}
		if project.ID != projectID || project.Status != "active" {
			return ProjectOperationalStatus{}, fmt.Errorf("project %q is not active", projectID)
		}
		identifiers, identifiersErr := s.ProjectIdentifiersRead(ctx, projectID)
		if identifiersErr != nil {
			return ProjectOperationalStatus{}, identifiersErr
		}
		projectCode = identifiers.ProjectCode
		rulesDigest, err = s.ProjectRuleEffectiveDigest(ctx, projectID)
		if err != nil {
			return ProjectOperationalStatus{}, err
		}
	}
	result := ProjectOperationalStatus{
		Project: ProjectOperationalIdentity{
			ID:   projectID,
			Code: projectCode,
		},
		State: "idle",
		Agent: ProjectOperationalAgent{
			Expected: "coding",
			State:    "unavailable",
		},
		Integration: ProjectOperationalIntegration{
			State: "unknown",
		},
		Rules: ProjectOperationalRules{
			Digest:       rulesDigest,
			Acknowledged: false,
			Fresh:        false,
		},
		ReleaseCI: ProjectOperationalReleaseCI{
			State: "unavailable",
		},
		SharedSync:            sqlitestore.SharedSyncHealth{State: "unavailable"},
		RecommendedNextAction: "await work",
	}
	if s.Durability != nil {
		if syncHealth, syncErr := s.Durability.SharedSyncHealth(ctx); syncErr == nil {
			result.SharedSync = syncHealth
		} else {
			result.SharedSync = sqlitestore.SharedSyncHealth{State: "degraded", LastError: "shared sync health unavailable"}
		}
		if pollFailure := s.sharedOutboxPollFailure(); pollFailure != "" {
			result.SharedSync.State = "degraded"
			result.SharedSync.LastError = pollFailure
		}
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" {
		if session, sessionErr := s.SessionInfo(ctx, sessionID); sessionErr == nil {
			result.Rules.Acknowledged = rulesDigest != "" && session.Session.ProjectRulesDigest == rulesDigest
			result.Rules.Fresh = result.Rules.Acknowledged
		}
	}
	result.Operation = s.projectOperationalOperation(projectID)
	if result.Operation != nil {
		result.State = "working"
		result.RecommendedNextAction = "supervise current operation"
	}
	var taskStates []model.TaskExecutionState
	var taskStatesErr error
	workerContract := false
	if s.Durability != nil {
		taskStates, taskStatesErr = s.Durability.ListTaskExecutionStates(ctx, projectID)
		workerContract = taskStatesErr == nil && len(taskStates) > 0
		if !workerContract {
			workerContract = s.projectHasExplicitAgentBinding(ctx, projectID)
		}
	}
	if workerContract && s.Durability != nil {
		worker, workerErr := s.ResolveProjectWorker(ctx, projectID)
		if workerErr != nil {
			result.Agent.State = "unavailable"
		} else {
			result.Agent.AgentID = worker.Agent.AgentID
			result.Agent.Expected = worker.Agent.AgentID
			result.Agent.SessionReady = worker.ControllerReachable
			result.Agent.State = "idle"
			if worker.RuntimeState == "busy" || worker.RuntimeState == "working" || worker.RuntimeState == "running" {
				result.Agent.State = "working"
			}
		}
	} else if s.Durability == nil {
		if agents, listErr := s.AgentList(ctx, projectID); listErr == nil {
			for _, agent := range agents {
				if agent.Role != model.AgentRoleCoding {
					continue
				}
				result.Agent.AgentID = agent.AgentID
				result.Agent.Expected = agent.AgentID
				if !agent.Enabled {
					result.Agent.State = "unavailable"
					break
				}
				binding, bound := s.agentBinding(projectID, agent.AgentID)
				if !bound || binding.Validate() != nil {
					result.Agent.State = "unavailable"
					break
				}
				probe, probeErr := s.Airelay.Status(ctx, binding.SessionKey)
				result.Agent.SessionReady = probeErr == nil && probe.ControllerReachable
				result.Agent.State = "idle"
				if probe.State == "busy" || probe.State == "working" {
					result.Agent.State = "working"
				}
				if probeErr != nil || !probe.ControllerReachable {
					result.Agent.State = "unavailable"
				}
				break
			}
		}
	}
	if workerContract && result.Agent.AgentID != "" {
		s.populateProjectOperationalTask(&result, taskStates, result.Agent.AgentID)
	}
	runtime := controller.Controller{Config: s.Config, ConfigPath: s.ConfigPath}.RuntimeIdentity(ctx)
	result.Integration = ProjectOperationalIntegration{
		State:            "ready",
		Ready:            runtime.GatewayReady && runtime.TunnelReady,
		RuntimeSourceSHA: runtime.SourceSHA,
		VersionMatch:     runtime.VersionMatch,
		ExactSourceMatch: runtime.ExactSourceMatch,
	}
	if (!runtime.GatewayReady || !runtime.TunnelReady) && result.Blocker == "" {
		result.Integration.State = "unavailable"
		result.State = "unavailable"
		result.Blocker = "runtime_unavailable"
		result.RecommendedNextAction = "inspect runtime blocker"
	} else if result.Blocker == "" && !result.Rules.Fresh {
		result.State = "blocked"
		result.Blocker = "project_rules_stale"
		result.RecommendedNextAction = "acknowledge current project rules"
	}
	if result.Agent.State == "unavailable" && result.State == "idle" {
		result.RecommendedNextAction = "start Agent"
	}
	return result, nil
}
