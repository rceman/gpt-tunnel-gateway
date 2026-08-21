package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	TrainID               string                        `json:"train_id,omitempty"`
	ItemPosition          int                           `json:"item_position,omitempty"`
	AttemptNumber         uint64                        `json:"attempt_number,omitempty"`
	TaskState             string                        `json:"task_state,omitempty"`
	TrainState            string                        `json:"train_state,omitempty"`
	ItemState             string                        `json:"item_state,omitempty"`
	AttemptState          string                        `json:"attempt_state,omitempty"`
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
	Revision     int    `json:"revision"`
	Digest       string `json:"digest"`
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
	session, err := durableSession.NewStore(s.Config.StateDir).Get(sessionID)
	if err != nil || session.ProjectID == "" {
		return ProjectOperationalStatus{}, fmt.Errorf("project status session is invalid")
	}
	projectID := session.ProjectID
	project, err := s.ProjectRead(ctx, projectID)
	if err != nil {
		return ProjectOperationalStatus{}, err
	}
	if project.ID != projectID || project.Status != "active" {
		return ProjectOperationalStatus{}, fmt.Errorf("project %q is not active", projectID)
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, projectID)
	if err != nil {
		return ProjectOperationalStatus{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
	if err != nil {
		return ProjectOperationalStatus{}, err
	}
	rulesDigest := projectOperationalDigest(policy)
	result := ProjectOperationalStatus{
		Project: ProjectOperationalIdentity{
			ID:   projectID,
			Code: identifiers.ProjectCode,
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
			Revision:     policy.Revision,
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
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" {
		if session, sessionErr := s.SessionInfo(ctx, sessionID); sessionErr == nil {
			result.Rules.Acknowledged = session.Session.ProjectRulesRevision == policy.Revision && session.Session.ProjectRulesDigest == rulesDigest
			result.Rules.Fresh = result.Rules.Acknowledged
		}
	}
	result.Operation = s.projectOperationalOperation(projectID)
	if result.Operation != nil {
		result.State = "working"
		result.RecommendedNextAction = "supervise current operation"
	}
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
			binding, bound := s.Config.ResolveAgentBinding(projectID, agent.AgentID)
			if !bound {
				binding, bound = s.Config.ResolveAutoAgentBinding(projectID)
			}
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
	trains, trainsErr := s.readTrainV2Records(ctx, projectID)
	if trainsErr == nil {
		s.populateProjectOperationalTrain(&result, trains)
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

func projectOperationalDigest(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Service) populateProjectOperationalTrain(result *ProjectOperationalStatus, trains []model.TrainV2) {
	sort.Slice(trains, func(i, j int) bool { return trains[i].UpdatedAt.After(trains[j].UpdatedAt) })
	for _, train := range trains {
		if train.Historical != nil {
			continue
		}
		classification, err := s.classifyTrainV2Lifecycle(train.ProjectID, train)
		if err != nil {
			result.TrainID = train.ID
			result.TrainState = train.Status
			result.State = "blocked"
			result.Blocker = "TRAIN_RECONCILIATION_UNAVAILABLE"
			result.RecommendedNextAction = "diagnose Hub/state availability, then retry project/status"
			return
		}
		if stale := staleTrainProjection(classification, train); stale != nil {
			result.TrainID = stale.TrainID
			result.TrainState = stale.Status
			result.State = "blocked"
			result.Blocker = stale.Blocker
			result.RecommendedNextAction = stale.RecommendedNextAction
			return
		}
		if correction := correctionTrainProjection(classification, train); correction != nil {
			result.TrainID = correction.TrainID
			result.TrainState = correction.Status
			result.State = "correction_pending"
			result.Blocker = correction.Blocker
			result.RecommendedNextAction = correction.RecommendedNextAction
			return
		}
		if train.Status == model.TrainV2Completed || train.Status == model.TrainV2ReadyForIntegration || train.Status == model.TrainV2Retired {
			if result.State == "idle" {
				result.RecommendedNextAction = "review or integrate current Train"
			}
			continue
		}
		result.TrainID, result.TrainState = train.ID, train.Status
		for _, item := range train.Items {
			if item.Status != model.TrainV2ItemRunning && item.Status != model.TrainV2ItemBlocked && item.Status != model.TrainV2ItemQueued {
				continue
			}
			result.TaskID, result.ItemPosition, result.ItemState = item.TaskID, item.Position, item.Status
			if item.ActiveAttemptNumber > 0 && item.ActiveAttemptNumber <= uint64(len(item.Attempts)) {
				result.AttemptNumber = item.ActiveAttemptNumber
				result.AttemptState = item.Attempts[item.ActiveAttemptNumber-1].Status
			}
			if item.Status == model.TrainV2ItemBlocked || train.Status == model.TrainV2Blocked {
				result.State, result.Blocker, result.RecommendedNextAction = "blocked", "train_blocked", "inspect blocker"
			} else if item.Status == model.TrainV2ItemRunning || train.Status == model.TrainV2Running {
				result.State, result.RecommendedNextAction = "working", "supervise current Attempt"
			}
			return
		}
	}
}

func (s *Service) projectOperationalOperation(projectID string) *ProjectOperationalOperation {
	dir := filepath.Join(s.Config.StateDir, "operations", "mutations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var candidates []durableMutationOperation
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operation, readErr := s.readDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
		if readErr != nil || operation.ProjectID != projectID || operation.Status == "completed" || operation.Status == "failed" || operation.Status == "outcome_unknown" {
			continue
		}
		candidates = append(candidates, operation)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt) })
	return &ProjectOperationalOperation{
		Kind:        candidates[0].Kind,
		OperationID: candidates[0].OperationID,
		Status:      candidates[0].Status,
	}
}
