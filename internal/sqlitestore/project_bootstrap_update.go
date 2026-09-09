package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type ProjectBootstrapUpdate struct {
	ProjectID           string
	PreviousProjectCode string
	ProjectCode         string
	NextTaskNumber      uint64
	NextADRNumber       uint64
	Configuration       model.ProjectConfiguration
}

// ReconcileProjectBootstrap is the typed Shared authority for correcting a
// virgin project's code. It never publishes an outbox mutation.
func (d *Databases) ReconcileProjectBootstrap(ctx context.Context, in ProjectBootstrapUpdate) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return err
	}
	if err := model.ValidateProjectCode(in.PreviousProjectCode); err != nil {
		return fmt.Errorf("previous project code: %w", err)
	}
	if err := model.ValidateProjectCode(in.ProjectCode); err != nil {
		return err
	}
	if err := model.ValidateCompactIDNumber(in.NextTaskNumber); err != nil {
		return fmt.Errorf("next task number: %w", err)
	}
	if err := model.ValidateCompactIDNumber(in.NextADRNumber); err != nil {
		return fmt.Errorf("next ADR number: %w", err)
	}
	if in.PreviousProjectCode == in.ProjectCode {
		return fmt.Errorf("project code is unchanged")
	}
	if in.Configuration.ProjectID != in.ProjectID {
		return fmt.Errorf("project configuration identity mismatch")
	}
	if err := model.ValidateProjectConfiguration(in.Configuration); err != nil {
		return err
	}
	payload, err := json.Marshal(in.Configuration)
	if err != nil {
		return err
	}
	identifiers, err := readBootstrapIdentifiers(ctx, d, in.ProjectID)
	if err != nil {
		return err
	}
	if identifiers.found && identifiers.projectCode != in.PreviousProjectCode && identifiers.projectCode != in.ProjectCode {
		return fmt.Errorf("Shared project identifier code conflicts with requested correction")
	}
	if identifiers.found && (identifiers.nextTask < 1 || identifiers.nextADR < 1) {
		return fmt.Errorf("Shared project identifier counters are invalid")
	}
	sequences, err := readBootstrapSequences(ctx, d, in.ProjectID)
	if err != nil {
		return err
	}
	for _, sequence := range sequences {
		if sequence.projectCode != in.PreviousProjectCode && sequence.projectCode != in.ProjectCode {
			return fmt.Errorf("Shared %s sequence code conflicts with requested correction", sequence.entityType)
		}
		if sequence.nextNumber < 1 {
			return fmt.Errorf("Shared %s sequence counter is invalid", sequence.entityType)
		}
	}
	for _, table := range []string{"shared_tasks", "shared_adrs", "shared_trains", "shared_journals", "shared_rules"} {
		rows, err := d.Shared.Query(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id LIKE ?", in.ProjectCode+"-%")
		if err != nil {
			return err
		}
		if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
			return fmt.Errorf("invalid Shared %s count", table)
		}
		count, ok := rows.Rows[0][0].(int64)
		if !ok {
			return fmt.Errorf("invalid Shared %s count", table)
		}
		if count != 0 {
			return fmt.Errorf("project %q is not bootstrap-only: Shared %s already exists", in.ProjectID, table)
		}
	}
	configurationPresent := false
	if entity, err := d.ReadSharedEntity(ctx, "project_configuration", in.ProjectID); err == nil {
		if entity.Revision != int64(in.Configuration.Revision) || !bytes.Equal(entity.Payload, payload) {
			var existing model.ProjectConfiguration
			if json.Unmarshal(entity.Payload, &existing) != nil || !reflect.DeepEqual(existing, in.Configuration) {
				return fmt.Errorf("Shared project configuration conflicts with Hub authority")
			}
		}
		configurationPresent = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	statements := make([]upstream.Statement, 0, 7)
	if identifiers.found {
		statements = append(statements, upstream.Statement{SQL: `UPDATE shared_project_identifiers SET project_code=? WHERE project_id=? AND project_code=?`, Args: []any{in.ProjectCode, in.ProjectID, identifiers.projectCode}, RequireRowsAffected: 1})
	} else {
		statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_project_identifiers(project_id,project_code,next_task_number,next_adr_number,next_rule_number,next_journal_number,next_train_number) VALUES(?,?,?,?,?,?,?)`, Args: []any{in.ProjectID, in.ProjectCode, in.NextTaskNumber, in.NextADRNumber, 1, 1, 1}, RequireRowsAffected: 1})
	}
	for _, sequence := range sequences {
		statements = append(statements, upstream.Statement{SQL: `UPDATE shared_entity_sequences SET project_code=? WHERE entity_type=? AND project_id=? AND project_code=?`, Args: []any{in.ProjectCode, sequence.entityType, in.ProjectID, sequence.projectCode}, RequireRowsAffected: 1})
	}
	for _, entityType := range []string{"task", "adr"} {
		if !hasBootstrapSequence(sequences, entityType) {
			statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES(?,?,?,?)`, Args: []any{entityType, in.ProjectID, in.ProjectCode, maxBootstrapNext(sequences, entityType, sequenceFallback(entityType, identifiers, in))}, RequireRowsAffected: 1})
		}
	}
	if !configurationPresent {
		statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{in.ProjectID, in.Configuration.Revision, payload, in.Configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)}, RequireRowsAffected: 1})
	}
	_, err = d.Shared.Batch(ctx, statements)
	return err
}

type bootstrapIdentifiers struct {
	found             bool
	projectCode       string
	nextTask, nextADR int64
}
type bootstrapSequence struct {
	entityType, projectCode string
	nextNumber              int64
}

func readBootstrapIdentifiers(ctx context.Context, d *Databases, projectID string) (bootstrapIdentifiers, error) {
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_task_number,next_adr_number FROM shared_project_identifiers WHERE project_id=?`, projectID)
	if err != nil {
		return bootstrapIdentifiers{}, err
	}
	if len(rows.Rows) == 0 {
		return bootstrapIdentifiers{}, nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return bootstrapIdentifiers{}, fmt.Errorf("invalid Shared project identifiers row")
	}
	code, ok := rows.Rows[0][0].(string)
	if !ok {
		return bootstrapIdentifiers{}, fmt.Errorf("invalid Shared project identifier code")
	}
	task, ok := rows.Rows[0][1].(int64)
	if !ok {
		return bootstrapIdentifiers{}, fmt.Errorf("invalid Shared task counter")
	}
	adr, ok := rows.Rows[0][2].(int64)
	if !ok {
		return bootstrapIdentifiers{}, fmt.Errorf("invalid Shared ADR counter")
	}
	return bootstrapIdentifiers{true, code, task, adr}, nil
}

func readBootstrapSequences(ctx context.Context, d *Databases, projectID string) ([]bootstrapSequence, error) {
	rows, err := d.Shared.Query(ctx, `SELECT entity_type,project_code,next_number FROM shared_entity_sequences WHERE project_id=? AND entity_type IN ('task','adr') ORDER BY entity_type`, projectID)
	if err != nil {
		return nil, err
	}
	result := make([]bootstrapSequence, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 3 {
			return nil, fmt.Errorf("invalid Shared entity sequence row")
		}
		typ, a := row[0].(string)
		code, b := row[1].(string)
		next, c := row[2].(int64)
		if !a || !b || !c {
			return nil, fmt.Errorf("invalid Shared entity sequence row")
		}
		result = append(result, bootstrapSequence{typ, code, next})
	}
	return result, nil
}
func hasBootstrapSequence(rows []bootstrapSequence, typ string) bool {
	for _, row := range rows {
		if row.entityType == typ {
			return true
		}
	}
	return false
}
func sequenceFallback(typ string, ids bootstrapIdentifiers, in ProjectBootstrapUpdate) int64 {
	if typ == "adr" && ids.nextADR > 0 {
		return ids.nextADR
	}
	if ids.nextTask > 0 {
		return ids.nextTask
	}
	if typ == "adr" {
		return int64(in.NextADRNumber)
	}
	return int64(in.NextTaskNumber)
}
func maxBootstrapNext(rows []bootstrapSequence, typ string, fallback int64) int64 {
	for _, row := range rows {
		if row.entityType == typ && row.nextNumber > fallback {
			fallback = row.nextNumber
		}
	}
	if fallback < 1 {
		return 1
	}
	return fallback
}
