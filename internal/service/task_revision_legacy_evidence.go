package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

// LegacyTaskRevisionEvidence is the historical TaskRevision wire shape. It is
// separate from model.TaskRevision because deployed revisions include
// source_run_id and source_report_id, and those fields are hash material.
type LegacyTaskRevisionEvidence struct {
	SchemaVersion          int       `json:"schema_version"`
	ID                     string    `json:"id"`
	TaskID                 string    `json:"task_id"`
	TaskRevision           int       `json:"task_revision"`
	RevisionSHA256         string    `json:"revision_sha256"`
	ParentTaskRevision     int       `json:"parent_task_revision,omitempty"`
	ParentTaskSHA256       string    `json:"parent_task_sha256,omitempty"`
	ProjectID              string    `json:"project_id"`
	Title                  string    `json:"title"`
	Objective              string    `json:"objective"`
	Branch                 string    `json:"branch"`
	BaseRevision           string    `json:"base_revision"`
	AcceptanceCriteria     []string  `json:"acceptance_criteria"`
	Constraints            []string  `json:"constraints"`
	RequiredGates          []string  `json:"required_gates,omitempty"`
	WorkflowPolicyRevision int       `json:"workflow_policy_revision"`
	OperationClass         string    `json:"operation_class"`
	EffectiveCIField       string    `json:"effective_ci_field"`
	EffectiveCIMode        string    `json:"effective_ci_mode"`
	WaitForCI              bool      `json:"wait_for_ci"`
	CIBlocking             bool      `json:"ci_blocking"`
	AgentMayWait           bool      `json:"agent_may_wait"`
	Status                 string    `json:"status"`
	SourceRunID            string    `json:"source_run_id,omitempty"`
	SourceReportID         string    `json:"source_report_id,omitempty"`
	CreatedBy              string    `json:"created_by"`
	CreatedAt              time.Time `json:"created_at"`
}

type LegacyTaskRevisionEvidenceListPage struct {
	Revisions  []LegacyTaskRevisionEvidence
	NextCursor string
	HasMore    bool
}

func legacyTaskRevisionFromTask(task model.Task) LegacyTaskRevisionEvidence {
	legacy := model.TaskRevisionFromTask(task)
	return LegacyTaskRevisionEvidence{
		SchemaVersion:          legacy.SchemaVersion,
		ID:                     legacy.ID,
		TaskID:                 legacy.TaskID,
		TaskRevision:           legacy.TaskRevision,
		RevisionSHA256:         legacy.RevisionSHA256,
		ProjectID:              legacy.ProjectID,
		Title:                  legacy.Title,
		Objective:              legacy.Objective,
		Branch:                 legacy.Branch,
		BaseRevision:           legacy.BaseRevision,
		AcceptanceCriteria:     append([]string{}, legacy.AcceptanceCriteria...),
		Constraints:            append([]string{}, legacy.Constraints...),
		RequiredGates:          append([]string{}, legacy.RequiredGates...),
		WorkflowPolicyRevision: legacy.WorkflowPolicyRevision,
		OperationClass:         legacy.OperationClass,
		EffectiveCIField:       legacy.EffectiveCIField,
		EffectiveCIMode:        legacy.EffectiveCIMode,
		WaitForCI:              legacy.WaitForCI,
		CIBlocking:             legacy.CIBlocking,
		AgentMayWait:           legacy.AgentMayWait,
		Status:                 legacy.Status,
		CreatedBy:              legacy.CreatedBy,
		CreatedAt:              legacy.CreatedAt,
	}
}

func (r LegacyTaskRevisionEvidence) validate(project, taskID string) error {
	if r.SchemaVersion != model.TaskRevisionSchemaVersion || r.TaskID != taskID || r.ProjectID != project || r.TaskRevision < 1 {
		return fmt.Errorf("invalid legacy TaskRevision identity or ownership")
	}
	wantID, err := model.FormatTaskRevisionID(r.TaskID, r.TaskRevision)
	if err != nil || r.ID != wantID {
		return fmt.Errorf("invalid legacy TaskRevision id")
	}
	if len(r.RevisionSHA256) != 64 || strings.Trim(r.RevisionSHA256, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid legacy TaskRevision sha256")
	}
	if r.TaskRevision == 1 {
		if r.ParentTaskRevision != 0 || r.ParentTaskSHA256 != "" {
			return fmt.Errorf("legacy TaskRevision 1 cannot have a parent")
		}
	} else {
		if r.ParentTaskRevision != r.TaskRevision-1 || len(r.ParentTaskSHA256) != 64 || strings.Trim(r.ParentTaskSHA256, "0123456789abcdef") != "" {
			return fmt.Errorf("invalid legacy TaskRevision parent")
		}
		hash, hashErr := hashLegacyTaskRevision(r)
		if hashErr != nil || hash != r.RevisionSHA256 {
			return fmt.Errorf("legacy TaskRevision hash mismatch")
		}
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("legacy TaskRevision created_at is required")
	}
	return nil
}

func hashLegacyTaskRevision(revision LegacyTaskRevisionEvidence) (string, error) {
	revision.RevisionSHA256 = ""
	data, err := json.Marshal(revision)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func readLegacyTaskRevisionJSON(raw []byte, out *LegacyTaskRevisionEvidence) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("legacy TaskRevision JSON has trailing content")
	}
	return nil
}

func (s *Service) readLegacyTaskRevisionEvidence(ctx context.Context, project, taskID string, revision int) (LegacyTaskRevisionEvidence, error) {
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return LegacyTaskRevisionEvidence{}, err
	}
	if revision == 1 {
		result := legacyTaskRevisionFromTask(task)
		if err := result.validate(project, taskID); err != nil {
			return LegacyTaskRevisionEvidence{}, err
		}
		return result, nil
	}
	raw, err := s.Hub.ReadFile(ctx, s.taskRevisionPath(project, taskID, revision))
	if err != nil {
		return LegacyTaskRevisionEvidence{}, err
	}
	var result LegacyTaskRevisionEvidence
	if err := readLegacyTaskRevisionJSON(raw, &result); err != nil {
		return LegacyTaskRevisionEvidence{}, err
	}
	if err := result.validate(project, taskID); err != nil {
		return LegacyTaskRevisionEvidence{}, err
	}
	return result, nil
}

func (s *Service) legacyTaskRevisionEvidenceList(ctx context.Context, task model.Task) ([]LegacyTaskRevisionEvidence, error) {
	result := []LegacyTaskRevisionEvidence{legacyTaskRevisionFromTask(task)}
	paths, err := s.Hub.List(ctx, s.taskRevisionPrefix(task.ProjectID, task.ID), ".json")
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		raw, err := s.Hub.ReadFile(ctx, path)
		if err != nil {
			return nil, err
		}
		var item LegacyTaskRevisionEvidence
		if err := readLegacyTaskRevisionJSON(raw, &item); err != nil {
			return nil, err
		}
		if err := item.validate(task.ProjectID, task.ID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TaskRevision < result[j].TaskRevision })
	for i, item := range result {
		if item.TaskRevision != i+1 {
			return nil, fmt.Errorf("legacy TaskRevisions are not contiguous")
		}
		if i > 0 && (item.ParentTaskRevision != result[i-1].TaskRevision || item.ParentTaskSHA256 != result[i-1].RevisionSHA256) {
			return nil, fmt.Errorf("legacy TaskRevision parent binding mismatch")
		}
	}
	return result, nil
}

func (s *Service) TaskRevisionLegacyEvidenceListPage(ctx context.Context, taskID string, in CollectionPageInput) (LegacyTaskRevisionEvidenceListPage, error) {
	limit, err := pagination.Limit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return LegacyTaskRevisionEvidenceListPage{}, err
	}
	if base, _, err := model.ParseTaskRevisionID(taskID); err == nil {
		taskID = base
	}
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return LegacyTaskRevisionEvidenceListPage{}, err
	}
	items, err := s.legacyTaskRevisionEvidenceList(ctx, task)
	if err != nil {
		return LegacyTaskRevisionEvidenceListPage{}, err
	}
	page, info, err := pagination.Page("task_revision_list:"+taskID, items, limit, in.Cursor, func(item LegacyTaskRevisionEvidence) string {
		return fmt.Sprintf("%09d", item.TaskRevision)
	})
	if err != nil {
		return LegacyTaskRevisionEvidenceListPage{}, err
	}
	return LegacyTaskRevisionEvidenceListPage{
		Revisions:  page,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, nil
}

func (s *Service) TaskRevisionLegacyEvidenceRead(ctx context.Context, revisionID string) (LegacyTaskRevisionEvidence, error) {
	taskID, revision, err := model.ParseTaskRevisionID(revisionID)
	if err != nil {
		return LegacyTaskRevisionEvidence{}, err
	}
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return LegacyTaskRevisionEvidence{}, err
	}
	return s.readLegacyTaskRevisionEvidence(ctx, task.ProjectID, taskID, revision)
}
