package mcp

import "testing"

func TestNormalizeObjectProjectsPublicTimestampsToUTCSeconds(t *testing.T) {
	got := normalizeObject(map[string]any{
		"updated_at": "2026-08-22T12:34:56.987654+02:00",
		"nested": map[string]any{
			"timestamp": "2026-08-22T10:11:12.123Z",
		},
		"items": []any{map[string]any{"finished_at": "2026-08-22T10:11:13.999999Z"}},
		"label": "2026-08-22T12:34:56.987654+02:00",
	})

	if got["updated_at"] != "2026-08-22T10:34:56Z" {
		t.Fatalf("updated_at=%v", got["updated_at"])
	}
	nested := got["nested"].(map[string]any)
	if nested["timestamp"] != "2026-08-22T10:11:12Z" {
		t.Fatalf("nested timestamp=%v", nested["timestamp"])
	}
	items := got["items"].([]any)
	if items[0].(map[string]any)["finished_at"] != "2026-08-22T10:11:13Z" {
		t.Fatalf("finished_at=%v", items[0])
	}
	if got["label"] != "2026-08-22T12:34:56.987654+02:00" {
		t.Fatalf("non-timestamp label changed: %v", got["label"])
	}
}

func TestNormalizeObjectOmitsZeroPublicTimestamps(t *testing.T) {
	got := normalizeObject(map[string]any{
		"created_at":    "0001-01-01T00:00:00Z",
		"last_activity": "0001-01-01T00:00:00.000000000Z",
		"name":          "kept",
	})
	if _, ok := got["created_at"]; ok {
		t.Fatal("zero created_at was not omitted")
	}
	if _, ok := got["last_activity"]; ok {
		t.Fatal("zero last_activity was not omitted")
	}
	if got["name"] != "kept" {
		t.Fatalf("name=%v", got["name"])
	}
}
