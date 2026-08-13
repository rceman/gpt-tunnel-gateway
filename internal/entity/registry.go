package entity

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

const protocolRoot = "gpt-tunnel/v1/projects"

type Source interface {
	List(context.Context, string, string) ([]string, error)
	ReadFile(context.Context, string) ([]byte, error)
}

type Registry struct {
	Source    Source
	ProjectID string
	MaxItems  int
}

type Record struct {
	Family Family
	ID     string
	Path   string
	Bytes  []byte
}

type Projection struct {
	Family Family `json:"family"`
	ID     string `json:"id"`
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
}

type Query struct {
	Family Family
	Text   string
	Limit  int
	Cursor string
}

type Page struct {
	Items      []Projection `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

func (r Registry) Read(ctx context.Context, family Family, id string) (Record, error) {
	descriptor, err := DescriptorFor(family)
	if err != nil {
		return Record{}, err
	}
	if !descriptor.ProjectScope || r.ProjectID == "" {
		return Record{}, fmt.Errorf("project_id is required")
	}
	if err := descriptor.ValidateID(id); err != nil {
		return Record{}, err
	}
	recordPath := path.Join(protocolRoot, r.ProjectID, descriptor.Collection, id+descriptor.Suffix)
	data, err := r.Source.ReadFile(ctx, recordPath)
	if err != nil {
		return Record{}, err
	}
	return Record{
		Family: family,
		ID:     id,
		Path:   recordPath,
		Bytes:  append([]byte(nil), data...),
	}, nil
}

func (r Registry) List(ctx context.Context, query Query) (Page, error) {
	descriptor, err := DescriptorFor(query.Family)
	if err != nil {
		return Page{}, err
	}
	if r.Source == nil || r.ProjectID == "" {
		return Page{}, fmt.Errorf("entity registry requires source and project_id")
	}
	limit, err := pagination.Limit(query.Limit, r.MaxItems)
	if err != nil {
		return Page{}, err
	}
	prefix := path.Join(protocolRoot, r.ProjectID, descriptor.Collection)
	paths, err := r.Source.List(ctx, prefix, descriptor.Suffix)
	if err != nil {
		return Page{}, err
	}
	items := make([]Projection, 0, len(paths))
	for _, recordPath := range paths {
		id := strings.TrimSuffix(path.Base(recordPath), descriptor.Suffix)
		if err := descriptor.ValidateID(id); err != nil || !entityPathAllowed(descriptor, recordPath) {
			continue
		}
		if query.Text != "" && !strings.Contains(strings.ToLower(id), strings.ToLower(query.Text)) {
			continue
		}
		data, err := r.Source.ReadFile(ctx, recordPath)
		if err != nil {
			return Page{}, err
		}
		items = append(items, Projection{
			Family: query.Family,
			ID:     id,
			Path:   recordPath,
			Bytes:  len(data),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	page, info, err := pagination.Page("entity:"+string(query.Family)+":"+r.ProjectID+":"+query.Text, items, limit, query.Cursor, func(item Projection) string { return item.ID })
	if err != nil {
		return Page{}, err
	}
	return Page{
		Items:      page,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, nil
}

func entityPathAllowed(descriptor Descriptor, recordPath string) bool {
	if descriptor.Family == TaskFamily && (strings.HasSuffix(recordPath, ".state.json") || strings.HasSuffix(recordPath, ".run-counter.json") || strings.Contains(recordPath, "/revisions/")) {
		return false
	}
	if descriptor.Family == TrainFamily && (strings.HasSuffix(recordPath, ".integration.json") || strings.HasSuffix(recordPath, ".integration-operation.json")) {
		return false
	}
	return true
}
