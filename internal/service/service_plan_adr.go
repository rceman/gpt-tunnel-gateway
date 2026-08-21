package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func (s *Service) PlanSectionDelete(ctx context.Context, in PlanSectionDeleteInput) (OperationResult, error) {
	if err := rejectPlanMutationAfterTrainV2(ctx, s, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ExpectedSectionRevision < 1 {
		return OperationResult{}, fmt.Errorf("expected section revision is required")
	}
	if _, err := s.PlanSectionRead(ctx, in.ProjectID, in.SectionID); err != nil {
		return OperationResult{}, err
	}
	expectedHubRevision, err := s.sectionWriteExpectedRevision(ctx, in.ExpectedHubRevision)
	if err != nil {
		return OperationResult{}, err
	}
	now := time.Now().UTC()
	tx, err := s.transactSectionWrite(ctx, expectedHubRevision, "gateway: delete plan section "+in.SectionID, func(w string) ([]string, error) {
		var currentPlan model.Plan
		if err := readWorktreeJSON(w, s.planPath(in.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		index, section, err := sectionIndex(currentPlan, in.SectionID)
		if err != nil {
			return nil, err
		}
		var currentSection model.PlanSection
		sectionPath := s.planSectionPath(in.ProjectID, in.SectionID)
		if err := readWorktreeJSON(w, sectionPath, &currentSection); err != nil {
			return nil, err
		}
		if currentSection.Revision != in.ExpectedSectionRevision || section.Revision != in.ExpectedSectionRevision {
			return nil, fmt.Errorf("SECTION_REVISION_CONFLICT expected=%d actual=%d", in.ExpectedSectionRevision, currentSection.Revision)
		}
		if err := os.Remove(filepath.Join(w, filepath.FromSlash(sectionPath))); err != nil {
			return nil, err
		}
		currentPlan.Sections = append(currentPlan.Sections[:index], currentPlan.Sections[index+1:]...)
		currentPlan.Revision++
		currentPlan.UpdatedBy, currentPlan.UpdatedAt = in.UpdatedBy, now
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.planPath(in.ProjectID), currentPlan); err != nil {
			return nil, err
		}
		return []string{sectionPath, s.planPath(in.ProjectID)}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "deleted",
	}, nil
}

func (s *Service) PlanRender(ctx context.Context, project string) (model.PlanRender, error) {
	plan, err := s.PlanRead(ctx, project)
	if err != nil {
		return model.PlanRender{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", plan.Title, plan.Summary)
	if plan.CurrentObjective != "" {
		fmt.Fprintf(&b, "Current objective: %s\n\n", plan.CurrentObjective)
	}
	for _, index := range plan.Sections {
		section, err := s.PlanSectionRead(ctx, project, index.ID)
		if err != nil {
			return model.PlanRender{}, err
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n%s\n\n", section.Title, section.ShortDescription, section.Description)
	}
	text := b.String()
	if s.Config.MaxReadBytes > 0 && int64(len(text)) > s.Config.MaxReadBytes {
		return model.PlanRender{}, fmt.Errorf("plan render exceeds configured output limit")
	}
	return model.PlanRender{SchemaVersion: model.PlanSchemaVersion, ProjectID: plan.ProjectID, Revision: plan.Revision, Title: plan.Title, Summary: plan.Summary, CurrentObjective: plan.CurrentObjective, Text: text}, nil
}

func (s *Service) PlanHistory(ctx context.Context, project string, limit int) ([]map[string]string, error) {
	return s.Hub.History(ctx, s.planPath(project), limit)
}

func (s *Service) PlanHistoryPage(ctx context.Context, project string, in CollectionPageInput) (PlanHistoryPageResult, error) {
	limit, err := pagination.Limit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return PlanHistoryPageResult{}, err
	}
	items, err := s.Hub.History(ctx, s.planPath(project), s.Config.MaxListItems)
	if err != nil {
		return PlanHistoryPageResult{}, err
	}
	page, info, err := pagination.Page("plan_history:"+project, items, limit, in.Cursor, func(item map[string]string) string { return item["sha"] })
	if err != nil {
		return PlanHistoryPageResult{}, err
	}
	return PlanHistoryPageResult{
		History:    page,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, nil
}

func (s *Service) ADRList(ctx context.Context, project string) ([]model.ADR, error) {
	if err := validateEntityProject(project); err != nil {
		return nil, err
	}
	records, err := s.entityRegistry(project).ListRecords(ctx, entity.Query{Family: entity.ADRFamily})
	if err != nil {
		return nil, err
	}
	items := make([]model.ADR, 0, len(records))
	for _, record := range records {
		var v model.ADR
		if err := decodeStrict(record.Bytes, &v); err != nil {
			return nil, err
		}
		if err := model.ValidateADR(v); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *Service) ADRListPage(ctx context.Context, project string, in CollectionPageInput) (ADRListPageResult, error) {
	limit, err := pagination.Limit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return ADRListPageResult{}, err
	}
	items, err := s.ADRList(ctx, project)
	if err != nil {
		return ADRListPageResult{}, err
	}
	page, info, err := pagination.Page("adr_list:"+project, items, limit, in.Cursor, func(item model.ADR) string { return item.ID })
	if err != nil {
		return ADRListPageResult{}, err
	}
	return ADRListPageResult{
		ADRs:       page,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, nil
}

func (s *Service) ADRRead(ctx context.Context, project, id string) (model.ADR, error) {
	if err := validateEntityProject(project); err != nil {
		return model.ADR{}, err
	}
	if model.ValidateADRIdentifier(id) != nil && model.ValidateCanonicalADRIdentifier(id) != nil {
		return model.ADR{}, fmt.Errorf("invalid ADR identifier")
	}
	var v model.ADR
	_, err := s.entityRegistry(project).ReadInto(ctx, entity.ADRFamily, id, &v)
	if err == nil {
		err = model.ValidateADR(v)
	}
	return v, err
}

func allocatorConflict(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "project identifiers changed before") ||
		strings.Contains(message, "already exists") ||
		strings.Contains(message, "HUB_REVISION_CONFLICT")
}

// allocatorRetryLimit bounds optimistic allocator retries for every canonical
// ID family, including operator journal events and corrections.

const allocatorRetryLimit = 20

func (s *Service) ADRCreate(ctx context.Context, in ADRCreateInput) (OperationResult, error) {
	if s.Durability != nil {
		return s.adrCreateShared(ctx, in)
	}
	for attempt := 0; ; attempt++ {
		result, err := s.adrCreateOnce(ctx, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return result, err
		}
	}
}
