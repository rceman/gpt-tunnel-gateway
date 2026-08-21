package service

import "testing"

func TestProjectSearchHitUsesTypedFamilyFieldsAndAllTerms(t *testing.T) {
	hit, score, ok := projectSearchHit("task", "EXM-TSK1", "example", "2026-08-22T10:00:00Z", map[string]any{
		"title": "Latency receipt", "objective": "Bounded local operation", "status": "ready", "secret": "unsearchable",
	}, []string{"latency", "receipt"})
	if !ok || score < 30 || hit.Title != "Latency receipt" || hit.Summary != "Bounded local operation" {
		t.Fatalf("hit=%#v score=%d ok=%v", hit, score, ok)
	}
	if _, _, ok := projectSearchHit("task", "EXM-TSK2", "example", "2026-08-22T10:00:00Z", map[string]any{"title": "Latency"}, []string{"latency", "missing"}); ok {
		t.Fatal("search matched when a query term was absent")
	}
}

func TestProjectSearchHitSupportsADRRuleAndJournalFields(t *testing.T) {
	cases := []struct {
		family string
		id     string
		value  map[string]any
		term   string
		want   string
	}{
		{"adr", "EXM-ADR1", map[string]any{"title": "SQLite", "context": "Local authority"}, "local", "Local authority"},
		{"rule", "EXM-RUL1", map[string]any{"name": "Latency", "description": "Bounded calls"}, "bounded", "Bounded calls"},
		{"journal", "EXM-OPR1", map[string]any{"kind": "operation", "summary": "Checkpoint"}, "checkpoint", "Checkpoint"},
	}
	for _, tc := range cases {
		hit, _, ok := projectSearchHit(tc.family, tc.id, "example", "2026-08-22T10:00:00Z", tc.value, []string{tc.term})
		if !ok || hit.Summary != tc.want && hit.Title != tc.want {
			t.Fatalf("%s hit=%#v ok=%v", tc.family, hit, ok)
		}
	}
}
