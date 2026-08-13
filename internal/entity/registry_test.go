package entity

import (
	"context"
	"fmt"
	"testing"
)

type memorySource struct {
	files map[string][]byte
}

func (m memorySource) List(_ context.Context, prefix, suffix string) ([]string, error) {
	paths := make([]string, 0)
	for file := range m.files {
		if len(file) >= len(prefix) && file[:len(prefix)] == prefix && len(file) >= len(suffix) && file[len(file)-len(suffix):] == suffix {
			paths = append(paths, file)
		}
	}
	return paths, nil
}

func (m memorySource) ReadFile(_ context.Context, file string) ([]byte, error) {
	data, ok := m.files[file]
	if !ok {
		return nil, fmt.Errorf("missing %s", file)
	}
	return data, nil
}

func TestDescriptorsCoverCanonicalFamilies(t *testing.T) {
	if got := len(Descriptors()); got != 6 {
		t.Fatalf("descriptor count=%d", got)
	}
	for _, family := range []Family{TaskFamily, ADRFamily, RuleFamily, MessageFamily, TrainFamily, JournalFamily} {
		if _, err := DescriptorFor(family); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRegistryReadListOrderingFiltersAndContinuation(t *testing.T) {
	source := memorySource{files: map[string][]byte{
		"gpt-tunnel/v1/projects/p/tasks/TSK2.json":       []byte("two"),
		"gpt-tunnel/v1/projects/p/tasks/TSK1.json":       []byte("one"),
		"gpt-tunnel/v1/projects/p/tasks/TSK1.state.json": []byte("state"),
		"gpt-tunnel/v1/projects/p/tasks/TSK3.json":       []byte("three"),
	}}
	registry := Registry{
		Source:    source,
		ProjectID: "p",
		MaxItems:  100,
	}
	page, err := registry.List(context.Background(), Query{
		Family: TaskFamily,
		Limit:  2,
	})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != "TSK1" || page.Items[1].ID != "TSK2" || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	second, err := registry.List(context.Background(), Query{
		Family: TaskFamily,
		Limit:  2,
		Cursor: page.NextCursor,
	})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "TSK3" {
		t.Fatalf("continuation=%#v err=%v", second, err)
	}
	read, err := registry.Read(context.Background(), TaskFamily, "TSK1")
	if err != nil || string(read.Bytes) != "one" {
		t.Fatalf("read=%#v err=%v", read, err)
	}
}
