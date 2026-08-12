package service

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

type PlanUpdateInput struct {
	ProjectID        string    `json:"project_id"`
	Title            *string   `json:"title,omitempty"`
	Summary          *string   `json:"summary,omitempty"`
	CurrentObjective *string   `json:"current_objective,omitempty"`
	Queue            *[]string `json:"queue,omitempty"`
	ActiveTaskID     *string   `json:"active_task_id,omitempty"`
	ActiveRunID      *string   `json:"active_run_id,omitempty"`
	UpdatedBy        string    `json:"updated_by"`
	WriteOptions
}

type PlanSectionCreateInput struct {
	ProjectID        string `json:"project_id"`
	SectionID        string `json:"section_id"`
	Title            string `json:"title"`
	ShortDescription string `json:"short_description"`
	Description      string `json:"description"`
	UpdatedBy        string `json:"updated_by"`
	WriteOptions
}

type PlanSectionUpdateInput struct {
	ProjectID               string  `json:"project_id"`
	SectionID               string  `json:"section_id"`
	Title                   *string `json:"title,omitempty"`
	ShortDescription        *string `json:"short_description,omitempty"`
	Description             *string `json:"description,omitempty"`
	UpdatedBy               string  `json:"updated_by"`
	ExpectedSectionRevision int     `json:"expected_section_revision"`
	WriteOptions
}

type PlanSectionDeleteInput struct {
	ProjectID               string `json:"project_id"`
	SectionID               string `json:"section_id"`
	UpdatedBy               string `json:"updated_by"`
	ExpectedSectionRevision int    `json:"expected_section_revision"`
	WriteOptions
}

type ADRCreateInput struct {
	ADR model.ADR `json:"adr"`
	WriteOptions
}

type TaskCreateInput struct {
	ProjectID          string   `json:"project_id"`
	Slug               string   `json:"slug"`
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	RequiredGates      []string `json:"required_gates,omitempty"`
	OperationClass     string   `json:"operation_class"`
	CreatedBy          string   `json:"created_by"`
	Supersedes         string   `json:"supersedes,omitempty"`
	WriteOptions
}

type TaskAuthoringCreateInput struct {
	ProjectID             string            `json:"project_id"`
	Title                 string            `json:"title"`
	Objective             string            `json:"objective"`
	AcceptanceCriteria    []string          `json:"acceptance_criteria"`
	Constraints           []string          `json:"constraints"`
	Priority              string            `json:"priority,omitempty"`
	Dependencies          []string          `json:"dependencies,omitempty"`
	PreparationReferences []string          `json:"preparation_references,omitempty"`
	Metadata              map[string]string `json:"metadata,omitempty"`
	ADRRelation           string            `json:"adr_relation"`
	ADRReferences         []string          `json:"adr_references,omitempty"`
	CreatedBy             string            `json:"created_by"`
	WriteOptions
}

type TaskAuthoringUpdateInput struct {
	ProjectID              string             `json:"project_id"`
	TaskID                 string             `json:"task_id"`
	ExpectedRevision       int                `json:"expected_revision"`
	ExpectedRevisionSHA256 string             `json:"expected_revision_sha256,omitempty"`
	Title                  *string            `json:"title,omitempty"`
	Objective              *string            `json:"objective,omitempty"`
	AcceptanceCriteria     *[]string          `json:"acceptance_criteria,omitempty"`
	Constraints            *[]string          `json:"constraints,omitempty"`
	Priority               *string            `json:"priority,omitempty"`
	Dependencies           *[]string          `json:"dependencies,omitempty"`
	PreparationReferences  *[]string          `json:"preparation_references,omitempty"`
	Metadata               *map[string]string `json:"metadata,omitempty"`
	ADRRelation            *string            `json:"adr_relation,omitempty"`
	ADRReferences          *[]string          `json:"adr_references,omitempty"`
	UpdatedBy              string             `json:"updated_by"`
	WriteOptions
}

type TaskAuthoringReadyInput struct {
	ProjectID              string `json:"project_id"`
	TaskID                 string `json:"task_id"`
	ExpectedRevision       int    `json:"expected_revision"`
	ExpectedRevisionSHA256 string `json:"expected_revision_sha256,omitempty"`
	ReadyBy                string `json:"ready_by"`
	WriteOptions
}

type TaskAuthoringListInput struct {
	ProjectID string `json:"project_id"`
	Status    string `json:"status,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type TaskAuthoringListResult struct {
	Tasks []model.TaskAuthoring `json:"tasks"`
}
