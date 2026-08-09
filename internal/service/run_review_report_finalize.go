package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) readFinalReviewReport(ctx context.Context, task model.Task, run model.Run) (model.RunReviewReport, error) {
	data, err := s.Hub.ReadFile(ctx, s.reviewReportPath(run.ProjectID, run.ID))
	if err != nil {
		return model.RunReviewReport{}, err
	}
	report, err := model.ParseRunReviewReport(data)
	if err != nil {
		return model.RunReviewReport{}, err
	}
	if report.TaskID != task.ID || report.RunID != run.ID || report.ProjectID != task.ProjectID || report.TaskSHA256 != task.SHA256 || report.TaskRevision != run.TaskRevision || report.TaskRevisionSHA256 != run.TaskRevisionSHA256 || report.TaskRunNumber != run.TaskRunNumber || report.Branch != run.Branch || report.BaseRevision != run.BaseRevision {
		return model.RunReviewReport{}, fmt.Errorf("delivery review report identity mismatch")
	}
	if report.HubCommit == "" {
		report.HubCommit, _ = s.Hub.LastChange(ctx, s.reviewReportPath(run.ProjectID, run.ID))
	}
	return report, nil
}

func (s *Service) TaskReportRead(ctx context.Context, taskID, runID string) (model.RunReviewReport, error) {
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return model.RunReviewReport{}, err
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return model.RunReviewReport{}, err
	}
	if runID != "" {
		for _, run := range runs {
			if run.ID != runID {
				continue
			}
			if run.TaskID != task.ID {
				return model.RunReviewReport{}, fmt.Errorf("run does not belong to task")
			}
			return s.readFinalReviewReport(ctx, task, run)
		}
		return model.RunReviewReport{}, fmt.Errorf("run not found for task")
	}
	revision := 0
	var revisionSHA string
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		current, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return model.RunReviewReport{}, revisionErr
		}
		revision = current.TaskRevision
		revisionSHA = current.RevisionSHA256
	}
	latest, ok := latestApplicableRunForRevision(runs, task.ID, revision, revisionSHA)
	if !ok {
		return model.RunReviewReport{}, fmt.Errorf("no applicable run for task %s", task.ID)
	}
	report, err := s.readFinalReviewReport(ctx, task, latest)
	if err == nil {
		return report, nil
	}
	if IsNotFound(err) {
		return model.RunReviewReport{}, fmt.Errorf("latest applicable run %s is awaiting Delivery review", latest.ID)
	}
	return model.RunReviewReport{}, err
}

func (s *Service) taskReviewSummaries(ctx context.Context, task model.Task, runs []model.Run) ([]model.RunReviewSummary, error) {
	items := make([]model.RunReviewSummary, 0)
	for _, run := range runs {
		if run.TaskID != task.ID {
			continue
		}
		item := model.RunReviewSummary{RunID: run.ID, AgentStatus: run.Status, DeliveryStatus: "not_available", HistoryOnly: run.Historical}
		if run.Historical {
			item.DeliveryStatus = "history_only"
			items = append(items, item)
			continue
		}
		report, err := s.readFinalReviewReport(ctx, task, run)
		if err == nil {
			item.DeliveryStatus = "finalized"
			item.DeliveryReportID = report.ID
			item.DeliveryOutcome = report.Outcome
			item.ReviewedHead = report.ReviewedHead
			item.NextAction = report.NextAction
			if report.Outcome != model.ReviewOutcomeAccepted {
				item.Blocker = report.Outcome
			}
		} else if IsNotFound(err) {
			if run.Status == "succeeded" {
				item.DeliveryStatus = "awaiting_review"
				item.Blocker = "awaiting_review"
				item.NextAction = "perform_delivery_review"
			}
		} else {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
