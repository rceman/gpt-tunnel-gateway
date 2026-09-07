package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

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
	if s.Durability == nil {
		return nil, fmt.Errorf("ADR Shared durability is unavailable")
	}
	return s.listSharedADRs(ctx, project)
}

func (s *Service) ADRListPage(ctx context.Context, project string, in CollectionPageInput) (ADRListPageResult, error) {
	return s.ADRListPageWithOptions(ctx, project, ADRListInput{CollectionPageInput: in})
}

func (s *Service) ADRListPageWithOptions(ctx context.Context, project string, in ADRListInput) (ADRListPageResult, error) {
	if s.Durability == nil {
		return ADRListPageResult{}, fmt.Errorf("ADR Shared durability is unavailable")
	}
	page, err := s.querySharedADRs(ctx, project, "", "", in.IncludeArchived, sqlitestore.SharedLifecycleQueryMaxRows, in.Cursor)
	if err != nil {
		return ADRListPageResult{}, err
	}
	return ADRListPageResult{
		ADRs:       page.ADRs,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
	}, nil
}

func (s *Service) ADRRead(ctx context.Context, project, id string) (model.ADR, error) {
	return s.ADRReadRevision(ctx, project, id, 0)
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
	if s.Durability == nil {
		return OperationResult{}, fmt.Errorf("ADR Shared durability is unavailable")
	}
	return s.adrCreateShared(ctx, in)
}
