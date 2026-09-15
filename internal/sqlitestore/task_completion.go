package sqlitestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	upstream "github.com/rceman/go-sqlite-store/store"
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

const taskLifecycleEventColumns = `SELECT id,operation_id,project_id,task_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at FROM shared_task_lifecycle_events`

func decodeTaskLifecycleEvent(row []any) (TaskLifecycleEvent, error) {
	var event TaskLifecycleEvent
	if len(row) != 12 {
		return event, fmt.Errorf("invalid task lifecycle event row")
	}
	text := func(i int) (string, error) {
		v, ok := row[i].(string)
		if !ok {
			return "", fmt.Errorf("invalid task lifecycle event column %d", i)
		}
		return v, nil
	}
	id, ok := row[0].(int64)
	if !ok {
		return event, fmt.Errorf("invalid task lifecycle event id")
	}
	var err error
	if event.OperationID, err = text(1); err != nil {
		return event, err
	}
	if event.ProjectID, err = text(2); err != nil {
		return event, err
	}
	if event.TaskID, err = text(3); err != nil {
		return event, err
	}
	event.ID = id
	revision, ok := row[4].(int64)
	if !ok {
		return event, fmt.Errorf("invalid task lifecycle event revision")
	}
	event.Revision = revision
	if event.EventKind, err = text(5); err != nil {
		return event, err
	}
	if event.FromStatus, err = text(6); err != nil {
		return event, err
	}
	if event.ToStatus, err = text(7); err != nil {
		return event, err
	}
	if event.Actor, err = text(8); err != nil {
		return event, err
	}
	if event.Reason, err = text(9); err != nil {
		return event, err
	}
	contract, ok := row[10].([]byte)
	if !ok {
		return event, fmt.Errorf("invalid task lifecycle event contract")
	}
	event.Contract = contract
	recorded, err := text(11)
	if err != nil {
		return event, err
	}
	event.RecordedAt, err = time.Parse(time.RFC3339Nano, recorded)
	if err != nil {
		return event, fmt.Errorf("invalid task lifecycle event recorded_at")
	}
	event.Contract = append([]byte(nil), contract...)
	complete := event.EventKind == TaskLifecycleEventKindComplete && event.ToStatus == model.TaskAuthoringDone &&
		(event.FromStatus == model.TaskAuthoringPlanned || event.FromStatus == model.TaskAuthoringReady)
	archive := event.EventKind == TaskLifecycleEventKindArchive && event.ToStatus == model.TaskAuthoringArchived &&
		(event.FromStatus == model.TaskAuthoringPlanned || event.FromStatus == model.TaskAuthoringReady || event.FromStatus == model.TaskAuthoringDone)
	if event.ID < 1 || event.Revision < 1 || model.ValidateProjectIdentifier(event.ProjectID) != nil || model.ValidateCanonicalTaskID(event.TaskID) != nil ||
		!complete && !archive ||
		utf8.RuneCountInString(event.Actor) < 1 || utf8.RuneCountInString(event.Actor) > 256 ||
		utf8.RuneCountInString(event.Reason) < 1 || utf8.RuneCountInString(event.Reason) > 1024 ||
		strings.ContainsRune(event.Actor, 0) || strings.ContainsRune(event.Reason, 0) ||
		len(event.Contract) == 0 || event.OperationID == "" {
		return event, fmt.Errorf("invalid task lifecycle event identity")
	}
	return event, nil
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
	rows, err := d.Shared.Query(ctx, taskLifecycleEventColumns+` WHERE project_id=? AND task_id=? AND event_kind='complete'`, projectID, taskID)
	if err != nil {
		return TaskLifecycleEvent{}, false, err
	}
	if len(rows.Rows) == 0 {
		return TaskLifecycleEvent{}, false, nil
	}
	if len(rows.Rows) != 1 {
		return TaskLifecycleEvent{}, false, fmt.Errorf("invalid task completion lifecycle history")
	}
	event, err := decodeTaskLifecycleEvent(rows.Rows[0])
	if err != nil {
		return TaskLifecycleEvent{}, false, err
	}
	return event, true, nil
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
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("task lifecycle event limit out of range")
	}
	rows, err := d.Shared.Query(ctx, taskLifecycleEventColumns+` WHERE project_id=? AND task_id=? ORDER BY id ASC LIMIT ?`, projectID, taskID, int64(limit))
	if err != nil {
		return nil, err
	}
	events := make([]TaskLifecycleEvent, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		event, err := decodeTaskLifecycleEvent(row)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
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
	recorded := req.RecordedAt.UTC().Format(time.RFC3339Nano)
	statements := []upstream.Statement{
		{SQL: `UPDATE shared_tasks SET payload=?,updated_at=? WHERE id=? AND revision=? AND payload=?`, Args: []any{req.TaskPayload, recorded, req.TaskID, req.Revision, req.PreviousTaskPayload}, RequireRowsAffected: 1},
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
		statements = append(statements, upstream.Statement{
			SQL: `UPDATE shared_task_execution_states SET status=?,execution_revision=?,updated_at=? WHERE project_id=? AND task_id=? AND status=? AND stage=? AND worktree=? AND base_head_sha=? AND head_sha=? AND branch=? AND agent=? AND task_revision=? AND task_revision_sha256=? AND execution_revision=?`,
			Args: []any{final.Status, final.ExecutionRevision, final.UpdatedAt.UTC().Format(time.RFC3339Nano),
				prev.ProjectID, prev.TaskID, prev.Status, prev.Stage, prev.Worktree, prev.BaseHead, prev.Head, prev.Branch, prev.Agent, prev.TaskRevision, prev.TaskRevisionSHA256, prev.ExecutionRevision},
			RequireRowsAffected: 1,
		})
	}
	statements = append(statements,
		upstream.Statement{SQL: `INSERT INTO shared_task_lifecycle_events(operation_id,project_id,task_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, Args: []any{req.OperationID, req.ProjectID, req.TaskID, req.Revision, TaskLifecycleEventKindComplete, req.FromStatus, model.TaskAuthoringDone, req.Actor, req.Reason, req.Contract, recorded}, RequireRowsAffected: 1},
		upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at,operation_id,request_sha256) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{req.OperationID, "task", req.TaskID, req.ProjectID, req.Revision, "task-complete", req.TaskPayload, recorded, req.OperationID, req.ContractSHA256}, RequireRowsAffected: 1},
	)
	_, err := d.Shared.Batch(ctx, statements)
	return err
}

type TaskHistoryCursor struct {
	RecordedAt string `json:"recorded_at"`
	Revision   int64  `json:"revision,omitempty"`
	Source     int    `json:"source"`
	ID         int64  `json:"id"`
}

func EncodeTaskHistoryCursor(cursor TaskHistoryCursor) (string, error) {
	if err := validateTaskHistoryCursor(cursor, false); err != nil {
		return "", err
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func DecodeTaskHistoryCursor(raw string) (TaskHistoryCursor, error) {
	var cursor TaskHistoryCursor
	if raw == "" {
		return TaskHistoryCursor{}, fmt.Errorf("invalid task history cursor")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cursor); err != nil {
		return TaskHistoryCursor{}, fmt.Errorf("invalid task history cursor")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return TaskHistoryCursor{}, fmt.Errorf("invalid task history cursor")
	}
	if err := validateTaskHistoryCursor(cursor, false); err != nil {
		return TaskHistoryCursor{}, err
	}
	return cursor, nil
}

func validateTaskHistoryCursor(cursor TaskHistoryCursor, allowZero bool) error {
	if allowZero && cursor == (TaskHistoryCursor{}) {
		return nil
	}
	if cursor.Source != 0 && cursor.Source != 1 {
		return fmt.Errorf("invalid task history cursor source")
	}
	if cursor.Revision < 0 {
		return fmt.Errorf("invalid task history cursor revision")
	}
	if cursor.ID < 1 {
		return fmt.Errorf("invalid task history cursor id")
	}
	parsed, err := time.Parse(time.RFC3339Nano, cursor.RecordedAt)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != cursor.RecordedAt {
		return fmt.Errorf("invalid task history cursor timestamp")
	}
	return nil
}

func (d *Databases) normalizeTaskHistoryCursor(ctx context.Context, projectID, taskID string, cursor TaskHistoryCursor) (TaskHistoryCursor, error) {
	if cursor == (TaskHistoryCursor{}) || cursor.Revision > 0 {
		return cursor, nil
	}
	var query string
	var args []any
	if cursor.Source == 0 {
		query = `SELECT revision FROM shared_entity_revisions WHERE entity_type='task' AND project_id=? AND entity_id=? AND revision=? AND recorded_at=?`
		args = []any{projectID, taskID, cursor.ID, cursor.RecordedAt}
	} else {
		query = `SELECT revision FROM shared_task_lifecycle_events WHERE project_id=? AND task_id=? AND id=? AND recorded_at=?`
		args = []any{projectID, taskID, cursor.ID, cursor.RecordedAt}
	}
	rows, err := d.Shared.Query(ctx, query, args...)
	if err != nil {
		return TaskHistoryCursor{}, err
	}
	if len(rows.Rows) != 1 {
		return TaskHistoryCursor{}, fmt.Errorf("invalid legacy task history cursor")
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok || revision < 1 {
		return TaskHistoryCursor{}, fmt.Errorf("invalid legacy task history cursor")
	}
	cursor.Revision = revision
	return cursor, nil
}

func (d *Databases) ListTaskHistoryPage(ctx context.Context, projectID, taskID string, after TaskHistoryCursor, limit int) (SharedHistoryPage, error) {
	if d == nil || d.Shared == nil {
		return SharedHistoryPage{}, fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return SharedHistoryPage{}, err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return SharedHistoryPage{}, err
	}
	if limit < 1 || limit > SharedLifecycleQueryMaxRows {
		return SharedHistoryPage{}, fmt.Errorf("invalid task history page")
	}
	if err := validateTaskHistoryCursor(after, true); err != nil {
		return SharedHistoryPage{}, err
	}
	after, err := d.normalizeTaskHistoryCursor(ctx, projectID, taskID, after)
	if err != nil {
		return SharedHistoryPage{}, err
	}
	type keyed struct {
		record   SharedRevisionRecord
		revision int64
		source   int
		id       int64
		at       time.Time
	}
	var merged []keyed
	contentSQL := `SELECT entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM shared_entity_revisions WHERE entity_type='task' AND project_id=? AND entity_id=? AND revision>? ORDER BY revision ASC LIMIT ?`
	contentArgs := []any{projectID, taskID, after.Revision, int64(limit) + 1}
	contentRows, err := d.Shared.Query(ctx, contentSQL, contentArgs...)
	if err != nil {
		return SharedHistoryPage{}, err
	}
	for _, row := range contentRows.Rows {
		item, err := decodeSharedRevisionRow(row)
		if err != nil {
			return SharedHistoryPage{}, err
		}
		at, err := time.Parse(time.RFC3339Nano, item.RecordedAt)
		if err != nil {
			return SharedHistoryPage{}, fmt.Errorf("invalid shared revision recorded_at")
		}
		merged = append(merged, keyed{
			record:   item,
			revision: item.Revision,
			source:   0,
			id:       item.Revision,
			at:       at,
		})
	}
	var lifecycleSQL string
	var lifecycleArgs []any
	if after == (TaskHistoryCursor{}) || after.Source == 0 {
		lifecycleSQL = taskLifecycleEventColumns + ` WHERE project_id=? AND task_id=? AND revision>=? ORDER BY revision ASC, id ASC LIMIT ?`
		lifecycleArgs = []any{projectID, taskID, after.Revision, int64(limit) + 1}
	} else {
		lifecycleSQL = taskLifecycleEventColumns + ` WHERE project_id=? AND task_id=? AND (revision>? OR (revision=? AND id>?)) ORDER BY revision ASC, id ASC LIMIT ?`
		lifecycleArgs = []any{projectID, taskID, after.Revision, after.Revision, after.ID, int64(limit) + 1}
	}
	lifecycleRows, err := d.Shared.Query(ctx, lifecycleSQL, lifecycleArgs...)
	if err != nil {
		return SharedHistoryPage{}, err
	}
	for _, row := range lifecycleRows.Rows {
		event, err := decodeTaskLifecycleEvent(row)
		if err != nil {
			return SharedHistoryPage{}, err
		}
		changedFields := []string{"status"}
		if event.FromStatus == model.TaskAuthoringReady {
			changedFields = append(changedFields, "ready_seal")
		}
		merged = append(merged, keyed{
			record: SharedRevisionRecord{
				EntityID:      event.TaskID,
				ProjectID:     event.ProjectID,
				Revision:      event.Revision,
				MutationKind:  event.EventKind,
				Actor:         event.Actor,
				Reason:        event.Reason,
				ChangedFields: changedFields,
				Payload:       append([]byte(nil), event.Contract...),
				RecordedAt:    event.RecordedAt.UTC().Format(time.RFC3339Nano),
			},
			revision: event.Revision,
			source:   1,
			id:       event.ID,
			at:       event.RecordedAt.UTC(),
		})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].revision != merged[j].revision {
			return merged[i].revision < merged[j].revision
		}
		if merged[i].source != merged[j].source {
			return merged[i].source < merged[j].source
		}
		return merged[i].id < merged[j].id
	})
	page := SharedHistoryPage{Records: make([]SharedRevisionRecord, 0, len(merged))}
	for _, item := range merged {
		page.Records = append(page.Records, item.record)
	}
	if len(page.Records) > limit {
		page.HasMore = true
		last := merged[limit-1]
		page.Records = page.Records[:limit]
		cursor, err := EncodeTaskHistoryCursor(TaskHistoryCursor{
			RecordedAt: last.at.UTC().Format(time.RFC3339Nano),
			Revision:   last.revision,
			Source:     last.source,
			ID:         last.id,
		})
		if err != nil {
			return SharedHistoryPage{}, err
		}
		page.NextCursor = cursor
	}
	return page, nil
}

const TaskLifecycleEventKindArchive = "archive"

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
	recorded := req.RecordedAt.UTC().Format(time.RFC3339Nano)
	contractSum := sha256.Sum256(req.Contract)
	_, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_tasks SET payload=?,updated_at=? WHERE id=? AND revision=? AND payload=?`, Args: []any{req.TaskPayload, recorded, req.TaskID, req.Revision, req.PreviousTaskPayload}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_task_lifecycle_events(operation_id,project_id,task_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, Args: []any{req.OperationID, req.ProjectID, req.TaskID, req.Revision, TaskLifecycleEventKindArchive, req.FromStatus, model.TaskAuthoringArchived, req.Actor, req.Reason, req.Contract, recorded}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at,operation_id,request_sha256) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{req.OperationID, "task", req.TaskID, req.ProjectID, req.Revision, "task-archive", req.TaskPayload, recorded, req.OperationID, hex.EncodeToString(contractSum[:])}, RequireRowsAffected: 1},
	})
	return err
}
