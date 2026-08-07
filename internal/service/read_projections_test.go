package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanReadPageIsBoundedAndCursorBound(t *testing.T) {
	svc, _, _ := testService(t)
	ctx := context.Background()

	page, err := svc.PlanReadPage(ctx, "example", 0, "")
	if err != nil {
		t.Fatalf("default plan read: %v", err)
	}
	if len(page.Sections) > planReadMaxLimit {
		t.Fatalf("plan read returned %d sections, want at most %d", len(page.Sections), planReadMaxLimit)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal plan projection: %v", err)
	}
	if strings.Contains(string(encoded), "description") || strings.Contains(string(encoded), "body") {
		t.Fatalf("bounded plan projection leaked section detail: %s", encoded)
	}
	if _, err := svc.PlanReadPage(ctx, "example", planReadMaxLimit+1, ""); err == nil {
		t.Fatal("plan read accepted a limit above the hard maximum")
	}

	if page.NextCursor != "" {
		if _, err := svc.PlanReadPage(ctx, "example", 0, page.NextCursor); err != nil {
			t.Fatalf("valid cursor rejected: %v", err)
		}
	}
	foreign := planReadCursor{
		Version:        1,
		ProjectID:      "other-project",
		Revision:       page.Revision,
		Limit:          planReadDefaultLimit,
		Offset:         1,
		SectionsSHA256: sectionsDigest(page.Sections),
	}
	foreignCursor, err := encodePlanReadCursor(foreign)
	if err != nil {
		t.Fatalf("encode foreign cursor: %v", err)
	}
	if _, err := svc.PlanReadPage(ctx, "example", 0, foreignCursor); err == nil {
		t.Fatal("plan read accepted a cursor for another project")
	}
	if _, err := svc.PlanReadPage(ctx, "example", 0, "not-a-cursor"); err == nil {
		t.Fatal("plan read accepted an invalid cursor")
	}
}

func TestProjectStatusBaselineDeltaAndTokenValidation(t *testing.T) {
	svc, _, _ := testService(t)
	ctx := context.Background()

	baselineValue, err := svc.ProjectStatusRead(ctx, "example", "")
	if err != nil {
		t.Fatalf("baseline project status: %v", err)
	}
	baseline, ok := baselineValue.(ProjectStatus)
	if !ok {
		t.Fatalf("baseline type = %T, want ProjectStatus", baselineValue)
	}
	if baseline.StatusToken == "" {
		t.Fatal("baseline did not return a status token")
	}
	baselineJSON, err := json.Marshal(baseline)
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}

	unchangedValue, err := svc.ProjectStatusRead(ctx, "example", baseline.StatusToken)
	if err != nil {
		t.Fatalf("unchanged project status: %v", err)
	}
	unchanged, ok := unchangedValue.(ProjectStatusDelta)
	if !ok {
		t.Fatalf("unchanged type = %T, want ProjectStatusDelta", unchangedValue)
	}
	if unchanged.Changed {
		t.Fatalf("unchanged status reported changes: %#v", unchanged)
	}
	unchangedJSON, err := json.Marshal(unchanged)
	if err != nil {
		t.Fatalf("marshal unchanged delta: %v", err)
	}
	if len(unchangedJSON) >= len(baselineJSON) {
		t.Fatalf("unchanged delta was not smaller than baseline: %d >= %d", len(unchangedJSON), len(baselineJSON))
	}

	if _, err := svc.ProjectStatusRead(ctx, "example", "invalid-token"); err == nil || !strings.Contains(err.Error(), "fresh project_status baseline") {
		t.Fatalf("invalid token error = %v", err)
	}
	statusToken, err := decodeStatusToken(baseline.StatusToken)
	if err != nil {
		t.Fatalf("decode baseline token: %v", err)
	}
	statusToken.ProjectID = "other-project"
	foreignToken, err := encodeStatusToken(statusToken)
	if err != nil {
		t.Fatalf("encode foreign status token: %v", err)
	}
	if _, err := svc.ProjectStatusRead(ctx, "example", foreignToken); err == nil {
		t.Fatal("project status accepted a token for another project")
	}
}

func TestProjectStatusProjectionDoesNotIncludePlanSections(t *testing.T) {
	svc, _, _ := testService(t)
	value, err := svc.ProjectStatusRead(context.Background(), "example", "")
	if err != nil {
		t.Fatalf("project status: %v", err)
	}
	status, ok := value.(ProjectStatus)
	if !ok {
		t.Fatalf("status type = %T, want ProjectStatus", value)
	}
	b, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(b), "sections") || strings.Contains(string(b), "full description") {
		t.Fatalf("project status contains unbounded plan detail: %s", b)
	}
}

func TestProjectStatusDeltaDetectsLiveProgressChange(t *testing.T) {
	svc, _, _ := testService(t)
	writeLivenessScript(t, svc, "initial tail", "idle", "initial log")
	baselineValue, err := svc.ProjectStatusRead(context.Background(), "example", "")
	if err != nil {
		t.Fatalf("baseline project status: %v", err)
	}
	baseline := baselineValue.(ProjectStatus)

	writeLivenessScript(t, svc, "changed tail", "idle", "changed log")
	deltaValue, err := svc.ProjectStatusRead(context.Background(), "example", baseline.StatusToken)
	if err != nil {
		t.Fatalf("changed project status: %v", err)
	}
	delta, ok := deltaValue.(ProjectStatusDelta)
	if !ok {
		t.Fatalf("delta type = %T, want ProjectStatusDelta", deltaValue)
	}
	if !delta.Changed {
		t.Fatalf("live change was not detected: %#v", delta)
	}
	foundProgress := false
	for _, component := range delta.ChangedComponents {
		if component == "progress" {
			foundProgress = true
		}
	}
	if !foundProgress {
		t.Fatalf("changed components = %#v, want progress", delta.ChangedComponents)
	}
}
