package sqlitestore

import (
	"context"
	"fmt"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

type TokenUsageEvent struct {
	EventID      string
	SessionID    string
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	RecordedAt   time.Time
}

type TokenUsageAggregate struct {
	SessionID    string
	RequestCount int64
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	UpdatedAt    time.Time
}

func (d *Databases) RecordTokenUsage(ctx context.Context, event TokenUsageEvent) error {
	if d == nil || d.Local == nil || event.EventID == "" || event.SessionID == "" || event.InputTokens < 0 || event.OutputTokens < 0 || event.TotalTokens < 0 || event.RecordedAt.IsZero() {
		return fmt.Errorf("invalid token usage event")
	}
	if event.TotalTokens != event.InputTokens+event.OutputTokens {
		return fmt.Errorf("token usage total does not match input and output")
	}
	at := event.RecordedAt.UTC().Format(time.RFC3339Nano)
	_, err := d.Local.Batch(ctx, []upstream.Statement{
		{SQL: `INSERT OR IGNORE INTO local_token_usage_events(event_id,session_id,input_tokens,output_tokens,total_tokens,recorded_at) VALUES(?,?,?,?,?,?)`, Args: []any{event.EventID, event.SessionID, event.InputTokens, event.OutputTokens, event.TotalTokens, at}},
		{SQL: `INSERT INTO local_token_usage(session_id,request_count,input_tokens,output_tokens,total_tokens,updated_at) SELECT ?,1,?,?,?,? WHERE changes()=1 ON CONFLICT(session_id) DO UPDATE SET request_count=request_count+1,input_tokens=input_tokens+excluded.input_tokens,output_tokens=output_tokens+excluded.output_tokens,total_tokens=total_tokens+excluded.total_tokens,updated_at=excluded.updated_at`, Args: []any{event.SessionID, event.InputTokens, event.OutputTokens, event.TotalTokens, at}},
	})
	return err
}

func (d *Databases) ReadTokenUsage(ctx context.Context, sessionID string) (TokenUsageAggregate, error) {
	if d == nil || d.Local == nil || sessionID == "" {
		return TokenUsageAggregate{}, fmt.Errorf("invalid token usage session")
	}
	rows, err := d.Local.Query(ctx, `SELECT session_id,request_count,input_tokens,output_tokens,total_tokens,updated_at FROM local_token_usage WHERE session_id=?`, sessionID)
	if err != nil {
		return TokenUsageAggregate{}, err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 6 {
		return TokenUsageAggregate{}, fmt.Errorf("token usage aggregate not found")
	}
	row := rows.Rows[0]
	result := TokenUsageAggregate{SessionID: sessionID}
	var ok bool
	if result.RequestCount, ok = row[1].(int64); !ok || result.RequestCount < 0 {
		return TokenUsageAggregate{}, fmt.Errorf("invalid token usage request count")
	}
	if result.InputTokens, ok = row[2].(int64); !ok || result.InputTokens < 0 {
		return TokenUsageAggregate{}, fmt.Errorf("invalid token usage input count")
	}
	if result.OutputTokens, ok = row[3].(int64); !ok || result.OutputTokens < 0 {
		return TokenUsageAggregate{}, fmt.Errorf("invalid token usage output count")
	}
	if result.TotalTokens, ok = row[4].(int64); !ok || result.TotalTokens < 0 {
		return TokenUsageAggregate{}, fmt.Errorf("invalid token usage total count")
	}
	timestamp, ok := row[5].(string)
	if !ok {
		return TokenUsageAggregate{}, fmt.Errorf("invalid token usage timestamp type")
	}
	result.UpdatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return TokenUsageAggregate{}, fmt.Errorf("invalid token usage timestamp: %w", err)
	}
	return result, nil
}
