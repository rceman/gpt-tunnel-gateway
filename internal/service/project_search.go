package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

type ProjectSearchInput struct {
	ProjectID string `json:"project_id"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type ProjectSearchHit struct {
	Family    string `json:"family"`
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Status    string `json:"status,omitempty"`
	Kind      string `json:"kind,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type projectSearchRankedHit struct {
	ProjectSearchHit
	Score int
}

type ProjectSearchResult struct {
	Items      []ProjectSearchHit `json:"items"`
	NextCursor string             `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

func (s *Service) ProjectSearch(ctx context.Context, in ProjectSearchInput) (ProjectSearchResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return ProjectSearchResult{}, err
	}
	query := strings.TrimSpace(strings.ToLower(in.Query))
	if query == "" || len(query) > 256 {
		return ProjectSearchResult{}, fmt.Errorf("query is required and bounded")
	}
	terms := strings.Fields(query)
	limit, err := PublicCollectionLimit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return ProjectSearchResult{}, err
	}
	ranked := make([]projectSearchRankedHit, 0)
	for _, family := range []string{"task", "adr", "rule", "journal"} {
		entities, err := s.sharedProjectEntities(ctx, family, in.ProjectID)
		if err != nil {
			return ProjectSearchResult{}, err
		}
		for _, entity := range entities {
			var record map[string]any
			if err := json.Unmarshal(entity.Payload, &record); err != nil {
				return ProjectSearchResult{}, fmt.Errorf("decode shared %s %s: %w", family, entity.ID, err)
			}
			hit, score, ok := projectSearchHit(family, entity.ID, in.ProjectID, entity.UpdatedAt, record, terms)
			if ok {
				ranked = append(ranked, projectSearchRankedHit{ProjectSearchHit: hit, Score: score})
			}
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		if ranked[i].Family != ranked[j].Family {
			return ranked[i].Family < ranked[j].Family
		}
		return ranked[i].ID < ranked[j].ID
	})
	page, info, err := pagination.Page("project-search:"+in.ProjectID+":"+query, ranked, limit, in.Cursor, func(item projectSearchRankedHit) string {
		return item.Family + "\x00" + item.ID
	})
	if err != nil {
		return ProjectSearchResult{}, err
	}
	items := make([]ProjectSearchHit, len(page))
	for i := range page {
		items[i] = page[i].ProjectSearchHit
	}
	return ProjectSearchResult{Items: items, NextCursor: info.NextCursor, HasMore: info.HasMore}, nil
}

func projectSearchHit(family, id, projectID, updatedAt string, record map[string]any, terms []string) (ProjectSearchHit, int, bool) {
	fields := make([]string, 0, 5)
	text := func(key string) string {
		value, _ := record[key].(string)
		if value != "" {
			fields = append(fields, strings.ToLower(value))
		}
		return value
	}
	title := text("title")
	summary := text("summary")
	if family == "task" {
		summary = text("objective")
	} else if family == "adr" {
		summary = text("context")
	} else if family == "rule" {
		title = text("name")
		summary = text("description")
	}
	status := text("status")
	kind := text("kind")
	fields = append(fields, strings.ToLower(id))
	score := 0
	for _, term := range terms {
		matched := false
		for _, field := range fields {
			if strings.Contains(field, term) {
				matched = true
				if field == strings.ToLower(id) {
					score += 100
				} else if strings.HasPrefix(field, term) {
					score += 20
				} else {
					score += 10
				}
			}
		}
		if !matched {
			return ProjectSearchHit{}, 0, false
		}
	}
	return ProjectSearchHit{Family: family, ID: id, ProjectID: projectID, Title: title, Summary: summary, Status: status, Kind: kind, UpdatedAt: updatedAt}, score, true
}
