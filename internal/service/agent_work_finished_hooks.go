package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const agentWorkFinishedPollInterval = 250 * time.Millisecond

func (s *Service) armAgentWorkFinished(ctx context.Context, projectID, agentID, sessionKey string) {
	if s.Durability == nil || AgentSessionID(ctx) == "" || projectID == "" || sessionKey == "" {
		return
	}
	epochID, err := newAgentWorkEpochID()
	if err != nil {
		return
	}
	_ = s.Durability.ArmCallbackEpoch(ctx, sqlitestore.CallbackEpoch{
		ID: epochID, ProjectID: projectID, AgentID: agentID,
		SessionID: AgentSessionID(ctx), SessionKey: sessionKey, ArmedAt: s.durableNow(),
	})
}

func newAgentWorkEpochID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return "agent-work-" + encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func (s *Service) startAgentWorkFinishedHookWorker() {
	s.agentWorkFinishedHookWorkerOnce.Do(func() {
		if s.Durability == nil {
			return
		}
		go s.agentWorkFinishedHookWorker()
	})
}

func (s *Service) agentWorkFinishedHookWorker() {
	ticker := time.NewTicker(agentWorkFinishedPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		epochs, err := s.Durability.PendingCallbackEpochs(context.Background(), 256)
		if err != nil {
			continue
		}
		for _, epoch := range epochs {
			ctx := WithAgentSessionID(context.Background(), epoch.SessionID)
			status, err := s.Airelay.Status(ctx, epoch.SessionKey)
			if err != nil {
				continue
			}
			ready, err := s.Durability.ObserveCallbackEpoch(context.Background(), epoch.ID, status.State)
			if err != nil || !ready {
				continue
			}
			s.dispatchAgentWorkFinishedHook(ctx, epoch)
		}
	}
}

func (s *Service) dispatchAgentWorkFinishedHook(ctx context.Context, epoch sqlitestore.CallbackEpoch) {
	configuration, err := s.ProjectConfigurationRead(ctx, epoch.ProjectID)
	if err != nil {
		return
	}
	procedureName, bound := configuration.Hooks[model.HookPostAgentWorkFinished]
	if !bound {
		_, _ = s.Durability.ClaimCallbackEpochWithoutHook(ctx, epoch.ID, s.durableNow())
		return
	}
	if epoch.SessionID == "" {
		return
	}
	definition, exists := configuration.Procedures[procedureName]
	if !exists {
		return
	}
	canonicalInput := map[string]any{"epoch": epoch.ID, "project": epoch.ProjectID}
	if epoch.AgentID != "" {
		canonicalInput["agent"] = epoch.AgentID
	}
	input, err := json.Marshal(projectProcedureHookInput(definition, canonicalInput))
	if err != nil {
		return
	}
	project, projectErr := s.EffectiveProjectConfig(epoch.ProjectID)
	executionRoot := ""
	if projectErr == nil {
		executionRoot = project.Root
	} else if configured, exists := s.Config.Projects[epoch.ProjectID]; exists {
		executionRoot = configured.Root
	}
	request := procedureExecutionRequest{
		Version:               procedureExecutionOperation,
		ProjectID:             epoch.ProjectID,
		Name:                  procedureName,
		ConfigurationRevision: configuration.Revision,
		Definition:            definition,
		Input:                 input,
		SessionID:             epoch.SessionID,
		Hook:                  model.HookPostAgentWorkFinished,
		ExecutionRoot:         executionRoot,
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return
	}
	inputDigest := sha256.Sum256(raw)
	mutationDigest := sha256.Sum256([]byte("post_agent_work_finished\x00" + epoch.ID))
	projectCode, err := s.localOperationProjectCode(ctx, epoch.ProjectID)
	if err != nil {
		return
	}
	local, created, err := s.Durability.AllocateCallbackEpochOperation(
		ctx, epoch.ID, epoch.ProjectID, projectCode, epoch.SessionID,
		hex.EncodeToString(mutationDigest[:]), hex.EncodeToString(inputDigest[:]),
		procedureExecutionKind, s.durableNow(),
	)
	if err != nil || !created {
		return
	}
	operation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   local.OperationID,
		MutationID:    local.MutationID,
		Kind:          procedureExecutionKind,
		RequestSHA256: hex.EncodeToString(inputDigest[:]),
		SessionID:     epoch.SessionID,
		ProjectID:     epoch.ProjectID,
		Input:         raw,
		Status:        "accepted",
		CreatedAt:     local.CreatedAt,
		UpdatedAt:     local.UpdatedAt,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		operation.Status = "outcome_unknown"
		operation.Error = "Agent-work Hook Operation state could not be durably persisted"
		operation.RecoveryReason = "claimed Agent work epoch was not launched because its Operation record could not be persisted"
		operation.UpdatedAt = s.durableNow()
		_ = s.writeDurableMutation(operation)
		_ = s.Durability.RecordCallbackEpochOperationOutcome(ctx, epoch.ID, operation.OperationID, "outcome_unknown", operation.UpdatedAt)
		return
	}
	s.enqueueDurableMutation(operation.OperationID)
}

func (s *Service) recoverNonReplayableProcedureOperation(operation durableMutationOperation) error {
	operation.Status = "outcome_unknown"
	operation.Error = "Procedure execution was not replayed after Gateway restart"
	operation.RecoveryReason = "Procedure execution may have crossed its external side-effect boundary before restart"
	operation.Result = nil
	operation.UpdatedAt = s.durableNow()
	if err := s.writeDurableMutation(operation); err != nil {
		return err
	}
	return s.recordAgentWorkFinishedOperationOutcome(operation)
}

func (s *Service) recordAgentWorkFinishedOperationOutcome(operation durableMutationOperation) error {
	if operation.Kind != procedureExecutionKind || s.Durability == nil {
		return nil
	}
	var request procedureExecutionRequest
	if err := json.Unmarshal(operation.Input, &request); err != nil || request.Hook != model.HookPostAgentWorkFinished || request.ParentOperationID != "" {
		return nil
	}
	return s.Durability.RecordCallbackEpochOperationOutcome(context.Background(), procedureEpochFromInput(request.Input), operation.OperationID, operation.Status, operation.UpdatedAt)
}

func procedureEpochFromInput(input json.RawMessage) string {
	var payload struct {
		Epoch string `json:"epoch"`
	}
	_ = json.Unmarshal(input, &payload)
	return payload.Epoch
}

func (s *Service) AgentWorkFinishedHookStatus(ctx context.Context, projectID, agentID string) (*AgentWorkFinishedHookStatus, error) {
	if s.Durability == nil {
		return nil, nil
	}
	state, found, err := s.Durability.LatestAgentWorkFinishedHook(ctx, projectID, agentID)
	if err != nil || !found || state.Outcome != "completed" && state.Outcome != "failed" && state.Outcome != "outcome_unknown" {
		return nil, err
	}
	if model.ValidateOperationID(state.OperationID) != nil {
		return nil, fmt.Errorf("invalid Agent-work Hook Operation reference")
	}
	return &AgentWorkFinishedHookStatus{
		Epoch:     state.EpochID,
		Operation: state.OperationID,
		Outcome:   state.Outcome,
	}, nil
}

type AgentWorkFinishedHookStatus struct {
	Epoch     string `json:"epoch"`
	Operation string `json:"operation"`
	Outcome   string `json:"outcome"`
}
