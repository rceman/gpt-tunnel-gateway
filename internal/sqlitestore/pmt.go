package sqlitestore

import (
	"context"
	"fmt"
	"os"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const pmtColumns = `id,project_id,project_code,title,instruction,planner_session_id,target_session_id,target_airelay_session_key,target_agent_id,train_id,item_position,task_id,attempt_number,created_at,state,first_fetched_at,delivered_at,last_fetched_at,cancelled_at,superseded_by,reference,reference_submitted_at,read_count,expires_at`

func pmtTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parsePMTTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	s, ok := value.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("invalid PMT timestamp")
	}
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func pmtFromRow(row []any) (model.PMT, error) {
	if len(row) != 24 {
		return model.PMT{}, fmt.Errorf("invalid PMT row")
	}
	stringAt := func(i int) (string, error) {
		v, ok := row[i].(string)
		if !ok {
			return "", fmt.Errorf("invalid PMT string column %d", i)
		}
		return v, nil
	}
	intAt := func(i int) (int64, error) {
		v, ok := row[i].(int64)
		if !ok {
			return 0, fmt.Errorf("invalid PMT integer column %d", i)
		}
		return v, nil
	}
	id, err := stringAt(0)
	if err != nil {
		return model.PMT{}, err
	}
	projectID, err := stringAt(1)
	if err != nil {
		return model.PMT{}, err
	}
	projectCode, err := stringAt(2)
	if err != nil {
		return model.PMT{}, err
	}
	title, err := stringAt(3)
	if err != nil {
		return model.PMT{}, err
	}
	instruction, ok := row[4].([]byte)
	if !ok {
		return model.PMT{}, fmt.Errorf("invalid PMT instruction")
	}
	planner, err := stringAt(5)
	if err != nil {
		return model.PMT{}, err
	}
	targetSession, err := stringAt(6)
	if err != nil {
		return model.PMT{}, err
	}
	targetAirelay, err := stringAt(7)
	if err != nil {
		return model.PMT{}, err
	}
	targetAgent, err := stringAt(8)
	if err != nil {
		return model.PMT{}, err
	}
	trainID, err := stringAt(9)
	if err != nil {
		return model.PMT{}, err
	}
	position, err := intAt(10)
	if err != nil {
		return model.PMT{}, err
	}
	taskID, err := stringAt(11)
	if err != nil {
		return model.PMT{}, err
	}
	attempt, err := intAt(12)
	if err != nil {
		return model.PMT{}, err
	}
	createdText, err := stringAt(13)
	if err != nil {
		return model.PMT{}, err
	}
	created, err := time.Parse(time.RFC3339Nano, createdText)
	if err != nil {
		return model.PMT{}, err
	}
	state, err := stringAt(14)
	if err != nil {
		return model.PMT{}, err
	}
	firstFetched, err := parsePMTTime(row[15])
	if err != nil {
		return model.PMT{}, err
	}
	delivered, err := parsePMTTime(row[16])
	if err != nil {
		return model.PMT{}, err
	}
	lastFetched, err := parsePMTTime(row[17])
	if err != nil {
		return model.PMT{}, err
	}
	cancelled, err := parsePMTTime(row[18])
	if err != nil {
		return model.PMT{}, err
	}
	supersededBy, err := stringAt(19)
	if err != nil {
		return model.PMT{}, err
	}
	reference, err := stringAt(20)
	if err != nil {
		return model.PMT{}, err
	}
	referenceSubmitted, err := parsePMTTime(row[21])
	if err != nil {
		return model.PMT{}, err
	}
	readCount, err := intAt(22)
	if err != nil {
		return model.PMT{}, err
	}
	expires, err := parsePMTTime(row[23])
	if err != nil {
		return model.PMT{}, err
	}
	if position < 0 || attempt < 0 {
		return model.PMT{}, fmt.Errorf("invalid PMT execution position")
	}
	return model.PMT{SchemaVersion: model.PMTSchemaVersion, ID: id, ProjectID: projectID, ProjectCode: projectCode, Title: title, Instruction: string(instruction), PlannerSessionID: planner, TargetSessionID: targetSession, TargetAirelaySessionKey: targetAirelay, TargetAgentID: targetAgent, TrainID: trainID, ItemPosition: int(position), TaskID: taskID, AttemptNumber: uint64(attempt), CreatedAt: created, State: state, FirstFetchedAt: firstFetched, DeliveredAt: delivered, LastFetchedAt: lastFetched, CancelledAt: cancelled, SupersededBy: supersededBy, Reference: reference, ReferenceSubmittedAt: referenceSubmitted, ReadCount: int(readCount), ExpiresAt: expires}, nil
}

func pmtArgs(p model.PMT) []any {
	return []any{p.ID, p.ProjectID, p.ProjectCode, p.Title, []byte(p.Instruction), p.PlannerSessionID, p.TargetSessionID, p.TargetAirelaySessionKey, p.TargetAgentID, p.TrainID, p.ItemPosition, p.TaskID, int64(p.AttemptNumber), p.CreatedAt.UTC().Format(time.RFC3339Nano), p.State, pmtTime(p.FirstFetchedAt), pmtTime(p.DeliveredAt), pmtTime(p.LastFetchedAt), pmtTime(p.CancelledAt), p.SupersededBy, p.Reference, pmtTime(p.ReferenceSubmittedAt), p.ReadCount, pmtTime(p.ExpiresAt)}
}

func (d *Databases) nextPMTNumber(ctx context.Context, projectID, projectCode string) (int64, error) {
	if d == nil || d.Local == nil {
		return 0, fmt.Errorf("local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT project_code,next_number FROM local_pmt_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) == 0 {
		if _, err := d.Local.Exec(ctx, `INSERT OR IGNORE INTO local_pmt_sequences(project_id,project_code,next_number) VALUES(?,?,1)`, projectID, projectCode); err != nil {
			return 0, err
		}
		rows, err = d.Local.Query(ctx, `SELECT project_code,next_number FROM local_pmt_sequences WHERE project_id=?`, projectID)
		if err != nil {
			return 0, err
		}
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode {
		return 0, fmt.Errorf("local PMT project code mismatch")
	}
	n, ok := rows.Rows[0][1].(int64)
	if !ok || n < 1 {
		return 0, fmt.Errorf("invalid local PMT sequence")
	}
	return n, nil
}

func (d *Databases) CreatePMT(ctx context.Context, pmt model.PMT) (model.PMT, error) {
	if d == nil || d.Local == nil {
		return model.PMT{}, fmt.Errorf("local store is unavailable")
	}
	if err := d.PrunePMTs(ctx, time.Now().UTC().Add(-30*24*time.Hour)); err != nil {
		return model.PMT{}, err
	}
	if pmt.ID != "" {
		return model.PMT{}, fmt.Errorf("PMT ID is server allocated")
	}
	for attempt := 0; attempt < 8; attempt++ {
		n, err := d.nextPMTNumber(ctx, pmt.ProjectID, pmt.ProjectCode)
		if err != nil {
			return model.PMT{}, err
		}
		candidate := pmt
		candidate.ID = fmt.Sprintf("%s-PMT%d", pmt.ProjectCode, n)
		if err := model.ValidatePMT(candidate); err != nil {
			return model.PMT{}, err
		}
		_, err = d.Local.Batch(ctx, []upstream.Statement{
			{SQL: `UPDATE local_pmt_sequences SET next_number=? WHERE project_id=? AND project_code=? AND next_number=?`, Args: []any{n + 1, pmt.ProjectID, pmt.ProjectCode, n}, RequireRowsAffected: 1},
			{SQL: `INSERT INTO local_pmts(` + pmtColumns + `) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Args: pmtArgs(candidate), RequireRowsAffected: 1},
		})
		if err == nil {
			return candidate, nil
		}
	}
	return model.PMT{}, fmt.Errorf("local PMT sequence changed during allocation")
}

func (d *Databases) ReadPMT(ctx context.Context, id string) (model.PMT, error) {
	if d == nil || d.Local == nil {
		return model.PMT{}, fmt.Errorf("local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT `+pmtColumns+` FROM local_pmts WHERE id=?`, id)
	if err != nil {
		return model.PMT{}, err
	}
	if len(rows.Rows) == 0 {
		return model.PMT{}, fmt.Errorf("PMT %q: %w", id, os.ErrNotExist)
	}
	pmt, err := pmtFromRow(rows.Rows[0])
	if err != nil {
		return model.PMT{}, err
	}
	if err := model.ValidatePMT(pmt); err != nil {
		return model.PMT{}, err
	}
	return pmt, nil
}

func (d *Databases) ListPendingPMTs(ctx context.Context, projectID, targetAirelay string, limit int) ([]model.PMTSummary, int, error) {
	if d == nil || d.Local == nil {
		return nil, 0, fmt.Errorf("local store is unavailable")
	}
	if limit < 1 || limit > model.MaxPMTQueueEntries {
		return nil, 0, fmt.Errorf("invalid PMT queue limit")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	countRows, err := d.Local.Query(ctx, `SELECT COUNT(*) FROM local_pmts WHERE project_id=? AND target_airelay_session_key=? AND state=? AND (expires_at IS NULL OR expires_at>?)`, projectID, targetAirelay, model.PMTStateUnread, now)
	if err != nil {
		return nil, 0, err
	}
	if len(countRows.Rows) != 1 {
		return nil, 0, fmt.Errorf("invalid PMT queue count")
	}
	count, ok := countRows.Rows[0][0].(int64)
	if !ok {
		return nil, 0, fmt.Errorf("invalid PMT queue count")
	}
	rows, err := d.Local.Query(ctx, `SELECT id,title,state,created_at FROM local_pmts WHERE project_id=? AND target_airelay_session_key=? AND state=? AND (expires_at IS NULL OR expires_at>?) ORDER BY created_at,id LIMIT ?`, projectID, targetAirelay, model.PMTStateUnread, now, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]model.PMTSummary, 0, len(rows.Rows))
	for i, row := range rows.Rows {
		if len(row) != 4 {
			return nil, 0, fmt.Errorf("invalid PMT queue row")
		}
		id, idOK := row[0].(string)
		title, titleOK := row[1].(string)
		state, stateOK := row[2].(string)
		createdText, createdOK := row[3].(string)
		created, parseErr := time.Parse(time.RFC3339Nano, createdText)
		if !idOK || !titleOK || !stateOK || !createdOK || parseErr != nil {
			return nil, 0, fmt.Errorf("invalid PMT queue row")
		}
		result = append(result, model.PMTSummary{ID: id, Title: title, State: state, CreatedAt: created, Order: i + 1})
	}
	return result, int(count), nil
}

func (d *Databases) MarkPMTFetched(ctx context.Context, id string, now time.Time) (model.PMT, bool, error) {
	pmt, err := d.ReadPMT(ctx, id)
	if err != nil {
		return model.PMT{}, false, err
	}
	if pmt.State != model.PMTStateUnread && pmt.State != model.PMTStateFetched {
		return pmt, false, nil
	}
	if pmt.ExpiresAt != nil && !pmt.ExpiresAt.After(now) && pmt.State == model.PMTStateUnread {
		_, err = d.Local.Exec(ctx, `UPDATE local_pmts SET state=? WHERE id=? AND state=?`, model.PMTStateExpired, id, model.PMTStateUnread)
		if err != nil {
			return model.PMT{}, false, err
		}
		pmt, err = d.ReadPMT(ctx, id)
		return pmt, false, err
	}
	first := pmt.FirstFetchedAt
	if first == nil {
		first = &now
	}
	updated := pmt
	updated.State = model.PMTStateFetched
	updated.FirstFetchedAt = first
	updated.DeliveredAt = first
	updated.LastFetchedAt = &now
	updated.ReadCount++
	result, err := d.Local.Exec(ctx, `UPDATE local_pmts SET state=?,first_fetched_at=?,delivered_at=?,last_fetched_at=?,read_count=? WHERE id=? AND state=?`, updated.State, pmtTime(updated.FirstFetchedAt), pmtTime(updated.DeliveredAt), pmtTime(updated.LastFetchedAt), updated.ReadCount, id, model.PMTStateUnread)
	if err != nil {
		return model.PMT{}, false, err
	}
	if result.RowsAffected == 1 {
		return updated, true, nil
	}
	result, err = d.Local.Exec(ctx, `UPDATE local_pmts SET last_fetched_at=?,read_count=read_count+1 WHERE id=? AND state=?`, pmtTime(&now), id, model.PMTStateFetched)
	if err != nil {
		return model.PMT{}, false, err
	}
	if result.RowsAffected != 1 {
		return model.PMT{}, false, fmt.Errorf("PMT read race")
	}
	updated, err = d.ReadPMT(ctx, id)
	return updated, true, err
}

func (d *Databases) CancelPMT(ctx context.Context, id string, now time.Time) (bool, error) {
	result, err := d.Local.Exec(ctx, `UPDATE local_pmts SET state=?,cancelled_at=? WHERE id=? AND state=?`, model.PMTStateCancelled, now.UTC().Format(time.RFC3339Nano), id, model.PMTStateUnread)
	if err != nil {
		return false, err
	}
	return result.RowsAffected == 1, nil
}

func (d *Databases) MarkPMTReferenceSubmitted(ctx context.Context, id, reference string, now time.Time) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	_, err := d.Local.Exec(ctx, `UPDATE local_pmts SET reference=?,reference_submitted_at=? WHERE id=? AND reference_submitted_at IS NULL`, reference, now.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (d *Databases) SupersedeAndCreatePMT(ctx context.Context, oldIDs []string, pmt model.PMT) (model.PMT, error) {
	if len(oldIDs) == 0 {
		return d.CreatePMT(ctx, pmt)
	}
	for attempt := 0; attempt < 8; attempt++ {
		n, err := d.nextPMTNumber(ctx, pmt.ProjectID, pmt.ProjectCode)
		if err != nil {
			return model.PMT{}, err
		}
		candidate := pmt
		candidate.ID = fmt.Sprintf("%s-PMT%d", pmt.ProjectCode, n)
		if err := model.ValidatePMT(candidate); err != nil {
			return model.PMT{}, err
		}
		statements := []upstream.Statement{{SQL: `UPDATE local_pmt_sequences SET next_number=? WHERE project_id=? AND project_code=? AND next_number=?`, Args: []any{n + 1, pmt.ProjectID, pmt.ProjectCode, n}, RequireRowsAffected: 1}}
		for _, oldID := range oldIDs {
			statements = append(statements, upstream.Statement{SQL: `UPDATE local_pmts SET state=?,superseded_by=? WHERE id=? AND project_id=? AND target_airelay_session_key=? AND state=?`, Args: []any{model.PMTStateSuperseded, candidate.ID, oldID, pmt.ProjectID, pmt.TargetAirelaySessionKey, model.PMTStateUnread}, RequireRowsAffected: 1})
		}
		statements = append(statements, upstream.Statement{SQL: `INSERT INTO local_pmts(` + pmtColumns + `) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Args: pmtArgs(candidate), RequireRowsAffected: 1})
		if _, err := d.Local.Batch(ctx, statements); err == nil {
			return candidate, nil
		}
	}
	return model.PMT{}, fmt.Errorf("PMT supersession race")
}

func (d *Databases) PrunePMTs(ctx context.Context, cutoff time.Time) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	_, err := d.Local.Exec(ctx, `DELETE FROM local_pmts WHERE state IN (?,?,?,?) AND COALESCE(last_fetched_at,cancelled_at,created_at)<?`, model.PMTStateFetched, model.PMTStateCancelled, model.PMTStateSuperseded, model.PMTStateExpired, cutoff.UTC().Format(time.RFC3339Nano))
	return err
}
