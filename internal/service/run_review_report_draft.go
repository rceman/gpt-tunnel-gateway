package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskReviewReportStart(ctx context.Context, taskID, runID string) (model.RunReviewReportDraft, error) {
	lock, err := s.reviewReportLock(runID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, taskID, runID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	if exists, err := s.reviewReportExists(ctx, context.task.ProjectID, runID); err != nil {
		return model.RunReviewReportDraft{}, err
	} else if exists {
		return model.RunReviewReportDraft{}, fmt.Errorf("delivery review report already finalized")
	}
	if draft, err := s.readReviewDraft(runID); err == nil {
		if draft.TaskID != taskID || draft.RunID != runID || draft.ProjectID != context.task.ProjectID {
			return model.RunReviewReportDraft{}, fmt.Errorf("review draft identity mismatch")
		}
		s.reviewMachineDraft(context, &draft)
		if err := s.writeReviewDraft(draft); err != nil {
			return model.RunReviewReportDraft{}, err
		}
		return draft, nil
	} else if !os.IsNotExist(err) {
		return model.RunReviewReportDraft{}, err
	}
	draft := model.RunReviewReportDraft{DraftRevision: 1, UpdatedAt: time.Now().UTC(), CompletedSections: []string{"repository_state", "gates", "changed_files"}}
	s.reviewMachineDraft(context, &draft)
	if err := s.writeReviewDraft(draft); err != nil {
		return model.RunReviewReportDraft{}, err
	}
	return draft, nil
}

func decodeReviewPayload(data []byte, out any) error {
	if len(data) == 0 {
		return fmt.Errorf("review section payload is required")
	}
	return decodeStrict(data, out)
}

func (s *Service) TaskReviewReportSectionUpdate(ctx context.Context, in TaskReviewReportSectionUpdateInput) (model.RunReviewReportDraft, error) {
	lock, err := s.reviewReportLock(in.RunID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, in.TaskID, in.RunID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	draft, err := s.readReviewDraft(in.RunID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	if draft.TaskID != in.TaskID || draft.RunID != in.RunID || draft.ProjectID != context.task.ProjectID {
		return model.RunReviewReportDraft{}, fmt.Errorf("review draft identity mismatch")
	}
	if in.ExpectedDraftRevision != draft.DraftRevision {
		return model.RunReviewReportDraft{}, fmt.Errorf("DRAFT_REVISION_CONFLICT expected=%d actual=%d", in.ExpectedDraftRevision, draft.DraftRevision)
	}
	switch in.SectionID {
	case "repository_state", "gates", "changed_files":
		return model.RunReviewReportDraft{}, fmt.Errorf("machine review section %q cannot be edited", in.SectionID)
	case "outcome":
		if err := decodeReviewPayload(in.Payload, &draft.Outcome); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "findings":
		if err := decodeReviewPayload(in.Payload, &draft.Findings); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "scope_coverage":
		if err := decodeReviewPayload(in.Payload, &draft.ScopeCoverage); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "unexpected_surfaces":
		if err := decodeReviewPayload(in.Payload, &draft.UnexpectedSurfaces); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "historical_compatibility":
		if err := decodeReviewPayload(in.Payload, &draft.HistoricalCompatibility); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "prohibited_actions":
		if err := decodeReviewPayload(in.Payload, &draft.ProhibitedActions); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "next_action":
		if err := decodeReviewPayload(in.Payload, &draft.NextAction); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	default:
		return model.RunReviewReportDraft{}, fmt.Errorf("unknown review section %q", in.SectionID)
	}
	s.reviewMachineDraft(context, &draft)
	draft.CompletedSections = addReviewSection(draft.CompletedSections, in.SectionID)
	draft.DraftRevision++
	draft.UpdatedAt = time.Now().UTC()
	if err := s.writeReviewDraft(draft); err != nil {
		return model.RunReviewReportDraft{}, err
	}
	return draft, nil
}

func reviewDraftMissingSections(draft model.RunReviewReportDraft) []string {
	missing := []string{}
	for _, section := range model.RunReviewReportSections {
		if !reviewSectionSeen(draft.CompletedSections, section) {
			missing = append(missing, section)
		}
	}
	if draft.Outcome == "" {
		missing = append(missing, "outcome_value")
	}
	if strings.TrimSpace(draft.NextAction) == "" {
		missing = append(missing, "next_action_value")
	}
	return missing
}

func (s *Service) TaskReviewReportValidate(ctx context.Context, taskID, runID string) (model.RunReviewValidation, error) {
	lock, err := s.reviewReportLock(runID)
	if err != nil {
		return model.RunReviewValidation{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, taskID, runID)
	if err != nil {
		return model.RunReviewValidation{}, err
	}
	draft, err := s.readReviewDraft(runID)
	if err != nil {
		return model.RunReviewValidation{}, err
	}
	s.reviewMachineDraft(context, &draft)
	result := model.RunReviewValidation{Valid: true, Errors: []string{}, Draft: draft}
	if err := model.ValidateRunReviewReportDraft(draft); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, err.Error())
	}
	result.Errors = append(result.Errors, reviewDraftMissingSections(draft)...)
	if len(result.Errors) > 0 {
		result.Valid = false
	}
	if err := s.writeReviewDraft(draft); err != nil {
		return model.RunReviewValidation{}, err
	}
	return result, nil
}
