package service

import (
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

type DeliveryHandoffCreateInput struct {
	ProjectID            string             `json:"project_id"`
	TaskID               string             `json:"task_id"`
	RunID                string             `json:"run_id"`
	TaskSHA256           string             `json:"task_sha256"`
	OwnerSummary         model.OwnerSummary `json:"owner_summary"`
	TechnicalEvidence    json.RawMessage    `json:"technical_evidence"`
	PlanRevision         int                `json:"plan_revision"`
	HubRevision          string             `json:"hub_revision"`
	TaskRefs             []model.TaskRef    `json:"task_refs"`
	TrainRefs            []string           `json:"train_refs"`
	PlanSectionRefs      []string           `json:"plan_section_refs"`
	OperatorEventRefs    []string           `json:"operator_event_refs"`
	ExpectedRepoBase     string             `json:"expected_repo_base"`
	ExpectedRepoHead     string             `json:"expected_repo_head"`
	FirstAction          string             `json:"first_action"`
	StopBoundary         string             `json:"stop_boundary"`
	ProhibitedOperations []string           `json:"prohibited_operations"`
	InstructionBody      string             `json:"instruction_body"`
	RoleRefs             []string           `json:"role_refs"`
	DelegationRefs       []string           `json:"delegation_refs"`
	CreatedBy            string             `json:"created_by"`
	SupersedesID         string             `json:"supersedes_handoff_id,omitempty"`
	WriteOptions
}

type DeliveryHandoffAcknowledgeInput struct {
	HandoffID      string `json:"handoff_id"`
	AcknowledgedBy string `json:"acknowledged_by"`
	WriteOptions
}

type DeliveryHandoffNextInput struct {
	HandoffID string `json:"handoff_id"`
	NextBy    string `json:"next_by"`
	WriteOptions
}

type DeliveryHandoffCancelInput struct {
	HandoffID   string `json:"handoff_id"`
	CancelledBy string `json:"cancelled_by"`
	Reason      string `json:"reason"`
	WriteOptions
}

type DeliveryHandoffSupersedeInput struct {
	HandoffID            string             `json:"handoff_id"`
	OwnerSummary         model.OwnerSummary `json:"owner_summary"`
	TechnicalEvidence    json.RawMessage    `json:"technical_evidence"`
	PlanRevision         int                `json:"plan_revision"`
	HubRevision          string             `json:"hub_revision"`
	TaskRefs             []model.TaskRef    `json:"task_refs"`
	TrainRefs            []string           `json:"train_refs"`
	PlanSectionRefs      []string           `json:"plan_section_refs"`
	OperatorEventRefs    []string           `json:"operator_event_refs"`
	ExpectedRepoBase     string             `json:"expected_repo_base"`
	ExpectedRepoHead     string             `json:"expected_repo_head"`
	FirstAction          string             `json:"first_action"`
	StopBoundary         string             `json:"stop_boundary"`
	ProhibitedOperations []string           `json:"prohibited_operations"`
	InstructionBody      string             `json:"instruction_body"`
	RoleRefs             []string           `json:"role_refs"`
	DelegationRefs       []string           `json:"delegation_refs"`
	CreatedBy            string             `json:"created_by"`
	WriteOptions
}

type PlannerReportPublishInput struct {
	HandoffID string              `json:"handoff_id"`
	Report    model.PlannerReport `json:"report"`
	WriteOptions
}

type PlannerReportAcknowledgeInput struct {
	ReportID       string `json:"report_id"`
	AcknowledgedBy string `json:"acknowledged_by"`
	WriteOptions
}

type PlannerReportNextInput struct {
	ReportID   string `json:"report_id"`
	ResolvedBy string `json:"resolved_by"`
	WriteOptions
}

const DefaultDurableHandoffListLimit = 20

type DeliveryHandoffListInput struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type PlannerReportListInput struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type DeliveryHandoffListPageResult struct {
	Handoffs   []model.DeliveryHandoffStatus `json:"handoffs"`
	NextCursor string                        `json:"next_cursor"`
	HasMore    bool                          `json:"has_more"`
}

type PlannerReportListPageResult struct {
	Reports    []model.PlannerReportStatus `json:"reports"`
	NextCursor string                      `json:"next_cursor"`
	HasMore    bool                        `json:"has_more"`
}

func boundedDurableListLimit(limit, max int) (int, error) {
	if max < 1 {
		return 0, fmt.Errorf("configured max list items is invalid")
	}
	if limit == 0 {
		limit = DefaultDurableHandoffListLimit
		if limit > max {
			limit = max
		}
	}
	if limit < 0 || limit > max {
		return 0, fmt.Errorf("list limit must be between 1 and %d", max)
	}
	return limit, nil
}

func publicDurableListLimit(limit, max int) (int, error) {
	return pagination.Limit(limit, max)
}

func (s *Service) deliveryHandoffPrefix(project string) string {
	if model.ValidateProjectIdentifier(project) != nil {
		return "../invalid-delivery-handoff"
	}
	return s.projectPrefix(project) + "/delivery-handoffs"
}

func (s *Service) deliveryHandoffPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-delivery-handoff"
	}
	return s.deliveryHandoffPrefix(project) + "/" + id + ".json"
}

func (s *Service) plannerReportPrefix(project string) string {
	if model.ValidateProjectIdentifier(project) != nil {
		return "../invalid-planner-report"
	}
	return s.projectPrefix(project) + "/planner-reports"
}

func (s *Service) plannerReportPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-planner-report"
	}
	return s.plannerReportPrefix(project) + "/" + id + ".json"
}

func (s *Service) plannerReportStatePath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-planner-report-state"
	}
	return s.plannerReportPrefix(project) + "/" + id + ".state.json"
}

func newDurableRecordID() (string, error) {
	id, err := model.NewID()
	if err != nil {
		return "", err
	}
	if err := model.ValidateObjectIdentifier(id); err != nil {
		return "", err
	}
	return id, nil
}

func validateHandoffSummaryAndEvidence(summary model.OwnerSummary, evidence json.RawMessage) error {
	if err := model.ValidateOwnerSummary(summary); err != nil {
		return err
	}
	return model.ValidateTechnicalEvidence(evidence)
}
