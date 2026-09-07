package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnsureSharedLifecycleHistory inserts one immutable history row without
// touching current state or the Hub outbox. Existing rows are accepted only
// when every persisted field is byte-for-byte identical.
func (d *Databases) EnsureSharedLifecycleHistory(ctx context.Context, entityType string, record SharedRevisionRecord) error {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.HistoryTable == "" {
		return fmt.Errorf("shared lifecycle %q has no revision history", entityType)
	}
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if record.EntityID == "" || record.ProjectID == "" || record.Revision < 1 || record.MutationKind == "" || record.Actor == "" || record.Reason == "" || len(record.Payload) == 0 || record.RecordedAt == "" {
		return fmt.Errorf("invalid shared %s history record", entityType)
	}
	changedFields, err := json.Marshal(record.ChangedFields)
	if err != nil {
		return err
	}
	compare := func(existing SharedRevisionRecord) error {
		existingFields, marshalErr := json.Marshal(existing.ChangedFields)
		if marshalErr != nil || existing.EntityID != record.EntityID || existing.ProjectID != record.ProjectID || existing.Revision != record.Revision || existing.MutationKind != record.MutationKind || existing.Actor != record.Actor || existing.Reason != record.Reason || !bytes.Equal(existingFields, changedFields) || !bytes.Equal(existing.Payload, record.Payload) || existing.RecordedAt != record.RecordedAt {
			return fmt.Errorf("shared %s history conflict for %s:%d", entityType, record.EntityID, record.Revision)
		}
		return nil
	}
	if existing, readErr := d.ReadSharedRevision(ctx, entityType, record.ProjectID, record.EntityID, record.Revision); readErr == nil {
		return compare(existing)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	insert := fmt.Sprintf("INSERT INTO %s(%s,%s,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)", definition.HistoryTable, definition.HistoryEntityColumn, definition.HistoryIDColumn)
	if _, insertErr := d.Shared.Exec(ctx, insert, definition.EntityType, record.EntityID, record.ProjectID, record.Revision, record.MutationKind, record.Actor, record.Reason, changedFields, record.Payload, record.RecordedAt); insertErr == nil {
		return nil
	} else if existing, readErr := d.ReadSharedRevision(ctx, entityType, record.ProjectID, record.EntityID, record.Revision); readErr == nil {
		return compare(existing)
	} else {
		return insertErr
	}
}

func (d *Databases) nextSharedLifecycleNumber(ctx context.Context, definition sharedLifecycleDefinition, projectID, projectCode string, initial int64) (int64, error) {
	query := fmt.Sprintf("SELECT %s,%s FROM %s WHERE %s=? AND project_id=?", definition.SequenceCodeColumn, definition.SequenceNumberColumn, definition.SequenceTable, definition.SequenceEntityColumn)
	rows, err := d.Shared.Query(ctx, query, definition.EntityType, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) == 0 {
		if initial < 1 {
			initial = 1
		}
		insert := fmt.Sprintf("INSERT OR IGNORE INTO %s(%s,project_id,%s,%s) VALUES(?,?,?,?)", definition.SequenceTable, definition.SequenceEntityColumn, definition.SequenceCodeColumn, definition.SequenceNumberColumn)
		if _, err := d.Shared.Exec(ctx, insert, definition.EntityType, projectID, projectCode, initial); err != nil {
			return 0, err
		}
		rows, err = d.Shared.Query(ctx, query, definition.EntityType, projectID)
		if err != nil {
			return 0, err
		}
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode {
		return 0, fmt.Errorf("shared %s project code mismatch", definition.EntityType)
	}
	next, ok := rows.Rows[0][1].(int64)
	if !ok || next < 1 {
		return 0, fmt.Errorf("invalid shared %s sequence", definition.EntityType)
	}
	return next, nil
}

func (d *Databases) ReadSharedSequence(ctx context.Context, entityType, projectID string) (string, int64, bool, error) {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.SequenceTable == "" {
		return "", 0, false, fmt.Errorf("shared lifecycle %q has no allocator", entityType)
	}
	if d == nil || d.Shared == nil {
		return "", 0, false, fmt.Errorf("shared store is unavailable")
	}
	query := fmt.Sprintf("SELECT %s,%s FROM %s WHERE %s=? AND project_id=?", definition.SequenceCodeColumn, definition.SequenceNumberColumn, definition.SequenceTable, definition.SequenceEntityColumn)
	rows, err := d.Shared.Query(ctx, query, definition.EntityType, projectID)
	if err != nil {
		return "", 0, false, err
	}
	if len(rows.Rows) == 0 {
		return "", 0, false, nil
	}
	if len(rows.Rows) != 1 {
		return "", 0, false, fmt.Errorf("invalid shared %s sequence", entityType)
	}
	code, codeOK := rows.Rows[0][0].(string)
	next, nextOK := rows.Rows[0][1].(int64)
	if !codeOK || !nextOK || next < 1 {
		return "", 0, false, fmt.Errorf("invalid shared %s sequence", entityType)
	}
	return code, next, true, nil
}

func (d *Databases) PutSharedSequence(ctx context.Context, entityType, projectID, projectCode string, next int64) error {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.SequenceTable == "" || d == nil || d.Shared == nil {
		return fmt.Errorf("invalid shared %s sequence", entityType)
	}
	if projectID == "" || len(projectCode) != 3 || strings.ToUpper(projectCode) != projectCode || next < 1 {
		return fmt.Errorf("invalid shared %s sequence", entityType)
	}
	sql := fmt.Sprintf("INSERT INTO %s(%s,project_id,%s,%s) VALUES(?,?,?,?) ON CONFLICT(%s,project_id) DO UPDATE SET %s=excluded.%s,%s=CASE WHEN excluded.%s > %s.%s THEN excluded.%s ELSE %s.%s END", definition.SequenceTable, definition.SequenceEntityColumn, definition.SequenceCodeColumn, definition.SequenceNumberColumn, definition.SequenceEntityColumn, definition.SequenceCodeColumn, definition.SequenceCodeColumn, definition.SequenceNumberColumn, definition.SequenceNumberColumn, definition.SequenceTable, definition.SequenceNumberColumn, definition.SequenceNumberColumn, definition.SequenceTable, definition.SequenceNumberColumn)
	_, err := d.Shared.Exec(ctx, sql, entityType, projectID, projectCode, next)
	return err
}

func (d *Databases) ReadSharedRevision(ctx context.Context, entityType, projectID, entityID string, revision int64) (SharedRevisionRecord, error) {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.HistoryTable == "" {
		return SharedRevisionRecord{}, fmt.Errorf("shared lifecycle %q has no revision history", entityType)
	}
	if d == nil || d.Shared == nil {
		return SharedRevisionRecord{}, fmt.Errorf("shared store is unavailable")
	}
	query := fmt.Sprintf("SELECT %s,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM %s WHERE %s=? AND project_id=? AND %s=? AND revision=?", definition.HistoryIDColumn, definition.HistoryTable, definition.HistoryEntityColumn, definition.HistoryIDColumn)
	rows, err := d.Shared.Query(ctx, query, entityType, projectID, entityID, revision)
	if err != nil {
		return SharedRevisionRecord{}, err
	}
	if len(rows.Rows) == 0 {
		return SharedRevisionRecord{}, fmt.Errorf("shared %s revision %s:%d: %w", entityType, entityID, revision, os.ErrNotExist)
	}
	return decodeSharedRevisionRow(rows.Rows[0])
}

func (d *Databases) ListSharedHistory(ctx context.Context, entityType, projectID, entityID string, limit int) ([]SharedRevisionRecord, error) {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.HistoryTable == "" || d == nil || d.Shared == nil {
		return nil, fmt.Errorf("shared lifecycle %q has no revision history", entityType)
	}
	if projectID == "" || entityID == "" || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid %s history limit", entityType)
	}
	query := fmt.Sprintf("SELECT %s,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM %s WHERE %s=? AND project_id=? AND %s=? ORDER BY revision ASC LIMIT ?", definition.HistoryIDColumn, definition.HistoryTable, definition.HistoryEntityColumn, definition.HistoryIDColumn)
	rows, err := d.Shared.Query(ctx, query, entityType, projectID, entityID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]SharedRevisionRecord, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		item, err := decodeSharedRevisionRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (d *Databases) ListSharedHistoryPage(ctx context.Context, entityType, projectID, entityID string, afterRevision int64, limit int) (SharedHistoryPage, error) {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.HistoryTable == "" || d == nil || d.Shared == nil {
		return SharedHistoryPage{}, fmt.Errorf("shared lifecycle %q has no revision history", entityType)
	}
	if projectID == "" || entityID == "" || afterRevision < 0 || limit < 1 || limit > 1000 {
		return SharedHistoryPage{}, fmt.Errorf("invalid %s history page", entityType)
	}
	query := fmt.Sprintf("SELECT %s,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM %s WHERE %s=? AND project_id=? AND %s=? AND revision>? ORDER BY revision ASC LIMIT ?", definition.HistoryIDColumn, definition.HistoryTable, definition.HistoryEntityColumn, definition.HistoryIDColumn)
	rows, err := d.Shared.Query(ctx, query, entityType, projectID, entityID, afterRevision, limit+1)
	if err != nil {
		return SharedHistoryPage{}, err
	}
	page := SharedHistoryPage{Records: make([]SharedRevisionRecord, 0, len(rows.Rows))}
	for _, row := range rows.Rows {
		item, err := decodeSharedRevisionRow(row)
		if err != nil {
			return SharedHistoryPage{}, err
		}
		page.Records = append(page.Records, item)
	}
	if len(page.Records) > limit {
		page.HasMore = true
		page.Records = page.Records[:limit]
		page.NextRevision = page.Records[len(page.Records)-1].Revision
	}
	return page, nil
}

func decodeSharedRevisionRow(row []any) (SharedRevisionRecord, error) {
	if len(row) != 9 {
		return SharedRevisionRecord{}, fmt.Errorf("invalid shared revision row")
	}
	entityID, a := row[0].(string)
	projectID, b := row[1].(string)
	revision, c := row[2].(int64)
	kind, d := row[3].(string)
	actor, e := row[4].(string)
	reason, f := row[5].(string)
	changedBytes, g := rowBytes(row[6])
	payload, h := row[7].([]byte)
	recorded, i := row[8].(string)
	if !a || !b || !c || !d || !e || !f || !g || !h || !i {
		return SharedRevisionRecord{}, fmt.Errorf("invalid shared revision row")
	}
	var fields []string
	if err := json.Unmarshal(changedBytes, &fields); err != nil {
		return SharedRevisionRecord{}, fmt.Errorf("invalid shared changed fields: %w", err)
	}
	return SharedRevisionRecord{
		EntityID:      entityID,
		ProjectID:     projectID,
		Revision:      revision,
		MutationKind:  kind,
		Actor:         actor,
		Reason:        reason,
		ChangedFields: fields,
		Payload:       append([]byte(nil), payload...),
		RecordedAt:    recorded,
	}, nil
}

func rowBytes(value any) ([]byte, bool) {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...), true
	case string:
		return []byte(typed), true
	default:
		return nil, false
	}
}
