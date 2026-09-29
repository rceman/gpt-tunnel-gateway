package sqlitestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const maxProjectRetirementRows = 4096

var (
	ErrProjectRetirementUnsafe   = errors.New("project has active or unresolved local authority")
	ErrProjectRetirementConflict = errors.New("project retirement conflicts with durable state")
	ErrLocalProjectRetired       = errors.New("Local project is retired")
)

type LocalProjectRetirement struct {
	ProjectID string
	Reason    string
	RetiredAt time.Time
}

func (d *Databases) BeginLocalProjectRetirement(ctx context.Context, retirement model.ProjectRetirement) error {
	return d.beginLocalProjectRetirement(ctx, retirement, nil, false)
}

func (d *Databases) beginLocalProjectRetirement(ctx context.Context, retirement model.ProjectRetirement, legacyEpochIDs []string, allowLegacy bool) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("Local store is unavailable")
	}
	if err := model.ValidateProjectRetirement(retirement); err != nil {
		return err
	}
	if existing, found, err := d.ReadLocalProjectRetirement(ctx, retirement.ProjectID); err != nil {
		return err
	} else if found {
		if existing.Reason != retirement.Reason {
			return ErrProjectRetirementConflict
		}
		allowed, err := d.localProjectRetirementReplayAllowed(ctx, retirement)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrProjectRetirementUnsafe
		}
		return nil
	}
	statement := localProjectRetirementInsertStatement(retirement, allowLegacy, legacyEpochIDs)
	_, err := d.Local.Batch(ctx, []upstream.Statement{
		statement,
		{SQL: `DELETE FROM local_session_bootstrap_grants WHERE project_id=?`, Args: []any{retirement.ProjectID}},
	})
	if err == nil {
		return nil
	}
	if existing, found, readErr := d.ReadLocalProjectRetirement(ctx, retirement.ProjectID); readErr == nil && found {
		if existing.Reason != retirement.Reason {
			return ErrProjectRetirementConflict
		}
		allowed, replayErr := d.localProjectRetirementReplayAllowed(ctx, retirement)
		if replayErr != nil {
			return replayErr
		}
		if allowed {
			return nil
		}
	}
	blocked, checkErr := d.localProjectRetirementBlocked(ctx, retirement.ProjectID)
	if checkErr != nil {
		return checkErr
	}
	if blocked && allowLegacy {
		blocked, checkErr = d.localProjectRetirementBlockedAllowingLegacyEpochs(ctx, retirement.ProjectID)
		if checkErr != nil {
			return checkErr
		}
	}
	if blocked {
		return ErrProjectRetirementUnsafe
	}
	return err
}

func (d *Databases) localProjectRetirementReplayAllowed(ctx context.Context, retirement model.ProjectRetirement) (bool, error) {
	blocked, err := d.localProjectRetirementBlocked(ctx, retirement.ProjectID)
	if err != nil || !blocked {
		return !blocked, err
	}
	if retirement.LegacyCallbackEpochCount == 0 || retirement.LegacyCallbackEpochSHA256 == "" {
		return false, nil
	}
	evidence, _, err := d.readProjectRetirementLegacyEpochEvidence(ctx, retirement.ProjectID)
	if err != nil {
		return false, err
	}
	if evidence.Count != retirement.LegacyCallbackEpochCount || evidence.SHA256 != retirement.LegacyCallbackEpochSHA256 {
		return false, nil
	}
	blocked, err = d.localProjectRetirementBlockedAllowingLegacyEpochs(ctx, retirement.ProjectID)
	return !blocked, err
}

func (d *Databases) ReadLocalProjectRetirement(ctx context.Context, projectID string) (LocalProjectRetirement, bool, error) {
	if d == nil || d.Local == nil || model.ValidateProjectIdentifier(projectID) != nil {
		return LocalProjectRetirement{}, false, fmt.Errorf("invalid Local project retirement selector")
	}
	rows, err := d.Local.Query(ctx, `SELECT project_id,reason,retired_at FROM local_project_retirements WHERE project_id=?`, projectID)
	if err != nil {
		return LocalProjectRetirement{}, false, err
	}
	if len(rows.Rows) == 0 {
		return LocalProjectRetirement{}, false, nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return LocalProjectRetirement{}, false, fmt.Errorf("invalid Local project retirement row")
	}
	id, idOK := rows.Rows[0][0].(string)
	reason, reasonOK := rows.Rows[0][1].(string)
	retiredAtText, timeOK := rows.Rows[0][2].(string)
	retiredAt, parseErr := time.Parse(time.RFC3339Nano, retiredAtText)
	if !idOK || !reasonOK || !timeOK || parseErr != nil || retiredAt.IsZero() || id != projectID || strings.TrimSpace(reason) != reason || len(reason) == 0 || len(reason) > 512 {
		return LocalProjectRetirement{}, false, fmt.Errorf("invalid Local project retirement values")
	}
	return LocalProjectRetirement{
		ProjectID: id,
		Reason:    reason,
		RetiredAt: retiredAt,
	}, true, nil
}

func (d *Databases) LocalProjectRetirementIDs(ctx context.Context) (map[string]struct{}, error) {
	retirements, err := d.ListLocalProjectRetirements(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(retirements))
	for _, retirement := range retirements {
		ids[retirement.ProjectID] = struct{}{}
	}
	return ids, nil
}

func (d *Databases) ListLocalProjectRetirements(ctx context.Context) ([]LocalProjectRetirement, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("Local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT project_id,reason,retired_at FROM local_project_retirements ORDER BY project_id LIMIT ?`, maxProjectRetirementRows+1)
	if err != nil {
		return nil, err
	}
	if len(rows.Rows) > maxProjectRetirementRows {
		return nil, fmt.Errorf("Local project retirement enumeration exceeds bounded row maximum")
	}
	retirements := make([]LocalProjectRetirement, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 3 {
			return nil, fmt.Errorf("invalid Local project retirement row")
		}
		id, idOK := row[0].(string)
		reason, reasonOK := row[1].(string)
		retiredAtText, timeOK := row[2].(string)
		retiredAt, parseErr := time.Parse(time.RFC3339Nano, retiredAtText)
		if !idOK || !reasonOK || !timeOK || parseErr != nil || retiredAt.IsZero() || model.ValidateProjectIdentifier(id) != nil || strings.TrimSpace(reason) != reason || len(reason) == 0 || len(reason) > 512 {
			return nil, fmt.Errorf("invalid Local project retirement identity")
		}
		retirements = append(retirements, LocalProjectRetirement{
			ProjectID: id,
			Reason:    reason,
			RetiredAt: retiredAt,
		})
	}
	return retirements, nil
}

func (d *Databases) ReadSharedProjectRetirement(ctx context.Context, projectID string) (model.ProjectRetirement, bool, error) {
	if d == nil || d.Shared == nil || model.ValidateProjectIdentifier(projectID) != nil {
		return model.ProjectRetirement{}, false, fmt.Errorf("invalid Shared project retirement selector")
	}
	rows, err := d.Shared.Query(ctx, `SELECT payload FROM shared_project_retirements WHERE project_id=?`, projectID)
	if err != nil {
		return model.ProjectRetirement{}, false, err
	}
	if len(rows.Rows) == 0 {
		return model.ProjectRetirement{}, false, nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return model.ProjectRetirement{}, false, fmt.Errorf("invalid Shared project retirement row")
	}
	payload, ok := rows.Rows[0][0].([]byte)
	if !ok || len(payload) == 0 || len(payload) > projectConfigurationHardCutMaxPayloadBytes {
		return model.ProjectRetirement{}, false, fmt.Errorf("invalid Shared project retirement payload")
	}
	var retirement model.ProjectRetirement
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&retirement); err != nil {
		return model.ProjectRetirement{}, false, fmt.Errorf("decode Shared project retirement: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return model.ProjectRetirement{}, false, fmt.Errorf("unexpected Shared project retirement payload suffix")
	}
	if err := model.ValidateProjectRetirement(retirement); err != nil || retirement.ProjectID != projectID {
		return model.ProjectRetirement{}, false, fmt.Errorf("invalid Shared project retirement authority")
	}
	return retirement, true, nil
}

func (d *Databases) ListSharedProjectRetirements(ctx context.Context) ([]model.ProjectRetirement, error) {
	if d == nil || d.Shared == nil {
		return nil, fmt.Errorf("Shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_id,payload FROM shared_project_retirements ORDER BY project_id LIMIT ?`, maxProjectRetirementRows+1)
	if err != nil {
		return nil, err
	}
	if len(rows.Rows) > maxProjectRetirementRows {
		return nil, fmt.Errorf("Shared project retirement enumeration exceeds bounded row maximum")
	}
	retirements := make([]model.ProjectRetirement, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 2 {
			return nil, fmt.Errorf("invalid Shared project retirement row")
		}
		projectID, idOK := row[0].(string)
		payload, payloadOK := row[1].([]byte)
		if !idOK || !payloadOK {
			return nil, fmt.Errorf("invalid Shared project retirement value types")
		}
		retirement, found, err := d.ReadSharedProjectRetirement(ctx, projectID)
		if err != nil || !found || len(payload) == 0 {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("invalid Shared project retirement row")
		}
		retirements = append(retirements, retirement)
	}
	return retirements, nil
}

func (d *Databases) RetireSharedProject(ctx context.Context, projectID, reason string, retiredAt time.Time) (model.ProjectRetirement, bool, error) {
	return d.retireSharedProject(ctx, projectID, reason, retiredAt, ProjectRetirementLegacyEpochEvidence{})
}

func (d *Databases) RetireSharedProjectWithLegacyEpochEvidence(ctx context.Context, projectID, reason string, retiredAt time.Time, evidence ProjectRetirementLegacyEpochEvidence) (model.ProjectRetirement, bool, error) {
	return d.retireSharedProject(ctx, projectID, reason, retiredAt, evidence)
}

func (d *Databases) retireSharedProject(ctx context.Context, projectID, reason string, retiredAt time.Time, evidence ProjectRetirementLegacyEpochEvidence) (model.ProjectRetirement, bool, error) {
	if d == nil || d.Shared == nil {
		return model.ProjectRetirement{}, false, fmt.Errorf("Shared store is unavailable")
	}
	if model.ValidateProjectIdentifier(projectID) != nil || retiredAt.IsZero() {
		return model.ProjectRetirement{}, false, fmt.Errorf("invalid project retirement request")
	}
	for attempt := 0; attempt < 3; attempt++ {
		if existing, found, err := d.ReadSharedProjectRetirement(ctx, projectID); err != nil {
			return model.ProjectRetirement{}, false, err
		} else if found {
			if existing.Reason != reason || existing.LegacyCallbackEpochCount != evidence.Count || existing.LegacyCallbackEpochSHA256 != evidence.SHA256 {
				return model.ProjectRetirement{}, false, ErrProjectRetirementConflict
			}
			return existing, false, nil
		}
		configurationRevision, err := d.projectConfigurationRetirementRevision(ctx, projectID)
		if err != nil {
			return model.ProjectRetirement{}, false, err
		}
		pending, err := d.pendingProjectConfigurationOutboxForRetirement(ctx, projectID)
		if err != nil {
			return model.ProjectRetirement{}, false, err
		}
		for _, entry := range pending {
			if int(entry.Revision) > configurationRevision {
				configurationRevision = int(entry.Revision)
			}
		}
		record := model.ProjectRetirement{
			SchemaVersion:               model.ProjectRetirementSchemaVersion,
			ProjectID:                   projectID,
			Revision:                    1,
			Reason:                      reason,
			Actor:                       "gatewayd",
			RetiredAt:                   retiredAt.UTC(),
			ConfigurationRevision:       configurationRevision,
			CancelledConfigPublications: len(pending),
			CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(pending),
			LegacyCallbackEpochCount:    evidence.Count,
			LegacyCallbackEpochSHA256:   evidence.SHA256,
		}
		if err := model.ValidateProjectRetirement(record); err != nil {
			return model.ProjectRetirement{}, false, err
		}
		payload, err := json.Marshal(record)
		if err != nil {
			return model.ProjectRetirement{}, false, err
		}
		at := record.RetiredAt.Format(time.RFC3339Nano)
		outboxID := "project-retirement-" + projectID
		statements := []upstream.Statement{
			{SQL: `INSERT INTO shared_project_retirements(project_id,revision,payload,retired_at) VALUES(?,?,?,?)`, Args: []any{projectID, record.Revision, payload, at}, RequireRowsAffected: 1},
			{SQL: `DELETE FROM shared_project_configurations WHERE id=?`, Args: []any{projectID}},
			{SQL: `UPDATE hub_outbox SET cancelled_at=?,cancellation_reason=? WHERE entity_type='project_configuration' AND (entity_id=? OR project_id=?) AND published_at IS NULL AND cancelled_at IS NULL`, Args: []any{at, "project retired", projectID, projectID}, RequireRowsAffected: int64(len(pending))},
			{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{outboxID, "project_retirement", projectID, projectID, record.Revision, "project-retirement", payload, at}, RequireRowsAffected: 1},
		}
		if _, err := d.Shared.Batch(ctx, statements); err == nil {
			return record, true, nil
		} else if existing, found, readErr := d.ReadSharedProjectRetirement(ctx, projectID); readErr == nil && found {
			if existing.Reason != reason {
				return model.ProjectRetirement{}, false, ErrProjectRetirementConflict
			}
			return existing, false, nil
		} else if attempt == 2 {
			return model.ProjectRetirement{}, false, err
		}
	}
	return model.ProjectRetirement{}, false, fmt.Errorf("project retirement did not converge")
}

func (d *Databases) ImportSharedProjectRetirement(ctx context.Context, retirement model.ProjectRetirement) error {
	if err := model.ValidateProjectRetirement(retirement); err != nil {
		return err
	}
	if existing, found, err := d.ReadSharedProjectRetirement(ctx, retirement.ProjectID); err != nil {
		return err
	} else if found {
		if !sameProjectRetirement(existing, retirement) {
			return ErrProjectRetirementConflict
		}
		return nil
	}
	configurationRevision, err := d.projectConfigurationRetirementSourceRevision(ctx, retirement.ProjectID)
	if err != nil {
		return err
	}
	if configurationRevision > retirement.ConfigurationRevision {
		return ErrProjectRetirementConflict
	}
	pending, err := d.pendingProjectConfigurationOutboxForImport(ctx, retirement.ProjectID)
	if err != nil {
		return err
	}
	for _, entry := range pending {
		if int(entry.Revision) > retirement.ConfigurationRevision {
			return ErrProjectRetirementConflict
		}
	}
	if len(pending) > 0 && (len(pending) != retirement.CancelledConfigPublications || pendingProjectConfigurationDigest(pending) != retirement.CancelledConfigOutboxSHA256) {
		return ErrProjectRetirementConflict
	}
	payload, err := json.Marshal(retirement)
	if err != nil {
		return err
	}
	at := retirement.RetiredAt.UTC().Format(time.RFC3339Nano)
	statements := []upstream.Statement{
		{SQL: `INSERT INTO shared_project_retirements(project_id,revision,payload,retired_at) VALUES(?,?,?,?)`, Args: []any{retirement.ProjectID, retirement.Revision, payload, at}, RequireRowsAffected: 1},
		{SQL: `DELETE FROM shared_project_configurations WHERE id=?`, Args: []any{retirement.ProjectID}},
		{SQL: `UPDATE hub_outbox SET cancelled_at=?,cancellation_reason=? WHERE entity_type='project_configuration' AND (entity_id=? OR project_id=?) AND published_at IS NULL AND cancelled_at IS NULL`, Args: []any{at, "project retired", retirement.ProjectID, retirement.ProjectID}, RequireRowsAffected: int64(len(pending))},
	}
	if _, err := d.Shared.Batch(ctx, statements); err != nil {
		if existing, found, readErr := d.ReadSharedProjectRetirement(ctx, retirement.ProjectID); readErr == nil && found && sameProjectRetirement(existing, retirement) {
			return nil
		}
		return err
	}
	return nil
}

func (d *Databases) MarkOutboxCancelled(ctx context.Context, id, reason string, at time.Time) error {
	if d == nil || d.Shared == nil || id == "" || len(id) > 256 || len(reason) == 0 || len(reason) > 512 || at.IsZero() {
		return fmt.Errorf("invalid outbox cancellation")
	}
	_, err := d.Shared.Exec(ctx, `UPDATE hub_outbox SET cancelled_at=?,cancellation_reason=? WHERE id=? AND published_at IS NULL AND cancelled_at IS NULL`, at.UTC().Format(time.RFC3339Nano), reason, id)
	return err
}

func (d *Databases) HasProjectConfigurationRetirementSource(ctx context.Context, projectID string) (bool, error) {
	if d == nil || d.Shared == nil || model.ValidateProjectIdentifier(projectID) != nil {
		return false, fmt.Errorf("invalid Shared ProjectConfiguration retirement selector")
	}
	rows, err := d.Shared.Query(ctx, `SELECT EXISTS(SELECT 1 FROM shared_project_configurations WHERE id=?) OR EXISTS(SELECT 1 FROM hub_outbox WHERE entity_type='project_configuration' AND (entity_id=? OR project_id=?) AND published_at IS NULL AND cancelled_at IS NULL) OR EXISTS(SELECT 1 FROM shared_entity_revisions WHERE entity_type='project_configuration' AND (entity_id=? OR project_id=?))`, projectID, projectID, projectID, projectID, projectID)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return false, fmt.Errorf("invalid Shared ProjectConfiguration retirement-source query")
	}
	present, ok := rows.Rows[0][0].(int64)
	if !ok || present != 0 && present != 1 {
		return false, fmt.Errorf("invalid Shared ProjectConfiguration retirement-source result")
	}
	return present == 1, nil
}

func (d *Databases) ProjectConfigurationMigrationComplete(ctx context.Context) (bool, error) {
	state, err := d.projectConfigurationMigrationState(ctx)
	return state == "complete", err
}

func (d *Databases) MigrateLocalSessionProjectCoordinates(ctx context.Context) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("Local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT session_id,payload FROM local_sessions WHERE status='active' AND project_id='' AND session_type='' ORDER BY session_id LIMIT ?`, maxProjectRetirementRows+1)
	if err != nil {
		return err
	}
	if len(rows.Rows) > maxProjectRetirementRows {
		return fmt.Errorf("Local Session identity migration exceeds bounded row maximum")
	}
	statements := make([]upstream.Statement, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 2 {
			return fmt.Errorf("invalid Local Session identity row")
		}
		id, idOK := row[0].(string)
		payload, payloadOK := row[1].([]byte)
		if !idOK || !payloadOK || len(payload) == 0 || len(payload) > 64<<10 {
			return fmt.Errorf("invalid Local Session identity values")
		}
		identity, err := decodeLocalSessionIdentity(payload)
		if err != nil {
			return fmt.Errorf("decode active Local Session identity")
		}
		if identity.ProjectID != "" && model.ValidateProjectIdentifier(identity.ProjectID) != nil || identity.SessionType != "" && identity.SessionType != "chatgpt" && identity.SessionType != "admin" || identity.Status != "" && identity.Status != "active" {
			return fmt.Errorf("invalid active Local Session identity")
		}
		if identity.ProjectID == "" && identity.SessionType == "" {
			continue
		}
		statements = append(statements, upstream.Statement{SQL: `UPDATE local_sessions SET project_id=?,session_type=? WHERE session_id=? AND payload=? AND status='active' AND project_id='' AND session_type=''`, Args: []any{identity.ProjectID, identity.SessionType, id, payload}, RequireRowsAffected: 1})
	}
	for offset := 0; offset < len(statements); offset += projectConfigurationHardCutBatchSize {
		end := offset + projectConfigurationHardCutBatchSize
		if end > len(statements) {
			end = len(statements)
		}
		if _, err := d.Local.Batch(ctx, statements[offset:end]); err != nil {
			return fmt.Errorf("migrate active Local Session identities: %w", err)
		}
	}
	return nil
}

func (d *Databases) projectConfigurationRetirementRevision(ctx context.Context, projectID string) (int, error) {
	currentRevision := 0
	rows, err := d.Shared.Query(ctx, `SELECT revision,payload FROM shared_project_configurations WHERE id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) > 1 || len(rows.Rows) == 1 && len(rows.Rows[0]) != 2 {
		return 0, fmt.Errorf("invalid Shared ProjectConfiguration retirement row")
	}
	if len(rows.Rows) == 1 {
		revision, revisionOK := rows.Rows[0][0].(int64)
		payload, payloadOK := rows.Rows[0][1].([]byte)
		if !payloadOK {
			if text, ok := rows.Rows[0][1].(string); ok {
				payload, payloadOK = []byte(text), true
			}
		}
		if !revisionOK || !payloadOK || revision < 1 || len(payload) == 0 || len(payload) > projectConfigurationHardCutMaxPayloadBytes || validateProjectConfigurationRetirementPayload(payload, projectID, revision) != nil {
			return 0, fmt.Errorf("invalid Shared ProjectConfiguration retirement authority")
		}
		currentRevision = int(revision)
	}
	history, err := d.Shared.Query(ctx, `SELECT revision FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? ORDER BY revision DESC LIMIT 1`, projectID)
	if err != nil {
		return 0, err
	}
	if len(history.Rows) > 1 || len(history.Rows) == 1 && len(history.Rows[0]) != 1 {
		return 0, fmt.Errorf("invalid Shared ProjectConfiguration history revision")
	}
	if len(history.Rows) == 1 {
		historyRevision, ok := history.Rows[0][0].(int64)
		if !ok || historyRevision < 1 {
			return 0, fmt.Errorf("invalid Shared ProjectConfiguration history revision")
		}
		if int(historyRevision) > currentRevision {
			currentRevision = int(historyRevision)
		}
	}
	return currentRevision, nil
}

func (d *Databases) projectConfigurationRetirementSourceRevision(ctx context.Context, projectID string) (int, error) {
	currentRevision := 0
	rows, err := d.Shared.Query(ctx, `SELECT revision FROM shared_project_configurations WHERE id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) > 1 || len(rows.Rows) == 1 && len(rows.Rows[0]) != 1 {
		return 0, fmt.Errorf("invalid Shared ProjectConfiguration retirement row")
	}
	if len(rows.Rows) == 1 {
		revision, ok := rows.Rows[0][0].(int64)
		if !ok || revision < 1 {
			return 0, fmt.Errorf("invalid Shared ProjectConfiguration retirement revision")
		}
		currentRevision = int(revision)
	}
	history, err := d.Shared.Query(ctx, `SELECT revision FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? ORDER BY revision DESC LIMIT 1`, projectID)
	if err != nil {
		return 0, err
	}
	if len(history.Rows) > 1 || len(history.Rows) == 1 && len(history.Rows[0]) != 1 {
		return 0, fmt.Errorf("invalid Shared ProjectConfiguration history revision")
	}
	if len(history.Rows) == 1 {
		revision, ok := history.Rows[0][0].(int64)
		if !ok || revision < 1 {
			return 0, fmt.Errorf("invalid Shared ProjectConfiguration history revision")
		}
		if int(revision) > currentRevision {
			currentRevision = int(revision)
		}
	}
	return currentRevision, nil
}

func (d *Databases) pendingProjectConfigurationOutboxForRetirement(ctx context.Context, projectID string) ([]projectConfigurationRetirementOutbox, error) {
	return d.pendingProjectConfigurationOutbox(ctx, projectID, true)
}

func (d *Databases) pendingProjectConfigurationOutboxForImport(ctx context.Context, projectID string) ([]projectConfigurationRetirementOutbox, error) {
	return d.pendingProjectConfigurationOutbox(ctx, projectID, false)
}

func (d *Databases) pendingProjectConfigurationOutbox(ctx context.Context, projectID string, validatePayload bool) ([]projectConfigurationRetirementOutbox, error) {
	rows, err := d.Shared.Query(ctx, `SELECT id,entity_id,project_id,revision,payload FROM hub_outbox WHERE entity_type='project_configuration' AND (entity_id=? OR project_id=?) AND published_at IS NULL AND cancelled_at IS NULL ORDER BY id LIMIT ?`, projectID, projectID, maxProjectRetirementRows+1)
	if err != nil {
		return nil, err
	}
	if len(rows.Rows) > maxProjectRetirementRows {
		return nil, fmt.Errorf("project configuration outbox retirement exceeds bounded row maximum")
	}
	pending := make([]projectConfigurationRetirementOutbox, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 5 {
			return nil, fmt.Errorf("invalid pending ProjectConfiguration outbox row")
		}
		id, idOK := row[0].(string)
		entityID, entityOK := row[1].(string)
		rowProjectID, projectOK := row[2].(string)
		revision, revisionOK := row[3].(int64)
		payload, payloadOK := row[4].([]byte)
		if !payloadOK {
			if text, ok := row[4].(string); ok {
				payload, payloadOK = []byte(text), true
			}
		}
		invalidPayload := !payloadOK || len(payload) == 0 || len(payload) > projectConfigurationHardCutMaxPayloadBytes
		if validatePayload && !invalidPayload {
			invalidPayload = validateProjectConfigurationRetirementPayload(payload, projectID, revision) != nil
		}
		if !idOK || !entityOK || !projectOK || !revisionOK || id == "" || entityID != projectID || rowProjectID != "" && rowProjectID != projectID || revision < 1 || invalidPayload {
			return nil, fmt.Errorf("invalid pending ProjectConfiguration outbox authority")
		}
		pending = append(pending, projectConfigurationRetirementOutbox{
			ID:       id,
			Revision: revision,
			Payload:  payload,
		})
	}
	return pending, nil
}

type projectConfigurationRetirementOutbox struct {
	ID       string
	Revision int64
	Payload  []byte
}

func pendingProjectConfigurationDigest(entries []projectConfigurationRetirementOutbox) string {
	hash := sha256.New()
	for _, entry := range entries {
		payloadDigest := sha256.Sum256(entry.Payload)
		_, _ = fmt.Fprintf(hash, "%d:%s:%d:%s\n", len(entry.ID), entry.ID, entry.Revision, hex.EncodeToString(payloadDigest[:]))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validateProjectConfigurationRetirementPayload(payload []byte, projectID string, rowRevision int64) error {
	var identity struct {
		SchemaVersion int    `json:"schema_version"`
		ProjectID     string `json:"project_id"`
		Revision      int64  `json:"revision"`
	}
	if err := json.Unmarshal(payload, &identity); err != nil || identity.SchemaVersion < 1 || identity.SchemaVersion > model.ProjectConfigurationSchemaVersion || identity.ProjectID != projectID || identity.Revision != rowRevision {
		return fmt.Errorf("ProjectConfiguration payload identity mismatch")
	}
	if identity.SchemaVersion == model.ProjectConfigurationSchemaVersion {
		configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
		if err != nil || configuration.ProjectID != projectID || int64(configuration.Revision) != rowRevision {
			return fmt.Errorf("invalid canonical ProjectConfiguration payload")
		}
	}
	return nil
}

func (d *Databases) IsLocalProjectRetired(ctx context.Context, projectID string) (bool, error) {
	_, found, err := d.ReadLocalProjectRetirement(ctx, projectID)
	return found, err
}

func (d *Databases) localProjectRetirementBlocked(ctx context.Context, projectID string) (bool, error) {
	rows, err := d.Local.Query(ctx, `SELECT
EXISTS(SELECT 1 FROM local_sessions WHERE status='active' AND session_type<>'admin' AND (project_id=? OR project_id=''))
OR EXISTS(SELECT 1 FROM local_operations WHERE project_id=? AND status NOT IN ('completed','failed'))
OR EXISTS(SELECT 1 FROM local_task_execution_states WHERE project_id=? AND status NOT IN ('done','failed','abandoned'))
OR EXISTS(SELECT 1 FROM local_callback_epochs WHERE project_id=? AND (emitted_at IS NULL OR hook_outcome IS NULL OR hook_outcome NOT IN ('completed','failed','unbound')))`, projectID, projectID, projectID, projectID)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return false, fmt.Errorf("invalid Local project retirement safety query")
	}
	blocked, ok := rows.Rows[0][0].(int64)
	if !ok || blocked != 0 && blocked != 1 {
		return false, fmt.Errorf("invalid Local project retirement safety result")
	}
	return blocked == 1, nil
}

func sameProjectRetirement(left, right model.ProjectRetirement) bool {
	left.RetiredAt = left.RetiredAt.UTC()
	right.RetiredAt = right.RetiredAt.UTC()
	return left == right
}

func sharedProjectRetirementPayload(retirement model.ProjectRetirement) ([]byte, error) {
	if err := model.ValidateProjectRetirement(retirement); err != nil {
		return nil, err
	}
	return json.Marshal(retirement)
}

func equalProjectRetirementPayload(left []byte, retirement model.ProjectRetirement) bool {
	canonical, err := sharedProjectRetirementPayload(retirement)
	return err == nil && bytes.Equal(left, canonical)
}
