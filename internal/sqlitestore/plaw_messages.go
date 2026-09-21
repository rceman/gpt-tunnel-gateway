package sqlitestore

import (
	"context"
	"fmt"
	"os"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const plawMessageColumns = `id,project_id,project_code,from_role,from_session,to_role,body,title,in_reply_to,state,created_at,read_at,read_session,cancelled_at,cancelled_by`

func messageTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseMessageTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return nil, fmt.Errorf("invalid message timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func plawMessageFromRow(row []any) (model.Message, error) {
	if len(row) != 15 {
		return model.Message{}, fmt.Errorf("invalid PLAW message row")
	}
	stringAt := func(index int) (string, error) {
		value, ok := row[index].(string)
		if !ok {
			return "", fmt.Errorf("invalid PLAW message string column %d", index)
		}
		return value, nil
	}
	body, ok := row[6].([]byte)
	if !ok {
		return model.Message{}, fmt.Errorf("invalid PLAW message body")
	}
	id, err := stringAt(0)
	if err != nil {
		return model.Message{}, err
	}
	projectID, err := stringAt(1)
	if err != nil {
		return model.Message{}, err
	}
	projectCode, err := stringAt(2)
	if err != nil {
		return model.Message{}, err
	}
	fromRole, err := stringAt(3)
	if err != nil {
		return model.Message{}, err
	}
	fromSession, err := stringAt(4)
	if err != nil {
		return model.Message{}, err
	}
	toRole, err := stringAt(5)
	if err != nil {
		return model.Message{}, err
	}
	title, err := stringAt(7)
	if err != nil {
		return model.Message{}, err
	}
	inReplyTo, err := stringAt(8)
	if err != nil {
		return model.Message{}, err
	}
	state, err := stringAt(9)
	if err != nil {
		return model.Message{}, err
	}
	createdText, err := stringAt(10)
	if err != nil {
		return model.Message{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdText)
	if err != nil {
		return model.Message{}, err
	}
	readAt, err := parseMessageTime(row[11])
	if err != nil {
		return model.Message{}, err
	}
	readSession, err := stringAt(12)
	if err != nil {
		return model.Message{}, err
	}
	cancelledAt, err := parseMessageTime(row[13])
	if err != nil {
		return model.Message{}, err
	}
	cancelledBy, err := stringAt(14)
	if err != nil {
		return model.Message{}, err
	}
	message := model.Message{
		SchemaVersion: model.MessageSchemaVersion,
		ID:            id,
		ProjectID:     projectID,
		ProjectCode:   projectCode,
		FromRole:      fromRole,
		FromSession:   fromSession,
		ToRole:        toRole,
		Body:          string(body),
		Title:         title,
		InReplyTo:     inReplyTo,
		State:         state,
		CreatedAt:     createdAt,
		ReadAt:        readAt,
		ReadSession:   readSession,
		CancelledAt:   cancelledAt,
		CancelledBy:   cancelledBy,
	}
	if err := model.ValidatePLAWMessage(message); err != nil {
		return model.Message{}, err
	}
	return message, nil
}

func plawMessageArgs(message model.Message) []any {
	return []any{
		message.ID, message.ProjectID, message.ProjectCode, message.FromRole, message.FromSession,
		message.ToRole, []byte(message.Body), message.Title, message.InReplyTo, message.State,
		message.CreatedAt.UTC().Format(time.RFC3339Nano), messageTime(message.ReadAt), message.ReadSession,
		messageTime(message.CancelledAt), message.CancelledBy,
	}
}

func (d *Databases) nextPLAWMessageNumber(ctx context.Context, projectID, projectCode string) (int64, error) {
	if d == nil || d.Local == nil {
		return 0, fmt.Errorf("local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT project_code,next_number FROM plaw_message_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) == 0 {
		if _, err := d.Local.Exec(ctx, `INSERT OR IGNORE INTO plaw_message_sequences(project_id,project_code,next_number) VALUES(?,?,1)`, projectID, projectCode); err != nil {
			return 0, err
		}
		rows, err = d.Local.Query(ctx, `SELECT project_code,next_number FROM plaw_message_sequences WHERE project_id=?`, projectID)
		if err != nil {
			return 0, err
		}
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode {
		return 0, fmt.Errorf("PLAW message project code mismatch")
	}
	number, ok := rows.Rows[0][1].(int64)
	if !ok || number < 1 {
		return 0, fmt.Errorf("invalid PLAW message sequence")
	}
	return number, nil
}

func (d *Databases) CreatePLAWMessage(ctx context.Context, message model.Message) (model.Message, error) {
	if d == nil || d.Local == nil {
		return model.Message{}, fmt.Errorf("local store is unavailable")
	}
	if message.ID != "" {
		return model.Message{}, fmt.Errorf("message ID is server allocated")
	}
	for attempt := 0; attempt < 8; attempt++ {
		number, err := d.nextPLAWMessageNumber(ctx, message.ProjectID, message.ProjectCode)
		if err != nil {
			return model.Message{}, err
		}
		candidate := message
		candidate.ID, err = model.FormatMessageID(message.ProjectCode, uint64(number))
		if err != nil {
			return model.Message{}, err
		}
		if err := model.ValidatePLAWMessage(candidate); err != nil {
			return model.Message{}, err
		}
		_, err = d.Local.Batch(ctx, []upstream.Statement{
			{SQL: `UPDATE plaw_message_sequences SET next_number=? WHERE project_id=? AND project_code=? AND next_number=?`, Args: []any{number + 1, message.ProjectID, message.ProjectCode, number}, RequireRowsAffected: 1},
			{SQL: `INSERT INTO plaw_messages(` + plawMessageColumns + `) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Args: plawMessageArgs(candidate), RequireRowsAffected: 1},
		})
		if err == nil {
			return candidate, nil
		}
	}
	return model.Message{}, fmt.Errorf("PLAW message sequence changed during allocation")
}

func (d *Databases) ReadPLAWMessage(ctx context.Context, id string) (model.Message, error) {
	if d == nil || d.Local == nil || model.ValidateMessageID(id) != nil {
		return model.Message{}, fmt.Errorf("invalid message identifier")
	}
	rows, err := d.Local.Query(ctx, `SELECT `+plawMessageColumns+` FROM plaw_messages WHERE id=?`, id)
	if err != nil {
		return model.Message{}, err
	}
	if len(rows.Rows) == 0 {
		return model.Message{}, fmt.Errorf("message %q: %w", id, os.ErrNotExist)
	}
	return plawMessageFromRow(rows.Rows[0])
}

func (d *Databases) MarkPLAWMessageRead(ctx context.Context, id, projectID, toRole, readSession string, now time.Time) (model.Message, error) {
	message, err := d.ReadPLAWMessage(ctx, id)
	if err != nil {
		return model.Message{}, err
	}
	if message.ProjectID != projectID || message.ToRole != toRole {
		return model.Message{}, fmt.Errorf("message receiver authority mismatch")
	}
	if message.State == model.MessageStateCancelled {
		return model.Message{}, fmt.Errorf("cancelled message cannot be read")
	}
	if message.State == model.MessageStateRead {
		return message, nil
	}
	readAt := now.UTC().Format(time.RFC3339Nano)
	result, err := d.Local.Exec(ctx, `UPDATE plaw_messages SET state=?,read_at=?,read_session=? WHERE id=? AND project_id=? AND to_role=? AND state=?`, model.MessageStateRead, readAt, readSession, id, projectID, toRole, model.MessageStateUnread)
	if err != nil {
		return model.Message{}, err
	}
	if result.RowsAffected == 0 {
		message, err = d.ReadPLAWMessage(ctx, id)
		if err != nil {
			return model.Message{}, err
		}
		if message.ProjectID != projectID || message.ToRole != toRole || message.State != model.MessageStateRead {
			return model.Message{}, fmt.Errorf("message read race")
		}
		return message, nil
	}
	return d.ReadPLAWMessage(ctx, id)
}

func (d *Databases) CancelPLAWMessage(ctx context.Context, id, projectID, actorSession, actorRole string, now time.Time) (model.Message, error) {
	message, err := d.ReadPLAWMessage(ctx, id)
	if err != nil {
		return model.Message{}, err
	}
	if message.ProjectID != projectID {
		return model.Message{}, fmt.Errorf("message project authority mismatch")
	}
	if message.State != model.MessageStateUnread {
		return model.Message{}, fmt.Errorf("only unread messages can be cancelled")
	}
	result, err := d.Local.Exec(ctx, `UPDATE plaw_messages SET state=?,cancelled_at=?,cancelled_by=? WHERE id=? AND project_id=? AND state=? AND (from_session=? OR ?=?)`, model.MessageStateCancelled, now.UTC().Format(time.RFC3339Nano), actorSession, id, projectID, model.MessageStateUnread, actorSession, actorRole, "planner")
	if err != nil {
		return model.Message{}, err
	}
	if result.RowsAffected == 0 {
		return model.Message{}, fmt.Errorf("message cancellation authority mismatch")
	}
	return d.ReadPLAWMessage(ctx, id)
}

func (d *Databases) ListPLAWMessages(ctx context.Context, projectID, toRole string, unreadOnly bool, afterCreatedAt, afterID string, limit int) ([]model.Message, bool, error) {
	if d == nil || d.Local == nil {
		return nil, false, fmt.Errorf("local store is unavailable")
	}
	if limit < 1 || limit > model.MessagePageMax {
		return nil, false, fmt.Errorf("invalid message page size")
	}
	query := `SELECT ` + plawMessageColumns + ` FROM plaw_messages WHERE project_id=? AND to_role=?`
	args := []any{projectID, toRole}
	if unreadOnly {
		query += ` AND state=?`
		args = append(args, model.MessageStateUnread)
	}
	if afterCreatedAt != "" || afterID != "" {
		if afterCreatedAt == "" || afterID == "" {
			return nil, false, fmt.Errorf("invalid message cursor")
		}
		query += ` AND (created_at>? OR (created_at=? AND id>?))`
		args = append(args, afterCreatedAt, afterCreatedAt, afterID)
	}
	query += ` ORDER BY created_at,id LIMIT ?`
	args = append(args, limit+1)
	rows, err := d.Local.Query(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	messages := make([]model.Message, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		message, decodeErr := plawMessageFromRow(row)
		if decodeErr != nil {
			return nil, false, decodeErr
		}
		messages = append(messages, message)
	}
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	return messages, hasMore, nil
}
