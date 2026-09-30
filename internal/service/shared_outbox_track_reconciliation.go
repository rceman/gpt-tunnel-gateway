package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const (
	tsk668TrackAcceptOutboxID           = "track-accept-GTW-TRK1-r8"
	tsk668TrackProjectID                = "gpt-tunnel-gateway"
	tsk668TrackID                       = "GTW-TRK1"
	tsk668TrackRevision           int64 = 8
	tsk668LegacyTrackUpdatedAt          = "2026-09-23T18:49:43.099719516Z"
	tsk668CanonicalTrackUpdatedAt       = "2026-09-24T08:31:52.370190213Z"
	tsk668LegacyTrackUpdatedBy          = "HOM_GTW_L_8yzfj"
	tsk668CanonicalTrackUpdatedBy       = "HOM_GTW_P_0vext"
	tsk668LegacyTrackSHA256             = "fe86a4e8e3da83a51202a2cc60b70f5869e4bc627c6d603e36b769eb857f5033"
	tsk668CanonicalTrackSHA256          = "43ecf4af400252617a23cd3032e133ff5ef5cd01bf26bfb77b37cdb4ee527372"
	tsk668TrackMigrationID              = "tsk668-trk1-r8-precanonical-accept-v1"
)

var errTSK668TrackReconciliationNotApplicable = errors.New("TSK668 legacy reconciliation is not applicable")

type trackReconciliationFingerprints struct {
	legacy    string
	canonical string
}

type trackCurrentReconciliationEvidence struct {
	SchemaVersion           int                          `json:"schema_version"`
	MigrationID             string                       `json:"migration_id"`
	ProjectID               string                       `json:"project_id"`
	EntityType              string                       `json:"entity_type"`
	EntityID                string                       `json:"entity_id"`
	Revision                int64                        `json:"revision"`
	OutboxID                string                       `json:"outbox_id"`
	Classification          string                       `json:"classification"`
	ReplacedFields          []string                     `json:"replaced_fields"`
	LegacySemanticSHA256    string                       `json:"legacy_semantic_sha256"`
	CanonicalSemanticSHA256 string                       `json:"canonical_semantic_sha256"`
	LegacyTrack             model.Track                  `json:"legacy_track"`
	SharedRevision          portableSharedRevision       `json:"shared_revision"`
	AcceptanceEvent         portableSharedLifecycleEvent `json:"acceptance_event"`
}

func (s *Service) publishTSK668TrackAcceptOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	return s.publishTSK668TrackAcceptOutboxWithFingerprints(ctx, entry, trackReconciliationFingerprints{
		legacy:    tsk668LegacyTrackSHA256,
		canonical: tsk668CanonicalTrackSHA256,
	})
}

func (s *Service) publishTSK668TrackAcceptOutboxWithFingerprints(ctx context.Context, entry sqlitestore.OutboxEntry, fingerprints trackReconciliationFingerprints) error {
	if entry.ID != tsk668TrackAcceptOutboxID || entry.EntityType != "track" || entry.EntityID != tsk668TrackID || entry.Revision != tsk668TrackRevision || entry.Kind != "track-accept" {
		return errTSK668TrackReconciliationNotApplicable
	}
	if s == nil || s.Durability == nil {
		return fmt.Errorf("Track acceptance reconciliation stores are unavailable")
	}
	sharedEntity, err := s.Durability.ReadSharedEntity(ctx, "track", tsk668TrackID)
	if err != nil {
		return err
	}
	if sharedEntity.ID != tsk668TrackID {
		return fmt.Errorf("Shared Track identity conflicts with the bounded acceptance")
	}
	if sharedEntity.Revision != tsk668TrackRevision || sharedEntity.UpdatedAt != tsk668CanonicalTrackUpdatedAt {
		return errTSK668TrackReconciliationNotApplicable
	}
	var canonicalTrack model.Track
	if err := decodeStrict(sharedEntity.Payload, &canonicalTrack); err != nil {
		return fmt.Errorf("decode authoritative Shared Track: %w", err)
	}
	if err := model.ValidateTrack(canonicalTrack); err != nil {
		return fmt.Errorf("invalid authoritative Shared Track: %w", err)
	}
	if canonicalTrack.ID != tsk668TrackID || canonicalTrack.ProjectID != tsk668TrackProjectID || canonicalTrack.Revision != int(tsk668TrackRevision) || canonicalTrack.Status != model.TrackAccepted || canonicalTrack.UpdatedBy != tsk668CanonicalTrackUpdatedBy || canonicalTrack.UpdatedAt.UTC().Format(time.RFC3339Nano) != tsk668CanonicalTrackUpdatedAt || !sameCanonicalJSON(entry.Payload, sharedEntity.Payload) {
		return fmt.Errorf("Shared Track current state conflicts with the bounded acceptance payload")
	}
	canonicalDigest, err := trackSemanticDigest(canonicalTrack)
	if err != nil {
		return err
	}
	revision, found, err := s.sharedRevisionOutboxRecord(ctx, entry)
	if err != nil {
		return err
	}
	if !found || revision.EntityType != "track" || revision.EntityID != tsk668TrackID || revision.ProjectID != tsk668TrackProjectID || revision.Revision != tsk668TrackRevision || revision.MutationKind != "update" || revision.Actor != tsk668LegacyTrackUpdatedBy || revision.Reason != "submit Track for Planner acceptance" || !reflect.DeepEqual(revision.ChangedFields, []string{"status", "review"}) || revision.RecordedAt != tsk668LegacyTrackUpdatedAt {
		return fmt.Errorf("Shared Track revision history does not prove the bounded pre-accept state")
	}
	var historicalTrack model.Track
	if err := decodeStrict(revision.Payload, &historicalTrack); err != nil {
		return fmt.Errorf("decode Shared Track revision history: %w", err)
	}
	if err := model.ValidateTrack(historicalTrack); err != nil {
		return fmt.Errorf("invalid Shared Track revision history: %w", err)
	}
	if historicalTrack.ID != canonicalTrack.ID || historicalTrack.ProjectID != canonicalTrack.ProjectID || historicalTrack.Revision != canonicalTrack.Revision || historicalTrack.Status != model.TrackReviewPending || historicalTrack.UpdatedBy != tsk668LegacyTrackUpdatedBy || historicalTrack.UpdatedAt.UTC().Format(time.RFC3339Nano) != tsk668LegacyTrackUpdatedAt {
		return fmt.Errorf("Shared Track revision history does not match the bounded pre-publisher representation")
	}
	legacyDigest, err := trackSemanticDigest(historicalTrack)
	if err != nil {
		return err
	}
	legacyFromCanonical := canonicalTrack
	legacyFromCanonical.Status = historicalTrack.Status
	legacyFromCanonical.UpdatedAt = historicalTrack.UpdatedAt
	legacyFromCanonical.UpdatedBy = historicalTrack.UpdatedBy
	if !tracksSemanticallyEqual(legacyFromCanonical, historicalTrack) {
		return fmt.Errorf("Shared Track acceptance changes fields outside the bounded legacy delta")
	}
	lifecycleEvents, err := s.sharedLifecycleOutboxEvents(ctx, entry)
	if err != nil {
		return err
	}
	if len(lifecycleEvents) == 0 {
		return fmt.Errorf("Shared Track acceptance lifecycle evidence is missing")
	}
	acceptanceEvent := lifecycleEvents[len(lifecycleEvents)-1]
	if acceptanceEvent.OperationID != entry.ID || acceptanceEvent.EntityType != "track" || acceptanceEvent.ProjectID != tsk668TrackProjectID || acceptanceEvent.EntityID != tsk668TrackID || acceptanceEvent.Revision != tsk668TrackRevision || acceptanceEvent.EventKind != "status" || acceptanceEvent.MutationKind != "status" || acceptanceEvent.FromStatus != model.TrackReviewPending || acceptanceEvent.ToStatus != model.TrackAccepted || acceptanceEvent.Actor != tsk668CanonicalTrackUpdatedBy || acceptanceEvent.Reason != "accept fresh Track review" || !reflect.DeepEqual(acceptanceEvent.ChangedFields, []string{"status"}) || !sameCanonicalJSON(acceptanceEvent.Contract, []byte(`{"entity":"track"}`)) || acceptanceEvent.RecordedAt != tsk668CanonicalTrackUpdatedAt {
		return fmt.Errorf("Shared Track lifecycle history does not prove the bounded acceptance")
	}
	evidence := trackCurrentReconciliationEvidence{
		SchemaVersion:           1,
		MigrationID:             tsk668TrackMigrationID,
		ProjectID:               tsk668TrackProjectID,
		EntityType:              "track",
		EntityID:                tsk668TrackID,
		Revision:                tsk668TrackRevision,
		OutboxID:                tsk668TrackAcceptOutboxID,
		Classification:          "pre-canonical-publisher review_pending projection replaced by accepted Shared state",
		ReplacedFields:          []string{"status", "updated_at", "updated_by"},
		LegacySemanticSHA256:    legacyDigest,
		CanonicalSemanticSHA256: canonicalDigest,
		LegacyTrack:             historicalTrack,
		SharedRevision:          revision,
		AcceptanceEvent:         acceptanceEvent,
	}
	revisionPath := ""
	if found {
		revisionPath = s.sharedRevisionPath(revision.ProjectID, revision.EntityType, revision.EntityID, revision.Revision)
	}
	trackPath := s.trackPath(tsk668TrackProjectID, tsk668TrackID)
	evidencePath := s.tsk668TrackReconciliationEvidencePath()
	_, err = s.Hub.Transact(ctx, "", "gateway: reconcile pre-canonical GTW-TRK1 revision 8 acceptance", func(worktree string) ([]string, error) {
		changed := make([]string, 0, len(lifecycleEvents)+3)
		if revisionPath != "" {
			path, didChange, writeErr := s.writeSharedRevisionOutbox(worktree, revision)
			if writeErr != nil {
				return nil, writeErr
			}
			if didChange {
				changed = append(changed, path)
			}
		}
		lifecyclePaths, writeErr := s.writeSharedLifecycleOutboxEvents(worktree, lifecycleEvents)
		if writeErr != nil {
			return nil, writeErr
		}
		changed = append(changed, lifecyclePaths...)
		var latest model.Track
		if readErr := readWorktreeJSON(worktree, trackPath, &latest); readErr == nil {
			if err := model.ValidateTrack(latest); err != nil {
				return nil, fmt.Errorf("Hub Track %q is malformed authority: %w", tsk668TrackID, err)
			}
			if latest.ID != canonicalTrack.ID || latest.ProjectID != canonicalTrack.ProjectID {
				return nil, fmt.Errorf("Hub Track identity conflicts at %s", trackPath)
			}
			switch {
			case latest.Revision > canonicalTrack.Revision:
			case latest.Revision < canonicalTrack.Revision:
				if err := hub.WriteJSON(worktree, trackPath, canonicalTrack); err != nil {
					return nil, err
				}
				changed = append(changed, trackPath)
			case tracksSemanticallyEqual(latest, canonicalTrack):
				var existing trackCurrentReconciliationEvidence
				if readErr := readWorktreeJSON(worktree, evidencePath, &existing); readErr == nil && !sameTrackCurrentReconciliationEvidence(existing, evidence) {
					return nil, fmt.Errorf("Hub Track reconciliation evidence conflicts at %s", evidencePath)
				} else if readErr != nil && !IsNotFound(readErr) {
					return nil, readErr
				}
			default:
				hubDigest, digestErr := trackSemanticDigest(latest)
				if digestErr != nil {
					return nil, digestErr
				}
				if hubDigest != fingerprints.legacy || canonicalDigest != fingerprints.canonical || !tracksSemanticallyEqual(latest, historicalTrack) {
					return nil, fmt.Errorf("Hub Track %q conflicts at revision %d (Hub semantic sha256=%s, Shared outbox semantic sha256=%s)", tsk668TrackID, tsk668TrackRevision, hubDigest, canonicalDigest)
				}
				var existing trackCurrentReconciliationEvidence
				if readErr := readWorktreeJSON(worktree, evidencePath, &existing); readErr == nil {
					if !sameTrackCurrentReconciliationEvidence(existing, evidence) {
						return nil, fmt.Errorf("Hub Track reconciliation evidence conflicts at %s", evidencePath)
					}
				} else if !IsNotFound(readErr) {
					return nil, readErr
				} else {
					if err := hub.WriteJSON(worktree, evidencePath, evidence); err != nil {
						return nil, err
					}
					changed = append(changed, evidencePath)
				}
				if err := hub.WriteJSON(worktree, trackPath, canonicalTrack); err != nil {
					return nil, err
				}
				changed = append(changed, trackPath)
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		} else {
			if err := hub.WriteJSON(worktree, trackPath, canonicalTrack); err != nil {
				return nil, err
			}
			changed = append(changed, trackPath)
		}
		if len(changed) == 0 {
			return nil, errSharedOutboxNoop
		}
		return changed, nil
	})
	return err
}

func (s *Service) tsk668TrackReconciliationEvidencePath() string {
	return s.projectPrefix(tsk668TrackProjectID) + "/migration-evidence/track-current/GTW-TRK1-r8.json"
}

func sameTrackCurrentReconciliationEvidence(left, right trackCurrentReconciliationEvidence) bool {
	return left.SchemaVersion == right.SchemaVersion && left.MigrationID == right.MigrationID && left.ProjectID == right.ProjectID && left.EntityType == right.EntityType && left.EntityID == right.EntityID && left.Revision == right.Revision && left.OutboxID == right.OutboxID && left.Classification == right.Classification && reflect.DeepEqual(left.ReplacedFields, right.ReplacedFields) && left.LegacySemanticSHA256 == right.LegacySemanticSHA256 && left.CanonicalSemanticSHA256 == right.CanonicalSemanticSHA256 && tracksSemanticallyEqual(left.LegacyTrack, right.LegacyTrack) && sameSharedRevisionEvidence(left.SharedRevision, right.SharedRevision) && samePortableLifecycleEvent(left.AcceptanceEvent, right.AcceptanceEvent)
}

func sameSharedRevisionEvidence(left, right portableSharedRevision) bool {
	return left.EntityType == right.EntityType && left.EntityID == right.EntityID && left.ProjectID == right.ProjectID && left.Revision == right.Revision && left.MutationKind == right.MutationKind && left.Actor == right.Actor && left.Reason == right.Reason && reflect.DeepEqual(left.ChangedFields, right.ChangedFields) && left.RecordedAt == right.RecordedAt && sameCanonicalJSON(left.Payload, right.Payload)
}
