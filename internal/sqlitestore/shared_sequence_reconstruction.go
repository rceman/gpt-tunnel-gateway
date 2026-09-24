package sqlitestore

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const sharedSequenceReconstructionMaxEntities = 4096

func (d *Databases) ReconstructSharedEntitySequences(ctx context.Context, projectID, projectCode string) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return err
	}
	specs := []struct {
		entityType string
		table      string
		parse      func(string) (string, uint64, error)
	}{
		{"task", "shared_tasks", model.ParseTaskID},
		{"adr", "shared_adrs", model.ParseADRID},
		{"rule", "shared_rules", model.ParseRuleID},
		{"journal", "shared_journals", model.ParseJournalID},
		{"milestone", "shared_milestones", model.ParseMilestoneID},
		{"track", "shared_tracks", model.ParseTrackID},
	}
	for _, spec := range specs {
		rows, err := d.Shared.Query(ctx, "SELECT id FROM "+spec.table+" WHERE json_extract(payload,'$.project_id')=? ORDER BY id LIMIT ?", projectID, int64(sharedSequenceReconstructionMaxEntities+1))
		if err != nil {
			return err
		}
		if len(rows.Rows) > sharedSequenceReconstructionMaxEntities {
			return fmt.Errorf("Shared %s sequence reconstruction exceeds bounded entity maximum", spec.entityType)
		}
		var maximum uint64
		for _, row := range rows.Rows {
			if len(row) != 1 {
				return fmt.Errorf("invalid Shared %s identifier row", spec.entityType)
			}
			id, ok := row[0].(string)
			if !ok {
				return fmt.Errorf("invalid Shared %s identifier value", spec.entityType)
			}
			code, number, err := spec.parse(id)
			if err != nil || code != projectCode {
				return fmt.Errorf("Shared %s identifier %q conflicts with project code", spec.entityType, id)
			}
			if number > maximum {
				maximum = number
			}
		}
		next := maximum + 1
		if next < 1 || next > model.MaxSafeInteger {
			return fmt.Errorf("Shared %s sequence exceeds the safe identifier range", spec.entityType)
		}
		if currentCode, currentNext, found, err := d.ReadSharedSequence(ctx, spec.entityType, projectID); err != nil {
			return err
		} else if found {
			if currentCode != projectCode {
				return fmt.Errorf("Shared %s sequence project code conflicts", spec.entityType)
			}
			if currentNext > int64(next) {
				next = uint64(currentNext)
			}
		}
		_, err = d.Shared.Exec(ctx, `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES(?,?,?,?) ON CONFLICT(entity_type,project_id) DO UPDATE SET next_number=MAX(shared_entity_sequences.next_number,excluded.next_number) WHERE shared_entity_sequences.project_code=excluded.project_code`, spec.entityType, projectID, projectCode, int64(next))
		if err != nil {
			return err
		}
		currentCode, currentNext, found, err := d.ReadSharedSequence(ctx, spec.entityType, projectID)
		if err != nil || !found || currentCode != projectCode || currentNext < int64(next) {
			return fmt.Errorf("Shared %s sequence reconstruction did not persist", spec.entityType)
		}
	}
	return nil
}
