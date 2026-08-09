package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) validateCompletedDeliveryProof(ctx context.Context, handoff model.DeliveryHandoff, evidence json.RawMessage) (model.Task, model.Run, model.Report, model.RunReviewReport, error) {
	task, run, err := s.validateHandoffReferences(ctx, handoff.ProjectID, handoff.TaskID, handoff.RunID, handoff.TaskSHA256)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, err
	}
	if run.Historical || operationalActiveRun(run) || run.Status != "succeeded" {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("completed handoff requires a terminal successful operational run")
	}
	agent, err := s.RunReport(ctx, run.ID)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("read immutable Agent report: %w", err)
	}
	if agent.Status != "succeeded" || agent.TaskID != task.ID || agent.RunID != run.ID || agent.ProjectID != task.ProjectID || agent.Repository.Branch != run.Branch || agent.Repository.DiffScope != run.BaseRevision+".."+agent.Repository.Head {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("immutable Agent report does not prove completed work")
	}
	delivery, err := s.readFinalReviewReport(ctx, task, run)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("read immutable Delivery report: %w", err)
	}
	if err := model.ValidateRunReviewReport(delivery); err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("immutable Delivery report is invalid: %w", err)
	}
	if delivery.Outcome != model.ReviewOutcomeAccepted || delivery.ReviewedHead != agent.Repository.Head || delivery.TaskSHA256 != task.SHA256 || delivery.Branch != run.Branch || delivery.BaseRevision != run.BaseRevision {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("immutable Delivery report does not prove accepted reviewed work")
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, err
	}
	switch state.Status {
	case "completed", "merge_ready", "merged":
	default:
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("task state %q does not prove completed work", state.Status)
	}
	var proof map[string]json.RawMessage
	if err := json.Unmarshal(evidence, &proof); err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence proof is invalid")
	}
	if err := model.PlannerReportRequiresTerminalEvidence(model.PlannerReport{ReportType: model.PlannerReportCompleted, TechnicalEvidence: evidence}); err != nil {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, err
	}
	if value, err := evidenceString(proof, "task_sha256"); err != nil || value != task.SHA256 {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence task hash does not match immutable task")
	}
	if value, err := evidenceString(proof, "run_id"); err != nil || value != run.ID {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence run does not match immutable run")
	}
	if value, err := evidenceString(proof, "delivery_report_id"); err != nil || value != delivery.ID {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence Delivery report does not match immutable report")
	}
	if value, err := evidenceString(proof, "reviewed_head"); err != nil || value != delivery.ReviewedHead {
		return model.Task{}, model.Run{}, model.Report{}, model.RunReviewReport{}, fmt.Errorf("technical evidence reviewed head does not match immutable report")
	}
	return task, run, agent, delivery, nil
}
