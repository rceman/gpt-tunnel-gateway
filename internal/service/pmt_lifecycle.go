package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type PMTReadResult struct {
	ID          string `json:"pmt_id"`
	State       string `json:"state"`
	Title       string `json:"title,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	ReadCount   int    `json:"read_count"`
	Tombstone   bool   `json:"tombstone"`
}

type PMTQueueResult struct {
	ProjectID string         `json:"project_id"`
	Queue     model.PMTQueue `json:"queue"`
}

type PMTCancelResult struct {
	ProjectID string         `json:"project_id"`
	PMTID     string         `json:"pmt_id"`
	Cancelled bool           `json:"cancelled"`
	Queue     model.PMTQueue `json:"queue"`
}

type PMTSupersedeInput struct {
	ProjectID string
	OldIDs    []string
	Title     string
	Message   string
}

func (s *Service) createAndSendPMT(ctx context.Context, projectID, title, instruction string, oldIDs []string) (AgentPromptResult, error) {
	if s.Durability == nil {
		return AgentPromptResult{}, fmt.Errorf("local PMT store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return AgentPromptResult{}, err
	}
	project, ok := s.Config.Projects[projectID]
	if !ok || model.ValidateProjectCode(project.ProjectCode) != nil {
		return AgentPromptResult{}, fmt.Errorf("project %q has no valid local project code", projectID)
	}
	if title == "" {
		title = pmtTitle(instruction)
	}
	if err := validatePMTInput(title, instruction); err != nil {
		return AgentPromptResult{}, err
	}
	agentID, target, targetSessionID, err := s.resolvePMTTarget(ctx, projectID)
	if err != nil {
		return AgentPromptResult{}, err
	}
	planner := AgentSessionID(ctx)
	if planner == "" {
		return AgentPromptResult{}, fmt.Errorf("Planner session is required for PMT creation")
	}
	now := time.Now().UTC()
	pmt := model.PMT{
		SchemaVersion: model.PMTSchemaVersion, ProjectID: projectID, ProjectCode: project.ProjectCode,
		Title: title, Instruction: instruction, PlannerSessionID: planner,
		TargetSessionID: targetSessionID, TargetAirelaySessionKey: target, TargetAgentID: agentID, CreatedAt: now,
		State: model.PMTStateUnread, Reference: "pending",
	}
	enabled, activeErr := s.trainV2Enabled(ctx, projectID)
	if activeErr != nil {
		return AgentPromptResult{}, fmt.Errorf("resolve Train execution authority for PMT: %w", activeErr)
	}
	if enabled {
		active, found, err := s.trainV2ActiveAttempt(ctx, projectID)
		if err != nil {
			return AgentPromptResult{}, fmt.Errorf("resolve current Train Attempt for PMT: %w", err)
		}
		if found {
			pmt.TrainID = active.Train.ID
			pmt.ItemPosition = active.Start.CurrentItemPosition
			pmt.TaskID = active.Item.TaskID
			pmt.AttemptNumber = active.Attempt.Number
			pmt.TargetAgentID = active.Attempt.AgentID
			pmt.TargetAirelaySessionKey = active.Attempt.AirelaySessionKey
		}
	}
	var created model.PMT
	if len(oldIDs) == 0 {
		created, err = s.Durability.CreatePMT(ctx, pmt)
	} else {
		created, err = s.Durability.SupersedeAndCreatePMT(ctx, oldIDs, pmt)
	}
	if err != nil {
		return AgentPromptResult{}, err
	}
	plannerReference := fmt.Sprintf("read prompt: gpt-tunnel prompt %s", created.ID)
	lock, err := s.acquireSessionSendLock(target)
	if err != nil {
		return AgentPromptResult{
			ProjectID: projectID,
			PMTID:     created.ID,
			Queued:    true,
			Delivered: false,
		}, fmt.Errorf("agent session send is already in progress")
	}
	defer func() { _ = lock.Release() }()
	if _, err := s.Airelay.PromptWithProvenance(ctx, target, planner, plannerReference); err != nil {
		return AgentPromptResult{
			ProjectID: projectID,
			PMTID:     created.ID,
			Queued:    true,
			Delivered: false,
		}, err
	}
	if err := s.Durability.MarkPMTReferenceSubmitted(ctx, created.ID, "["+planner+"] "+plannerReference, time.Now().UTC()); err != nil {
		return AgentPromptResult{}, err
	}
	queue, queueErr := s.PMTQueue(ctx, projectID, model.MaxPMTQueueEntries)
	if queueErr != nil {
		return AgentPromptResult{}, queueErr
	}
	return AgentPromptResult{
		ProjectID: projectID,
		PMTID:     created.ID,
		Queued:    true,
		Delivered: false,
		Queue:     &queue.Queue,
	}, nil
}

func (s *Service) resolvePMTTarget(ctx context.Context, projectID string) (string, string, string, error) {
	local, localErr := s.projectConfig(projectID)
	if localErr != nil || strings.TrimSpace(local.AirelaySessionKey) == "" {
		return "", "", "", fmt.Errorf("project %q has no local Agent session binding", projectID)
	}
	records, err := session.NewStore(s.Config.StateDir).List()
	if err != nil {
		return "", "", "", fmt.Errorf("read coding Agent sessions: %w", err)
	}
	var targetSession string
	for _, record := range records {
		if record.Status != session.StatusActive || record.Role != session.RoleAgent || record.ProjectID != projectID || record.SessionRef == nil || *record.SessionRef != local.AirelaySessionKey {
			continue
		}
		if targetSession != "" {
			return "", "", "", fmt.Errorf("project %q has ambiguous coding Agent sessions", projectID)
		}
		targetSession = record.ID
	}
	if targetSession == "" {
		return "", "", "", fmt.Errorf("project %q has no active coding Agent session", projectID)
	}
	return "coding", local.AirelaySessionKey, targetSession, nil
}

func (s *Service) PMTRead(ctx context.Context, id string) (PMTReadResult, error) {
	if s.Durability == nil {
		return PMTReadResult{}, fmt.Errorf("local PMT store is unavailable")
	}
	if err := model.ValidateObjectIdentifier(id); err != nil {
		return PMTReadResult{}, fmt.Errorf("invalid PMT identifier: %w", err)
	}
	pmt, err := s.Durability.ReadPMT(ctx, id)
	if err != nil {
		return PMTReadResult{}, err
	}
	if err := s.authorizePMTTarget(ctx, pmt); err != nil {
		return PMTReadResult{}, err
	}
	if pmt.State == model.PMTStateUnread && pmt.TrainID != "" {
		if err := s.validatePMTExecution(ctx, pmt); err != nil {
			return PMTReadResult{}, err
		}
	}
	if pmt.State != model.PMTStateUnread && pmt.State != model.PMTStateFetched {
		return PMTReadResult{
			ID:        pmt.ID,
			State:     pmt.State,
			ReadCount: pmt.ReadCount,
			Tombstone: true,
		}, nil
	}
	updated, _, err := s.Durability.MarkPMTFetched(ctx, id, time.Now().UTC())
	if err != nil {
		return PMTReadResult{}, err
	}
	if updated.State != model.PMTStateUnread && updated.State != model.PMTStateFetched {
		return PMTReadResult{
			ID:        updated.ID,
			State:     updated.State,
			ReadCount: updated.ReadCount,
			Tombstone: true,
		}, nil
	}
	return PMTReadResult{
		ID:          updated.ID,
		State:       updated.State,
		Title:       updated.Title,
		Instruction: updated.Instruction,
		ReadCount:   updated.ReadCount,
	}, nil
}

func (s *Service) PMTQueue(ctx context.Context, projectID string, limit int) (PMTQueueResult, error) {
	if s.Durability == nil {
		return PMTQueueResult{}, fmt.Errorf("local PMT store is unavailable")
	}
	_, _, targetSessionID, err := s.resolvePMTTarget(ctx, projectID)
	if err != nil {
		return PMTQueueResult{}, err
	}
	queue, count, err := s.Durability.ListPendingPMTs(ctx, projectID, targetSessionID, limit)
	if err != nil {
		return PMTQueueResult{}, err
	}
	return PMTQueueResult{
		ProjectID: projectID,
		Queue:     model.PMTQueue{QueueCount: count, Entries: queue},
	}, nil
}

func (s *Service) PMTCancel(ctx context.Context, projectID, id string) (PMTCancelResult, error) {
	if s.Durability == nil {
		return PMTCancelResult{}, fmt.Errorf("local PMT store is unavailable")
	}
	if err := model.ValidateObjectIdentifier(id); err != nil {
		return PMTCancelResult{}, fmt.Errorf("invalid PMT identifier: %w", err)
	}
	pmt, err := s.Durability.ReadPMT(ctx, id)
	if err != nil {
		return PMTCancelResult{}, err
	}
	if pmt.ProjectID != projectID || AgentSessionID(ctx) == "" || pmt.PlannerSessionID != AgentSessionID(ctx) {
		return PMTCancelResult{}, fmt.Errorf("PMT authority mismatch")
	}
	if pmt.State != model.PMTStateUnread {
		return PMTCancelResult{}, fmt.Errorf("PMT %s cannot be cancelled from state %q", id, pmt.State)
	}
	cancelled, err := s.Durability.CancelPMT(ctx, id, time.Now().UTC())
	if err != nil {
		return PMTCancelResult{}, err
	}
	if !cancelled {
		return PMTCancelResult{}, fmt.Errorf("PMT %s changed before cancellation", id)
	}
	queue, err := s.PMTQueue(ctx, projectID, model.MaxPMTQueueEntries)
	if err != nil {
		return PMTCancelResult{}, err
	}
	return PMTCancelResult{
		ProjectID: projectID,
		PMTID:     id,
		Cancelled: cancelled,
		Queue:     queue.Queue,
	}, nil
}

func (s *Service) PMTSupersede(ctx context.Context, in PMTSupersedeInput) (AgentPromptResult, error) {
	if s.Durability == nil {
		return AgentPromptResult{}, fmt.Errorf("local PMT store is unavailable")
	}
	if len(in.OldIDs) == 0 {
		return AgentPromptResult{}, fmt.Errorf("at least one PMT is required for supersession")
	}
	for _, id := range in.OldIDs {
		if model.ValidateObjectIdentifier(id) != nil {
			return AgentPromptResult{}, fmt.Errorf("invalid PMT identifier")
		}
		pmt, err := s.Durability.ReadPMT(ctx, id)
		if err != nil || pmt.ProjectID != in.ProjectID || AgentSessionID(ctx) == "" || pmt.PlannerSessionID != AgentSessionID(ctx) {
			return AgentPromptResult{}, fmt.Errorf("PMT supersession authority mismatch")
		}
	}
	return s.createAndSendPMT(ctx, in.ProjectID, in.Title, in.Message, in.OldIDs)
}

func (s *Service) authorizePMTTarget(ctx context.Context, pmt model.PMT) error {
	sessionID := AgentSessionID(ctx)
	if sessionID == "" || pmt.TargetSessionID == "" || sessionID != pmt.TargetSessionID {
		return fmt.Errorf("PMT session authority mismatch")
	}
	info, err := s.SessionInfo(ctx, sessionID)
	if err != nil || info.Session.ProjectID != pmt.ProjectID || info.Session.Role != session.RoleAgent {
		return fmt.Errorf("PMT session authority mismatch")
	}
	target, err := s.resolveAgentTailSession(pmt.ProjectID)
	if err != nil || target != pmt.TargetAirelaySessionKey {
		return fmt.Errorf("PMT Agent target is stale")
	}
	return nil
}

func (s *Service) validatePMTExecution(ctx context.Context, pmt model.PMT) error {
	active, found, err := s.trainV2ActiveAttempt(ctx, pmt.ProjectID)
	if err != nil || !found {
		return fmt.Errorf("PMT execution identity is stale")
	}
	if active.Train.ID != pmt.TrainID || active.Start.CurrentItemPosition != pmt.ItemPosition || active.Item.TaskID != pmt.TaskID || active.Attempt.Number != pmt.AttemptNumber || active.Attempt.AgentID != pmt.TargetAgentID || active.Attempt.AirelaySessionKey != pmt.TargetAirelaySessionKey {
		return fmt.Errorf("PMT execution identity is stale")
	}
	return nil
}

func pmtTitle(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		value = value[:index]
	}
	if len(value) > model.MaxPMTTitleBytes {
		var bounded strings.Builder
		for _, r := range value {
			width := utf8.RuneLen(r)
			if bounded.Len()+width > model.MaxPMTTitleBytes {
				break
			}
			bounded.WriteRune(r)
		}
		value = bounded.String()
	}
	return strings.TrimSpace(value)
}

func validatePMTInput(title, instruction string) error {
	probe := model.PMT{Title: title, Instruction: instruction}
	if err := validatePMTTextForService(probe); err != nil {
		return err
	}
	return nil
}

func validatePMTTextForService(p model.PMT) error {
	if !utf8.ValidString(p.Title) || strings.TrimSpace(p.Title) == "" || len([]byte(p.Title)) > model.MaxPMTTitleBytes || strings.ContainsRune(p.Title, '\x00') {
		return fmt.Errorf("PMT title is empty or exceeds bounded size")
	}
	if !utf8.ValidString(p.Instruction) || strings.TrimSpace(p.Instruction) == "" || len([]byte(p.Instruction)) > model.MaxPMTInstructionBytes || strings.ContainsRune(p.Instruction, '\x00') {
		return fmt.Errorf("PMT instruction is empty or exceeds bounded size")
	}
	return nil
}
