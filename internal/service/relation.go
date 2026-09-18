package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// Canonical relation authority.
//
// Inventory and classification of the pre-existing relation-like surfaces
// (owner contract GTW-PLANNER-20260918-1123-TSK511-RELATION-PACKAGE-V1,
// GTW-ADR104 rev2):
//
//   - Task.adr_relation / Task.adr_references: Task-owned descriptive scalar
//     and selector list. Classified as a legacy reference projection, not
//     relation authority; preserved unchanged in v1.
//   - Task.dependencies / Task.preparation_references: Task-owned scalars.
//     Explicitly out of scope; not migrated in v1.
//   - ADR.supersedes / ADR.replaced_by and the legacy ADR supersession bridge:
//     ADR status/lifecycle semantics plus a read-only historical bridge. Not
//     relation authority; the `supersedes` relation does not replace or alter
//     ADR status semantics.
//   - Operator journal `supersedes_event_id`: correction-journal metadata owned
//     by the operator journal lifecycle, not a canonical entity relation.
//   - Selectors (Task scope, adr_relation, dependencies): scalar identifiers
//     remain scalar; key-to-title maps are output only.
//   - PMT (Local-only prompt family, GTW-ADR122): relation rows for a PMT
//     source are persisted in the Local store only and are never Shared/Hub
//     replicated. No PMT body/runtime state is added here.
//
// The canonical authority stores one directed row per fact and resolves target
// titles live at read time; titles are never copied into relation state.

type RelationCreateInput struct {
	ProjectID string
	Source    string
	Kind      string
	Target    string
	CreatedBy string
}

type RelationCreateResult struct {
	Source  string
	Kind    string
	Target  string
	Created bool
}

type RelationListInput struct {
	ProjectID string
	Source    string
	Kind      string
	Direction string
	Cursor    string
	Limit     int
}

type RelationListResult struct {
	Source     string
	Relations  map[string]map[string]string
	HasMore    bool
	NextCursor string
}

var relationFamilyEntityType = map[string]string{
	model.RelationFamilyTask: "task",
	model.RelationFamilyADR:  "adr",
	model.RelationFamilyRule: "rule",
}

func relationSourceIsLocal(family string) bool {
	return family == model.RelationFamilyPMT
}

// relationKindsForSource returns the closed kinds that can legally carry the
// family at the requested endpoint.
func relationKindsForSource(family, direction string) []string {
	kinds := make([]string, 0, len(model.RelationKinds()))
	for _, kind := range model.RelationKinds() {
		outgoing, incoming := relationKindFamilies(kind)
		if (direction == sqlitestore.RelationDirectionOutgoing || direction == sqlitestore.RelationDirectionBoth) && containsRelationFamilyValue(outgoing, family) {
			kinds = append(kinds, kind)
			continue
		}
		if (direction == sqlitestore.RelationDirectionIncoming || direction == sqlitestore.RelationDirectionBoth) && containsRelationFamilyValue(incoming, family) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func relationKindFamilies(kind string) ([]string, []string) {
	switch kind {
	case model.RelationKindAuthority:
		return []string{model.RelationFamilyTask, model.RelationFamilyADR, model.RelationFamilyRule}, []string{model.RelationFamilyADR, model.RelationFamilyRule}
	case model.RelationKindCorrects:
		return []string{model.RelationFamilyTask}, []string{model.RelationFamilyTask}
	case model.RelationKindSupersedes:
		return []string{model.RelationFamilyADR}, []string{model.RelationFamilyADR}
	case model.RelationKindConcerns:
		return []string{model.RelationFamilyPMT}, []string{model.RelationFamilyTask, model.RelationFamilyADR, model.RelationFamilyRule}
	default:
		return nil, nil
	}
}

func containsRelationFamilyValue(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// relationLiveTitle resolves the current authoritative title of a canonical
// relation endpoint. Relation state never stores a copy of this value, and an
// unresolvable endpoint is an integrity failure.
func (s *Service) relationLiveTitle(ctx context.Context, projectID, id string) (string, error) {
	family, err := model.RelationFamilyOf(id)
	if err != nil {
		return "", err
	}
	entityType, ok := relationFamilyEntityType[family]
	if !ok {
		return "", fmt.Errorf("relation endpoint %s has no live title authority", id)
	}
	titleField, err := model.RelationTitleField(family)
	if err != nil {
		return "", err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, entityType, id)
	if err != nil {
		return "", fmt.Errorf("relation target %s is unresolved: %w", id, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(entity.Payload, &payload); err != nil {
		return "", fmt.Errorf("relation target %s is unresolved: %w", id, err)
	}
	if payloadProject, _ := payload["project_id"].(string); payloadProject != projectID {
		return "", fmt.Errorf("relation target %s is outside the bound project", id)
	}
	title, _ := payload[titleField].(string)
	if strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("relation target %s has no live title", id)
	}
	return title, nil
}

func (s *Service) RelationCreate(ctx context.Context, in RelationCreateInput) (RelationCreateResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return RelationCreateResult{}, err
	}
	sourceFamily, _, err := model.ValidateRelationEndpoints(in.Kind, in.Source, in.Target)
	if err != nil {
		return RelationCreateResult{}, err
	}
	if _, err := s.relationLiveTitle(ctx, in.ProjectID, in.Target); err != nil {
		return RelationCreateResult{}, err
	}
	if !relationSourceIsLocal(sourceFamily) {
		if _, err := s.relationLiveTitle(ctx, in.ProjectID, in.Source); err != nil {
			return RelationCreateResult{}, fmt.Errorf("relation source: %w", err)
		}
	}
	actor := firstNonEmpty(in.CreatedBy, "server")
	created, err := s.Durability.CreateRelation(ctx, relationSourceIsLocal(sourceFamily), in.ProjectID, in.Kind, in.Source, in.Target, actor, time.Now().UTC())
	if err != nil {
		return RelationCreateResult{}, err
	}
	return RelationCreateResult{
		Source:  in.Source,
		Kind:    in.Kind,
		Target:  in.Target,
		Created: created,
	}, nil
}

func (s *Service) RelationList(ctx context.Context, in RelationListInput) (RelationListResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return RelationListResult{}, err
	}
	direction := in.Direction
	if direction == "" {
		direction = sqlitestore.RelationDirectionOutgoing
	}
	if direction != sqlitestore.RelationDirectionOutgoing && direction != sqlitestore.RelationDirectionIncoming && direction != sqlitestore.RelationDirectionBoth {
		return RelationListResult{}, fmt.Errorf("invalid relation direction %q", direction)
	}
	sourceFamily, err := model.RelationFamilyOf(in.Source)
	if err != nil {
		return RelationListResult{}, err
	}
	if !relationSourceIsLocal(sourceFamily) {
		if _, err := s.relationLiveTitle(ctx, in.ProjectID, in.Source); err != nil {
			return RelationListResult{}, fmt.Errorf("relation source: %w", err)
		}
	}
	kinds := relationKindsForSource(sourceFamily, direction)
	if in.Kind != "" {
		if err := model.ValidateRelationKind(in.Kind); err != nil {
			return RelationListResult{}, err
		}
		if !containsRelationFamilyValue(kinds, in.Kind) {
			return RelationListResult{}, fmt.Errorf("relation kind %q is not available for a %s source in %s direction", in.Kind, sourceFamily, direction)
		}
		kinds = []string{in.Kind}
	}
	limit := in.Limit
	if limit < 1 || limit > sqlitestore.RelationListMaxRows {
		limit = DefaultPublicCollectionLimit
	}
	scope := "relation-list:" + in.ProjectID + ":" + in.Source + ":" + in.Kind + ":" + direction
	afterKind, afterOther, afterDirection := "", "", ""
	if in.Cursor != "" {
		decoded, err := pagination.DecodeOpaqueKeyset(in.Cursor, scope)
		if err != nil {
			return RelationListResult{}, err
		}
		parts := strings.Split(decoded, "\x00")
		if len(parts) != 3 {
			return RelationListResult{}, fmt.Errorf("invalid relation cursor")
		}
		afterKind, afterOther, afterDirection = parts[0], parts[1], parts[2]
	}
	result := RelationListResult{
		Source:    in.Source,
		Relations: map[string]map[string]string{},
	}
	if len(kinds) == 0 {
		return result, nil
	}
	rows, err := s.Durability.ListRelationPage(ctx, relationSourceIsLocal(sourceFamily), in.ProjectID, in.Source, kinds, direction, afterKind, afterOther, afterDirection, limit)
	if err != nil {
		return RelationListResult{}, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		result.HasMore = true
		last := rows[len(rows)-1]
		result.NextCursor = pagination.EncodeOpaqueKeyset(scope, last.Kind+"\x00"+last.Other+"\x00"+last.Direction)
	}
	for _, row := range rows {
		title, err := s.relationLiveTitle(ctx, in.ProjectID, row.Other)
		if err != nil {
			return RelationListResult{}, err
		}
		bucket, ok := result.Relations[row.Kind]
		if !ok {
			bucket = map[string]string{}
			result.Relations[row.Kind] = bucket
		}
		bucket[row.Other] = title
	}
	return result, nil
}

// relationCreateSugar validates one optional relation_type/relation_target
// pair for a canonical entity create. Both must be supplied together or
// omitted together. When supplied, the returned builder commits the relation
// in the same Shared.Batch as the entity create, so both succeed or neither
// does.
func (s *Service) relationCreateSugar(ctx context.Context, projectID, sourceFamily, relationType, relationTarget, createdBy string) (func(entityID string) ([]upstream.Statement, error), error) {
	if (relationType == "") != (relationTarget == "") {
		return nil, fmt.Errorf("relation_type and relation_target must be supplied together or omitted together")
	}
	if relationType == "" {
		return nil, nil
	}
	targetFamily, err := model.RelationFamilyOf(relationTarget)
	if err != nil {
		return nil, err
	}
	if err := model.ValidateRelationKindFamilies(relationType, sourceFamily, targetFamily); err != nil {
		return nil, err
	}
	if _, err := s.relationLiveTitle(ctx, projectID, relationTarget); err != nil {
		return nil, err
	}
	actor := firstNonEmpty(createdBy, "server")
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	local := relationSourceIsLocal(sourceFamily)
	return func(entityID string) ([]upstream.Statement, error) {
		return []upstream.Statement{sqlitestore.RelationInsertStatement(local, projectID, relationType, entityID, relationTarget, createdAt, actor)}, nil
	}, nil
}

// RelationProjection returns the grouped outgoing relation projection for one
// canonical entity read. Titles are resolved live; a target that cannot be
// resolved is an integrity failure rather than an empty or cached label.
func (s *Service) RelationProjection(ctx context.Context, projectID, entityID string) (map[string]map[string]string, error) {
	family, err := model.RelationFamilyOf(entityID)
	if err != nil {
		return nil, err
	}
	if relationSourceIsLocal(family) {
		return nil, fmt.Errorf("relation projection is unavailable for a %s entity", family)
	}
	result, err := s.RelationList(ctx, RelationListInput{
		ProjectID: projectID,
		Source:    entityID,
		Direction: sqlitestore.RelationDirectionOutgoing,
		Limit:     sqlitestore.RelationListMaxRows,
	})
	if err != nil {
		return nil, err
	}
	if result.HasMore {
		return nil, fmt.Errorf("relation projection for %s exceeds the bounded relation limit", entityID)
	}
	return result.Relations, nil
}
