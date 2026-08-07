package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	planReadDefaultLimit = 10
	planReadMaxLimit     = 10
	statusTokenVersion   = 1
)

type PlanReadInput struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type ProjectStatusInput struct {
	ProjectID string `json:"project_id"`
	Since     string `json:"since,omitempty"`
}

// PlanReadProjection is the bounded plan manifest returned by plan_read.
// Full section bodies remain available only through plan_section_read and
// plan_render.
type PlanReadProjection struct {
	SchemaVersion    int                      `json:"schema_version"`
	ProjectID        string                   `json:"project_id"`
	Revision         int                      `json:"revision"`
	Title            string                   `json:"title"`
	Summary          string                   `json:"summary"`
	CurrentObjective string                   `json:"current_objective"`
	ActiveTaskID     string                   `json:"active_task_id,omitempty"`
	ActiveRunID      string                   `json:"active_run_id,omitempty"`
	Sections         []model.PlanSectionIndex `json:"sections"`
	NextCursor       string                   `json:"next_cursor,omitempty"`
}

type planReadCursor struct {
	Version        int    `json:"version"`
	ProjectID      string `json:"project_id"`
	Revision       int    `json:"revision"`
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
	SectionsSHA256 string `json:"sections_sha256"`
}

func sectionsDigest(sections []model.PlanSectionIndex) string {
	data, _ := json.Marshal(sections)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func encodePlanReadCursor(cursor planReadCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodePlanReadCursor(value string) (planReadCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return planReadCursor{}, fmt.Errorf("invalid or stale plan cursor; request a fresh plan read")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cursor planReadCursor
	if err := decoder.Decode(&cursor); err != nil {
		return planReadCursor{}, fmt.Errorf("invalid or stale plan cursor; request a fresh plan read")
	}
	if decoder.More() || cursor.Version != 1 || cursor.ProjectID == "" || cursor.Revision < 1 || cursor.Limit < 1 || cursor.Limit > planReadMaxLimit || cursor.Offset < 0 || len(cursor.SectionsSHA256) != 64 {
		return planReadCursor{}, fmt.Errorf("invalid or stale plan cursor; request a fresh plan read")
	}
	return cursor, nil
}

// PlanReadPage returns one bounded deterministic page of the current plan's
// section index. The cursor carries all query and snapshot state, so no
// mutable server-side pagination state is required.
func (s *Service) PlanReadPage(ctx context.Context, project string, limit int, cursorValue string) (PlanReadProjection, error) {
	if limit < 0 || limit > planReadMaxLimit {
		return PlanReadProjection{}, fmt.Errorf("plan_read limit must be between 1 and %d", planReadMaxLimit)
	}
	plan, err := s.PlanRead(ctx, project)
	if err != nil {
		return PlanReadProjection{}, err
	}
	sectionsSHA256 := sectionsDigest(plan.Sections)
	offset := 0
	if cursorValue != "" {
		cursor, decodeErr := decodePlanReadCursor(cursorValue)
		if decodeErr != nil || cursor.ProjectID != project || cursor.Revision != plan.Revision || cursor.SectionsSHA256 != sectionsSHA256 {
			return PlanReadProjection{}, fmt.Errorf("invalid or stale plan cursor; request a fresh plan read")
		}
		if limit != 0 && limit != cursor.Limit {
			return PlanReadProjection{}, fmt.Errorf("invalid plan cursor query; request a fresh plan read with the same limit")
		}
		limit = cursor.Limit
		offset = cursor.Offset
	} else if limit == 0 {
		limit = planReadDefaultLimit
	}
	if offset > len(plan.Sections) {
		return PlanReadProjection{}, fmt.Errorf("invalid or stale plan cursor; request a fresh plan read")
	}
	end := offset + limit
	if end > len(plan.Sections) {
		end = len(plan.Sections)
	}
	projection := PlanReadProjection{
		SchemaVersion: plan.SchemaVersion, ProjectID: plan.ProjectID, Revision: plan.Revision,
		Title: plan.Title, Summary: plan.Summary, CurrentObjective: plan.CurrentObjective,
		ActiveTaskID: plan.ActiveTaskID, ActiveRunID: plan.ActiveRunID,
		Sections: append([]model.PlanSectionIndex(nil), plan.Sections[offset:end]...),
	}
	if end < len(plan.Sections) {
		projection.NextCursor, err = encodePlanReadCursor(planReadCursor{Version: 1, ProjectID: project, Revision: plan.Revision, Limit: limit, Offset: end, SectionsSHA256: sectionsSHA256})
		if err != nil {
			return PlanReadProjection{}, err
		}
	}
	return projection, nil
}

type ProjectStatusDelta struct {
	ProjectID         string         `json:"project_id"`
	Changed           bool           `json:"changed"`
	StatusToken       string         `json:"status_token"`
	ChangedComponents []string       `json:"changed_components,omitempty"`
	Changes           map[string]any `json:"changes,omitempty"`
}

type statusTokenPayload struct {
	Version    int               `json:"version"`
	ProjectID  string            `json:"project_id"`
	Components map[string]string `json:"components"`
}

func statusComponentDigest(value any) string {
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func projectStatusComponents(status ProjectStatus) map[string]string {
	progress := status.Progress
	// This value is derived from the clock and must not make an unchanged
	// status appear changed on every read.
	progress.LastMeaningfulActivityAgeSeconds = 0
	return map[string]string{
		"project":         statusComponentDigest(status.Project),
		"local":           statusComponentDigest(status.Local),
		"worktree":        statusComponentDigest(status.Worktree),
		"plan":            statusComponentDigest(status.Plan),
		"hub_revision":    statusComponentDigest(status.HubRevision),
		"progress":        statusComponentDigest(progress),
		"workflow_policy": statusComponentDigest(status.WorkflowPolicy),
	}
}

func encodeStatusToken(payload statusTokenPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeStatusToken(value string) (statusTokenPayload, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return statusTokenPayload{}, fmt.Errorf("invalid or incompatible status_token; request a fresh project_status baseline")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload statusTokenPayload
	if err := decoder.Decode(&payload); err != nil || payload.Version != statusTokenVersion || payload.ProjectID == "" || len(payload.Components) != 7 {
		return statusTokenPayload{}, fmt.Errorf("invalid or incompatible status_token; request a fresh project_status baseline")
	}
	for _, name := range []string{"project", "local", "worktree", "plan", "hub_revision", "progress", "workflow_policy"} {
		if len(payload.Components[name]) != 64 {
			return statusTokenPayload{}, fmt.Errorf("invalid or incompatible status_token; request a fresh project_status baseline")
		}
	}
	return payload, nil
}

func (s *Service) ProjectStatusRead(ctx context.Context, project, since string) (any, error) {
	status, err := s.ProjectStatus(ctx, project)
	if err != nil {
		return nil, err
	}
	components := projectStatusComponents(status)
	token, err := encodeStatusToken(statusTokenPayload{Version: statusTokenVersion, ProjectID: project, Components: components})
	if err != nil {
		return nil, err
	}
	status.StatusToken = token
	if since == "" {
		return status, nil
	}
	previous, err := decodeStatusToken(since)
	if err != nil || previous.ProjectID != project {
		return nil, fmt.Errorf("invalid or incompatible status_token; request a fresh project_status baseline")
	}
	changed := make([]string, 0, len(components))
	for _, name := range []string{"project", "local", "worktree", "plan", "hub_revision", "progress", "workflow_policy"} {
		if previous.Components[name] != components[name] {
			changed = append(changed, name)
		}
	}
	if len(changed) == 0 {
		return ProjectStatusDelta{ProjectID: project, Changed: false, StatusToken: token}, nil
	}
	changes := make(map[string]any, len(changed))
	for _, name := range changed {
		switch name {
		case "project":
			changes[name] = status.Project
		case "local":
			changes[name] = status.Local
		case "worktree":
			changes[name] = status.Worktree
		case "plan":
			changes[name] = status.Plan
		case "hub_revision":
			changes[name] = status.HubRevision
		case "progress":
			changes[name] = status.Progress
		case "workflow_policy":
			changes[name] = status.WorkflowPolicy
		}
	}
	return ProjectStatusDelta{ProjectID: project, Changed: true, StatusToken: token, ChangedComponents: changed, Changes: changes}, nil
}
