package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
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
	if s.Durability != nil {
		return s.listSharedADRs(ctx, project)
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
	if s.Durability != nil {
		return s.readSharedADR(ctx, project, id)
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
