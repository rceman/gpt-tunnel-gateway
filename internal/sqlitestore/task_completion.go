package sqlitestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const TaskLifecycleEventKindComplete = "complete"

type TaskLifecycleEvent struct {
	ID          int64
	OperationID string
	ProjectID   string
	TaskID      string
	Revision    int64
	EventKind   string
	FromStatus  string
	ToStatus    string
	Actor       string
	Reason      string
	Contract    []byte
	RecordedAt  time.Time
}

const TaskLifecycleEventKindArchive = "archive"

func taskLifecycleEventFromShared(event SharedLifecycleEvent) TaskLifecycleEvent {
	kind := event.EventKind
	if kind == SharedLifecycleEventKindStatus {
		kind = TaskLifecycleEventKindComplete
	}
	return TaskLifecycleEvent{
		ID:          event.ID,
		OperationID: event.OperationID,
		ProjectID:   event.ProjectID,
		TaskID:      event.EntityID,
		Revision:    event.Revision,
		EventKind:   kind,
		FromStatus:  event.FromStatus,
		ToStatus:    event.ToStatus,
		Actor:       event.Actor,
		Reason:      event.Reason,
		Contract:    append([]byte(nil), event.Contract...),
		RecordedAt:  event.RecordedAt,
	}
}

func (d *Databases) ReadTaskCompletionEvent(ctx context.Context, projectID, taskID string) (TaskLifecycleEvent, bool, error) {
	if d == nil || d.Shared == nil {
		return TaskLifecycleEvent{}, false, fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return TaskLifecycleEvent{}, false, err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return TaskLifecycleEvent{}, false, err
	}
	events, err := d.ListSharedLifecycleEvents(ctx, "task", projectID, taskID, SharedLifecycleQueryMaxRows)
	if err != nil {
		return TaskLifecycleEvent{}, false, err
	}
	var completion *SharedLifecycleEvent
	for index := range events {
		if events[index].EventKind != SharedLifecycleEventKindStatus || events[index].ToStatus != model.TaskAuthoringDone {
			continue
		}
		if completion != nil {
			return TaskLifecycleEvent{}, false, fmt.Errorf("invalid task completion lifecycle history")
		}
		completion = &events[index]
	}
	if completion == nil {
		return TaskLifecycleEvent{}, false, nil
	}
	return taskLifecycleEventFromShared(*completion), true, nil
}

func (d *Databases) ListTaskLifecycleEvents(ctx context.Context, projectID, taskID string, limit int) ([]TaskLifecycleEvent, error) {
	if d == nil || d.Shared == nil {
		return nil, fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > SharedLifecycleQueryMaxRows {
		return nil, fmt.Errorf("task lifecycle event limit out of range")
	}
	events, err := d.ListSharedLifecycleEvents(ctx, "task", projectID, taskID, limit)
	if err != nil {
		return nil, err
	}
	converted := make([]TaskLifecycleEvent, 0, len(events))
	for _, event := range events {
		converted = append(converted, taskLifecycleEventFromShared(event))
	}
	return converted, nil
}

type CommitTaskCompletionRequest struct {
	OperationID         string
	ProjectID           string
	TaskID              string
	Revision            int64
	PreviousTaskPayload []byte
	TaskPayload         []byte
	FromStatus          string
	Actor               string
	Reason              string
	Contract            []byte
	ContractSHA256      string
	RecordedAt          time.Time
	PreviousExecution   *model.TaskExecutionState
	FinalExecution      *model.TaskExecutionState
}

func (d *Databases) CommitTaskCompletion(ctx context.Context, req CommitTaskCompletionRequest) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(req.ProjectID); err != nil {
		return err
	}
	if err := model.ValidateCanonicalTaskID(req.TaskID); err != nil {
		return err
	}
	if req.OperationID == "" || req.Revision < 1 || len(req.PreviousTaskPayload) == 0 || len(req.TaskPayload) == 0 || len(req.Contract) == 0 {
		return fmt.Errorf("invalid task completion identities")
	}
	contractSum := sha256.Sum256(req.Contract)
	if req.ContractSHA256 != hex.EncodeToString(contractSum[:]) {
		return fmt.Errorf("invalid task completion contract digest")
	}
	opSum := sha256.Sum256(append([]byte(req.ProjectID+"\x00"+req.TaskID+"\x00"), req.Contract...))
	if req.OperationID != "task-complete-"+hex.EncodeToString(opSum[:]) {
		return fmt.Errorf("invalid task completion operation identity")
	}
	if req.FromStatus != model.TaskAuthoringPlanned && req.FromStatus != model.TaskAuthoringReady {
		return fmt.Errorf("invalid task completion source status")
	}
	if strings.ContainsRune(req.Actor, 0) || utf8.RuneCountInString(req.Actor) < 1 || utf8.RuneCountInString(req.Actor) > 256 {
		return fmt.Errorf("invalid task completion actor")
	}
	if strings.ContainsRune(req.Reason, 0) || utf8.RuneCountInString(req.Reason) < 1 || utf8.RuneCountInString(req.Reason) > 1024 {
		return fmt.Errorf("invalid task completion reason")
	}
	if req.RecordedAt.IsZero() {
		return fmt.Errorf("invalid task completion time")
	}
	if (req.PreviousExecution == nil) != (req.FinalExecution == nil) {
		return fmt.Errorf("task completion execution state must be supplied in pairs")
	}
	var prevTask, finalTask model.TaskAuthoring
	if err := json.Unmarshal(req.PreviousTaskPayload, &prevTask); err != nil {
		return fmt.Errorf("invalid previous Task payload: %w", err)
	}
	if err := json.Unmarshal(req.TaskPayload, &finalTask); err != nil {
		return fmt.Errorf("invalid Task payload: %w", err)
	}
	if err := model.ValidateTaskAuthoring(prevTask); err != nil {
		return err
	}
	if err := model.ValidateTaskAuthoring(finalTask); err != nil {
		return err
	}
	if prevTask.ID != req.TaskID || prevTask.ProjectID != req.ProjectID || finalTask.ID != req.TaskID || finalTask.ProjectID != req.ProjectID ||
		int64(prevTask.Revision) != req.Revision || int64(finalTask.Revision) != req.Revision ||
		prevTask.Status != req.FromStatus || finalTask.Status != model.TaskAuthoringDone ||
		finalTask.ReadySeal != nil || finalTask.RevisionSHA256 != prevTask.RevisionSHA256 || !finalTask.UpdatedAt.Equal(req.RecordedAt.UTC()) {
		return fmt.Errorf("invalid task completion payload binding")
	}
	expectedTask := prevTask
	expectedTask.Status = model.TaskAuthoringDone
	expectedTask.ReadySeal = nil
	expectedTask.UpdatedAt = req.RecordedAt.UTC()
	if !reflect.DeepEqual(expectedTask, finalTask) {
		return fmt.Errorf("task completion payload drifts beyond the done transition")
	}
	if req.FinalExecution != nil {
		prev := req.PreviousExecution
		final := req.FinalExecution
		if err := model.ValidateTaskExecutionState(*prev); err != nil {
			return err
		}
		if err := model.ValidateTaskExecutionState(*final); err != nil {
			return err
		}
		if prev.Status != model.TaskExecutionIntegrated || final.Status != model.TaskExecutionDone || final.ExecutionRevision != prev.ExecutionRevision+1 ||
			prev.ProjectID != req.ProjectID || prev.TaskID != req.TaskID || final.ProjectID != prev.ProjectID || final.TaskID != prev.TaskID ||
			prev.TaskRevision != prevTask.Revision || prev.TaskRevisionSHA256 != prevTask.RevisionSHA256 ||
			!final.UpdatedAt.Equal(req.RecordedAt.UTC()) {
			return fmt.Errorf("task completion must only transition the exact previous integrated execution state to done")
		}
		expectedExecution := *prev
		expectedExecution.Status = model.TaskExecutionDone
		expectedExecution.ExecutionRevision = prev.ExecutionRevision + 1
		expectedExecution.UpdatedAt = req.RecordedAt.UTC()
		if !reflect.DeepEqual(expectedExecution, *final) {
			return fmt.Errorf("task completion execution state drifts beyond the done transition")
		}
	}
	_, err := d.CommitSharedLifecycleEvent(ctx, SharedLifecycleEventRequest{
		OperationID:           req.OperationID,
		EntityType:            "task",
		ProjectID:             req.ProjectID,
		EntityID:              req.TaskID,
		ExpectedRevision:      req.Revision,
		ExpectedStoreRevision: req.Revision,
		ExpectedPayload:       req.PreviousTaskPayload,
		Revision:              req.Revision,
		Kind:                  "task-complete",
		EventKind:             SharedLifecycleEventKindStatus,
		HistoryMutationKind:   "complete",
		FromStatus:            req.FromStatus,
		ToStatus:              model.TaskAuthoringDone,
		Payload:               req.TaskPayload,
		Actor:                 req.Actor,
		Reason:                req.Reason,
		ChangedFields:         taskLifecycleChangedFields(req.FromStatus),
		Contract:              req.Contract,
		CreatedAt:             req.RecordedAt,
	})
	if err != nil {
		return err
	}
	if req.FinalExecution != nil {
		return d.completeTaskExecutionState(ctx, *req.PreviousExecution, *req.FinalExecution)
	}
	return nil
}

func (d *Databases) completeTaskExecutionState(ctx context.Context, previous, final model.TaskExecutionState) error {
	current, found, err := d.ReadTaskExecutionState(ctx, previous.ProjectID, previous.TaskID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Task completion Local execution state is missing")
	}
	if sameTaskExecutionState(current, final) {
		return nil
	}
	if !sameTaskExecutionState(current, previous) {
		return fmt.Errorf("Task completion Local execution state changed concurrently")
	}
	result, err := d.Local.Exec(ctx, `UPDATE local_task_execution_states SET status=?,execution_revision=?,updated_at=? WHERE project_id=? AND task_id=? AND status=? AND stage=? AND worktree=? AND base_head_sha=? AND head_sha=? AND branch=? AND agent=? AND task_revision=? AND task_revision_sha256=? AND execution_revision=? AND updated_at=?`,
		final.Status, final.ExecutionRevision, final.UpdatedAt.UTC().Format(time.RFC3339Nano),
		previous.ProjectID, previous.TaskID, previous.Status, previous.Stage, previous.Worktree, previous.BaseHead, previous.Head, previous.Branch, previous.Agent, previous.TaskRevision, previous.TaskRevisionSHA256, previous.ExecutionRevision, previous.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("Task completion Local execution state changed concurrently")
	}
	return nil
}

func sameTaskExecutionState(a, b model.TaskExecutionState) bool {
	return a.TaskID == b.TaskID && a.ProjectID == b.ProjectID && a.Status == b.Status && a.Stage == b.Stage && a.Worktree == b.Worktree && a.BaseHead == b.BaseHead && a.Head == b.Head && a.Branch == b.Branch && a.TaskRevision == b.TaskRevision && a.TaskRevisionSHA256 == b.TaskRevisionSHA256 && a.Agent == b.Agent && a.ExecutionRevision == b.ExecutionRevision && a.UpdatedAt.Equal(b.UpdatedAt)
}

func taskLifecycleChangedFields(fromStatus string) []string {
	fields := []string{"status"}
	if fromStatus == model.TaskAuthoringReady {
		fields = append(fields, "ready_seal")
	}
	return fields
}

type CommitTaskArchiveRequest struct {
	OperationID         string
	ProjectID           string
	TaskID              string
	Revision            int64
	PreviousTaskPayload []byte
	TaskPayload         []byte
	FromStatus          string
	Actor               string
	Reason              string
	Contract            []byte
	RecordedAt          time.Time
}

func (d *Databases) CommitTaskArchive(ctx context.Context, req CommitTaskArchiveRequest) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(req.ProjectID); err != nil {
		return err
	}
	if err := model.ValidateCanonicalTaskID(req.TaskID); err != nil {
		return err
	}
	if req.OperationID == "" || req.Revision < 1 || len(req.PreviousTaskPayload) == 0 || len(req.TaskPayload) == 0 || len(req.Contract) == 0 {
		return fmt.Errorf("invalid task archive identities")
	}
	opSum := sha256.Sum256(append([]byte(req.ProjectID+"\x00"+req.TaskID+"\x00"), req.Contract...))
	if req.OperationID != "task-archive-"+hex.EncodeToString(opSum[:]) {
		return fmt.Errorf("invalid task archive operation identity")
	}
	if req.FromStatus != model.TaskAuthoringPlanned && req.FromStatus != model.TaskAuthoringReady && req.FromStatus != model.TaskAuthoringDone {
		return fmt.Errorf("invalid task archive source status")
	}
	if strings.ContainsRune(req.Actor, 0) || utf8.RuneCountInString(req.Actor) < 1 || utf8.RuneCountInString(req.Actor) > 256 {
		return fmt.Errorf("invalid task archive actor")
	}
	if !validSharedHistoryReason(req.Reason) || utf8.RuneCountInString(req.Reason) > 1024 {
		return fmt.Errorf("invalid task archive reason")
	}
	if req.RecordedAt.IsZero() {
		return fmt.Errorf("invalid task archive time")
	}
	var prevTask, finalTask model.TaskAuthoring
	if err := json.Unmarshal(req.PreviousTaskPayload, &prevTask); err != nil {
		return fmt.Errorf("invalid previous Task payload: %w", err)
	}
	if err := json.Unmarshal(req.TaskPayload, &finalTask); err != nil {
		return fmt.Errorf("invalid Task payload: %w", err)
	}
	if err := model.ValidateTaskAuthoring(prevTask); err != nil {
		return err
	}
	if err := model.ValidateTaskAuthoring(finalTask); err != nil {
		return err
	}
	if prevTask.ID != req.TaskID || prevTask.ProjectID != req.ProjectID || finalTask.ID != req.TaskID || finalTask.ProjectID != req.ProjectID ||
		int64(prevTask.Revision) != req.Revision || int64(finalTask.Revision) != req.Revision ||
		prevTask.Status != req.FromStatus || finalTask.Status != model.TaskAuthoringArchived ||
		finalTask.ReadySeal != nil || finalTask.RevisionSHA256 != prevTask.RevisionSHA256 || !finalTask.UpdatedAt.Equal(req.RecordedAt.UTC()) {
		return fmt.Errorf("invalid task archive payload binding")
	}
	expectedTask := prevTask
	expectedTask.Status = model.TaskAuthoringArchived
	expectedTask.ReadySeal = nil
	expectedTask.UpdatedAt = req.RecordedAt.UTC()
	if !reflect.DeepEqual(expectedTask, finalTask) {
		return fmt.Errorf("task archive payload drifts beyond the archived transition")
	}
	_, err := d.CommitSharedLifecycleEvent(ctx, SharedLifecycleEventRequest{
		OperationID:           req.OperationID,
		EntityType:            "task",
		ProjectID:             req.ProjectID,
		EntityID:              req.TaskID,
		ExpectedRevision:      req.Revision,
		ExpectedStoreRevision: req.Revision,
		ExpectedPayload:       req.PreviousTaskPayload,
		Revision:              req.Revision,
		Kind:                  "task-archive",
		EventKind:             SharedLifecycleEventKindArchive,
		HistoryMutationKind:   "archive",
		FromStatus:            req.FromStatus,
		ToStatus:              model.TaskAuthoringArchived,
		Payload:               req.TaskPayload,
		Actor:                 req.Actor,
		Reason:                req.Reason,
		ChangedFields:         taskLifecycleChangedFields(req.FromStatus),
		Contract:              req.Contract,
		CreatedAt:             req.RecordedAt,
	})
	return err
}
