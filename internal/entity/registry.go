package entity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type BatchSource interface {
	ReadFiles(context.Context, []string) (map[string][]byte, error)
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
	if !descriptor.ProjectScope || r.ProjectID == "" || r.Source == nil {
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

// ReadInto performs one exact entity read with strict JSON decoding. Domain
// callers retain ownership of validation; this adapter only owns path and
// decoding parity.
func (r Registry) ReadInto(ctx context.Context, family Family, id string, out any) (Record, error) {
	record, err := r.Read(ctx, family, id)
	if err != nil {
		return Record{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(record.Bytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return Record{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Record{}, fmt.Errorf("entity JSON has trailing content")
	}
	return record, nil
}

func (r Registry) ListRecords(ctx context.Context, query Query) ([]Record, error) {
	_, err := DescriptorFor(query.Family)
	if err != nil {
		return nil, err
	}
	if r.Source == nil || r.ProjectID == "" {
		return nil, fmt.Errorf("entity registry requires source and project_id")
	}
	paths, err := r.ListRecordPaths(ctx, query)
	if err != nil {
		return nil, err
	}
	return r.ReadRecords(ctx, query.Family, paths)
}

func (r Registry) ListRecordPaths(ctx context.Context, query Query) ([]string, error) {
	descriptor, err := DescriptorFor(query.Family)
	if err != nil {
		return nil, err
	}
	if r.Source == nil || r.ProjectID == "" {
		return nil, fmt.Errorf("entity registry requires source and project_id")
	}
	prefix := path.Join(protocolRoot, r.ProjectID, descriptor.Collection)
	paths, err := r.Source.List(ctx, prefix, descriptor.Suffix)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(paths))
	for _, recordPath := range paths {
		id := strings.TrimSuffix(path.Base(recordPath), descriptor.Suffix)
		if err := descriptor.ValidateID(id); err != nil || !entityPathAllowed(descriptor, recordPath) {
			continue
		}
		if query.Text != "" && !strings.Contains(strings.ToLower(id), strings.ToLower(query.Text)) {
			continue
		}
		filtered = append(filtered, recordPath)
	}
	sort.Strings(filtered)
	return filtered, nil
}

func (r Registry) ReadRecords(ctx context.Context, family Family, paths []string) ([]Record, error) {
	descriptor, err := DescriptorFor(family)
	if err != nil {
		return nil, err
	}
	dataByPath := make(map[string][]byte, len(paths))
	if batch, ok := r.Source.(BatchSource); ok && len(paths) > 0 {
		dataByPath, err = batch.ReadFiles(ctx, paths)
		if err != nil {
			return nil, err
		}
	} else {
		for _, recordPath := range paths {
			data, readErr := r.Source.ReadFile(ctx, recordPath)
			if readErr != nil {
				return nil, readErr
			}
			dataByPath[recordPath] = data
		}
	}
	records := make([]Record, 0, len(paths))
	for _, recordPath := range paths {
		data, ok := dataByPath[recordPath]
		if !ok {
			return nil, fmt.Errorf("missing entity batch record %q", recordPath)
		}
		id := strings.TrimSuffix(path.Base(recordPath), descriptor.Suffix)
		records = append(records, Record{
			Family: family,
			ID:     id,
			Path:   recordPath,
			Bytes:  append([]byte(nil), data...),
		})
	}
	return records, nil
}

func (r Registry) ListPageRecords(ctx context.Context, query Query) (Page, []Record, error) {
	descriptor, err := DescriptorFor(query.Family)
	if err != nil {
		return Page{}, nil, err
	}
	paths, err := r.ListRecordPaths(ctx, query)
	if err != nil {
		return Page{}, nil, err
	}
	limit, err := pagination.Limit(query.Limit, r.MaxItems)
	if err != nil {
		return Page{}, nil, err
	}
	items := make([]Projection, 0, len(paths))
	for _, recordPath := range paths {
		items = append(items, Projection{
			Family: query.Family,
			ID:     strings.TrimSuffix(path.Base(recordPath), descriptor.Suffix),
			Path:   recordPath,
		})
	}
	pageItems, info, err := pagination.Page("entity:"+string(query.Family)+":"+r.ProjectID+":"+query.Text, items, limit, query.Cursor, func(item Projection) string { return item.ID })
	if err != nil {
		return Page{}, nil, err
	}
	selected := make([]string, 0, len(pageItems))
	for _, item := range pageItems {
		selected = append(selected, item.Path)
	}
	records, err := r.ReadRecords(ctx, query.Family, selected)
	if err != nil {
		return Page{}, nil, err
	}
	for i := range pageItems {
		pageItems[i].Bytes = len(records[i].Bytes)
	}
	return Page{
		Items:      pageItems,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, records, nil
}

func (r Registry) List(ctx context.Context, query Query) (Page, error) {
	if _, err := DescriptorFor(query.Family); err != nil {
		return Page{}, err
	}
	if r.Source == nil || r.ProjectID == "" {
		return Page{}, fmt.Errorf("entity registry requires source and project_id")
	}
	page, _, err := r.ListPageRecords(ctx, query)
	if err != nil {
		return Page{}, err
	}
	return page, nil
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
