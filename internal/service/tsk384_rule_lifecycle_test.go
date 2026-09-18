package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk384Fixture(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	return s, db
}

func tsk384Create(t *testing.T, s *Service, rule model.Rule) OperationResult {
	t.Helper()
	rule.ProjectID = "example"
	rule.CreatedBy = "worker"
	rule.UpdatedBy = "worker"
	result, err := s.RuleCreate(context.Background(), RuleCreateInput{Rule: rule})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "created" || result.EntityKey == "" {
		t.Fatalf("unexpected rule create result: %#v", result)
	}
	return result
}

func tsk384MachineRule(name string, value json.RawMessage) model.Rule {
	return model.Rule{Title: name, Summary: "seeded " + name, Name: name, Value: value}
}

func tsk384UpdateStatus(t *testing.T, s *Service, key, status string) OperationResult {
	t.Helper()
	result, err := s.RuleUpdateCurrent(context.Background(), RuleUpdateInput{
		ProjectID: "example",
		RuleID:    key,
		Status:    &status,
		Reason:    "transition",
		UpdatedBy: "lead",
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTSK384RuleCreateProducesProposedOnly(t *testing.T) {
	s, _ := tsk384Fixture(t)
	ctx := context.Background()

	machine := tsk384Create(t, s, tsk384MachineRule("ci.release", json.RawMessage(`"release"`)))
	rule, err := s.RuleRead(ctx, "example", machine.EntityKey)
	if err != nil {
		t.Fatal(err)
	}
	if rule.Status != model.RuleStatusProposed || rule.Name != "ci.release" || string(rule.Value) != `"release"` || rule.Revision != 1 {
		t.Fatalf("unexpected machine rule: %#v", rule)
	}

	narrative := tsk384Create(t, s, model.Rule{Title: "Narrative", Summary: "narrative", Description: "Narrative rule body."})
	read, err := s.RuleRead(ctx, "example", narrative.EntityKey)
	if err != nil {
		t.Fatal(err)
	}
	if read.Name != "" || len(read.Value) != 0 || read.Description == "" {
		t.Fatalf("unexpected narrative rule: %#v", read)
	}

	// create status is closed to proposed
	for _, status := range []string{model.RuleStatusAccepted, model.RuleStatusArchived} {
		rule := tsk384MachineRule("forced."+status, json.RawMessage(`1`))
		rule.ProjectID = "example"
		rule.CreatedBy, rule.Status = "worker", status
		if _, err := s.RuleCreate(ctx, RuleCreateInput{Rule: rule}); err == nil {
			t.Fatalf("create with status %q accepted", status)
		}
	}
}

func TestTSK384RuleNameUniquenessImmutabilityAndNonReuse(t *testing.T) {
	s, _ := tsk384Fixture(t)
	ctx := context.Background()

	first := tsk384Create(t, s, tsk384MachineRule("ci.release", json.RawMessage(`"release"`)))
	second := tsk384MachineRule("ci.release", json.RawMessage(`"other"`))
	second.ProjectID = "example"
	second.CreatedBy = "worker"
	if _, err := s.RuleCreate(ctx, RuleCreateInput{Rule: second}); err == nil {
		t.Fatal("duplicate rule name accepted")
	}

	tsk384UpdateStatus(t, s, first.EntityKey, model.RuleStatusAccepted)
	if _, err := s.RuleArchiveCurrent(ctx, RuleArchiveInput{
		ProjectID:  "example",
		RuleID:     first.EntityKey,
		Reason:     "done",
		ArchivedBy: "lead",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RuleCreate(ctx, RuleCreateInput{Rule: second}); err == nil {
		t.Fatal("rule name was reused after archival")
	}

	// name is not a mutable surface: updating cannot rename
	title := "Renamed"
	if _, err := s.RuleUpdateCurrent(ctx, RuleUpdateInput{
		ProjectID: "example",
		RuleID:    first.EntityKey,
		Title:     &title,
		Reason:    "rename",
		UpdatedBy: "lead",
	}); err == nil {
		t.Fatal("archived rule update accepted")
	}
	read, err := s.RuleRead(ctx, "example", first.EntityKey)
	if err != nil {
		t.Fatal(err)
	}
	if read.Name != "ci.release" {
		t.Fatalf("rule name mutated: %#v", read)
	}
}

func TestTSK384RuleLifecycleTransitions(t *testing.T) {
	s, _ := tsk384Fixture(t)
	ctx := context.Background()

	rule := tsk384Create(t, s, tsk384MachineRule("ci.task", json.RawMessage(`"task"`)))
	if result := tsk384UpdateStatus(t, s, rule.EntityKey, model.RuleStatusAccepted); result.Status != "updated" {
		t.Fatalf("proposed->accepted failed: %#v", result)
	}
	read, err := s.RuleRead(ctx, "example", rule.EntityKey)
	if err != nil || read.Status != model.RuleStatusAccepted {
		t.Fatalf("rule not accepted: %#v %v", read, err)
	}
	if _, err := s.RuleUpdateCurrent(ctx, RuleUpdateInput{
		ProjectID: "example",
		RuleID:    rule.EntityKey,
		Status:    ptr(model.RuleStatusProposed),
		Reason:    "backwards",
		UpdatedBy: "lead",
	}); err == nil {
		t.Fatal("accepted->proposed transition accepted")
	}
	archived, err := s.RuleArchiveCurrent(ctx, RuleArchiveInput{
		ProjectID:  "example",
		RuleID:     rule.EntityKey,
		Reason:     "retire",
		ArchivedBy: "lead",
	})
	if err != nil || archived.Status != "archived" {
		t.Fatalf("archive failed: %#v %v", archived, err)
	}
	read, err = s.RuleRead(ctx, "example", rule.EntityKey)
	if err != nil || read.Status != model.RuleStatusArchived || read.ArchivedAt == nil {
		t.Fatalf("rule not archived: %#v %v", read, err)
	}

	// proposed -> archived is also an allowed lifecycle edge
	direct := tsk384Create(t, s, tsk384MachineRule("ci.task_merge", json.RawMessage(`"merge"`)))
	if _, err := s.RuleArchiveCurrent(ctx, RuleArchiveInput{
		ProjectID:  "example",
		RuleID:     direct.EntityKey,
		Reason:     "retire",
		ArchivedBy: "lead",
	}); err != nil {
		t.Fatalf("proposed->archived rejected: %v", err)
	}
}

func TestTSK384RuleTypedValuePreservation(t *testing.T) {
	s, _ := tsk384Fixture(t)
	ctx := context.Background()

	created := tsk384Create(t, s, tsk384MachineRule("agent.wait_for_ci", json.RawMessage(`true`)))
	tsk384UpdateStatus(t, s, created.EntityKey, model.RuleStatusAccepted)
	effective, _, err := s.RuleEffectiveSet(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) != 1 || effective[0].Name != "agent.wait_for_ci" {
		t.Fatalf("unexpected effective set: %#v", effective)
	}
	var decoded any
	if err := json.Unmarshal(effective[0].Value, &decoded); err != nil {
		t.Fatal(err)
	}
	if value, ok := decoded.(bool); !ok || !value {
		t.Fatalf("boolean rule value was not preserved: %#v (%T)", decoded, decoded)
	}
}

func TestTSK384RuleEffectiveSetIsAcceptedNamedOnly(t *testing.T) {
	s, _ := tsk384Fixture(t)
	ctx := context.Background()

	proposed := tsk384Create(t, s, tsk384MachineRule("ci.release", json.RawMessage(`"release"`)))
	accepted := tsk384Create(t, s, tsk384MachineRule("ci.task", json.RawMessage(`"task"`)))
	tsk384UpdateStatus(t, s, accepted.EntityKey, model.RuleStatusAccepted)
	tsk384Create(t, s, model.Rule{Title: "Narrative", Summary: "narrative", Description: "body"})

	effective, digest, err := s.RuleEffectiveSet(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) != 1 || effective[0].ID != accepted.EntityKey {
		t.Fatalf("effective set is not accepted+named only: %#v", effective)
	}
	if digest == "" || len(digest) != 64 {
		t.Fatalf("invalid effective digest: %q", digest)
	}

	// proposed named rule joins after acceptance -> membership change alters digest
	tsk384UpdateStatus(t, s, proposed.EntityKey, model.RuleStatusAccepted)
	effective, nextDigest, err := s.RuleEffectiveSet(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) != 2 || nextDigest == digest {
		t.Fatalf("membership change did not alter digest: %q -> %q", digest, nextDigest)
	}

	// content revision change alters digest deterministically
	summary := "revised"
	if _, err := s.RuleUpdateCurrent(ctx, RuleUpdateInput{
		ProjectID: "example",
		RuleID:    proposed.EntityKey,
		Summary:   &summary,
		Reason:    "rev",
		UpdatedBy: "lead",
	}); err != nil {
		t.Fatal(err)
	}
	_, thirdDigest, err := s.RuleEffectiveSet(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if thirdDigest == nextDigest || thirdDigest == "" {
		t.Fatalf("revision change did not alter digest: %q -> %q", nextDigest, thirdDigest)
	}
}

func TestTSK384RuleSessionAcknowledgementIsBoundToExactDigest(t *testing.T) {
	s, db := tsk384Fixture(t)
	ctx := context.Background()

	record, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	store := durableSession.NewStoreWithDurability(db)

	_, digest, err := s.RuleEffectiveSet(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.AcknowledgeRules(record.ID, "global", "global-digest", digest)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProjectRulesDigest != digest {
		t.Fatalf("session acknowledgement not bound to digest: %#v", updated)
	}
	// acknowledgement is idempotent for the exact effective set
	again, err := store.AcknowledgeRules(record.ID, "global", "global-digest", digest)
	if err != nil || again.ProjectRulesDigest != digest {
		t.Fatalf("idempotent acknowledgement failed: %#v %v", again, err)
	}
	// a different effective-set digest is a distinct acknowledgement
	created := tsk384Create(t, s, tsk384MachineRule("ci.release", json.RawMessage(`"release"`)))
	tsk384UpdateStatus(t, s, created.EntityKey, model.RuleStatusAccepted)
	_, nextDigest, err := s.RuleEffectiveSet(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if nextDigest == digest {
		t.Fatal("effective digest did not change after membership change")
	}
	latest, err := store.AcknowledgeRules(record.ID, "global", "global-digest", nextDigest)
	if err != nil || latest.ProjectRulesDigest != nextDigest {
		t.Fatalf("re-acknowledgement not bound to new digest: %#v %v", latest, err)
	}
}

func TestTSK384RuleRelationSugarAndLiveTitleProjection(t *testing.T) {
	s, db := tsk384Fixture(t)
	ctx := context.Background()

	tsk511InsertEntity(t, db, "shared_adrs", "EXM-ADR1", "title", "First ADR")
	tsk511InsertEntity(t, db, "shared_tasks", "EXM-TSK1", "title", "First Task")

	// RUL source producing an authority relation via create sugar
	rule := tsk384MachineRule("integration_branch", json.RawMessage(`"main"`))
	rule.ProjectID = "example"
	rule.CreatedBy = "worker"
	created, err := s.RuleCreate(ctx, RuleCreateInput{
		Rule:           rule,
		RelationType:   model.RelationKindAuthority,
		RelationTarget: "EXM-ADR1",
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := s.RelationProjection(ctx, "example", created.EntityKey)
	if err != nil {
		t.Fatal(err)
	}
	if projected[model.RelationKindAuthority]["EXM-ADR1"] != "First ADR" {
		t.Fatalf("authority relation sugar projection missing: %#v", projected)
	}

	// RUL as authority target resolves live title from the title field, not name
	if _, err := s.RelationCreate(ctx, RelationCreateInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Kind:      model.RelationKindAuthority,
		Target:    created.EntityKey,
		CreatedBy: "worker",
	}); err != nil {
		t.Fatal(err)
	}
	taskView, err := s.RelationProjection(ctx, "example", "EXM-TSK1")
	if err != nil {
		t.Fatal(err)
	}
	if taskView[model.RelationKindAuthority][created.EntityKey] != "integration_branch" {
		t.Fatalf("rule live title not projected from title field: %#v", taskView)
	}
	if field, err := model.RelationTitleField(model.RelationFamilyRule); err != nil || field != "title" {
		t.Fatalf("rule title field = %q err=%v", field, err)
	}
}

func TestTSK384RuleListQueryAndHistoryAreBounded(t *testing.T) {
	s, _ := tsk384Fixture(t)
	ctx := context.Background()

	created := tsk384Create(t, s, tsk384MachineRule("ci.release", json.RawMessage(`"release"`)))
	tsk384UpdateStatus(t, s, created.EntityKey, model.RuleStatusAccepted)

	page, err := s.RuleListPageWithOptions(ctx, "example", RuleListInput{CollectionPageInput: CollectionPageInput{
		Limit: 64,
	}})
	if err != nil || len(page.Rules) != 1 {
		t.Fatalf("unexpected rule list: %#v %v", page, err)
	}
	queried, err := s.RuleQuery(ctx, "example", RuleQueryInput{
		CollectionPageInput: CollectionPageInput{
			Limit: 64,
		},
		Status: model.RuleStatusAccepted,
	})
	if err != nil || len(queried.Rules) != 1 {
		t.Fatalf("unexpected rule query: %#v %v", queried, err)
	}
	history, err := s.RuleHistoryPage(ctx, "example", created.EntityKey, CollectionPageInput{Limit: 64}, false)
	if err != nil || len(history.Items) != 2 {
		t.Fatalf("unexpected rule history: %#v %v", history, err)
	}
	if history.Items[0].MutationKind != "create" || history.Items[1].MutationKind != "status" {
		t.Fatalf("unexpected history mutations: %#v", history.Items)
	}
}

func TestTSK384RuleReadSupportsExactRevision(t *testing.T) {
	s, _ := tsk384Fixture(t)
	ctx := context.Background()

	created := tsk384Create(t, s, tsk384MachineRule("ci.release", json.RawMessage(`"release"`)))
	summary := "v2 summary"
	if _, err := s.RuleUpdateCurrent(ctx, RuleUpdateInput{
		ProjectID: "example",
		RuleID:    created.EntityKey,
		Summary:   &summary,
		Reason:    "rev",
		UpdatedBy: "lead",
	}); err != nil {
		t.Fatal(err)
	}
	historical, err := s.RuleReadRevision(ctx, "example", created.EntityKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	if historical.Revision != 1 || historical.Summary != "seeded ci.release" {
		t.Fatalf("unexpected historical rule: %#v", historical)
	}
}

// tsk384ConfiguredFixture establishes a configured project under Shared
// durability: the shared configuration projection plus the six seeded
// machine-policy leaf rules, mirroring atomic bootstrap establishment.
func tsk384ConfiguredFixture(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedSharedRulesFromConfiguration(context.Background(), configuration, "EXM"); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func tsk384RuleByName(t *testing.T, s *Service, name string) model.Rule {
	t.Helper()
	page, err := s.RuleListPageWithOptions(context.Background(), "example", RuleListInput{IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range page.Rules {
		if rule.Name == name {
			return rule
		}
	}
	t.Fatalf("no rule named %q", name)
	return model.Rule{}
}

func TestTSK384RuleUpdateLeafChangesDigestAndDerivedPolicy(t *testing.T) {
	s, _ := tsk384ConfiguredFixture(t)
	ctx := context.Background()

	before, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	digestBefore, err := s.ProjectRuleEffectiveDigest(ctx, "example")
	if err != nil || digestBefore == "" {
		t.Fatalf("unexpected effective digest: %q %v", digestBefore, err)
	}
	target := tsk384RuleByName(t, s, "ci.release")
	value := json.RawMessage(`"require"`)
	if _, err := s.RuleUpdateCurrent(ctx, RuleUpdateInput{
		ProjectID: "example",
		RuleID:    target.ID,
		Value:     &value,
		Reason:    "route leaf change through rules",
		UpdatedBy: "planner",
	}); err != nil {
		t.Fatal(err)
	}
	digestAfter, err := s.ProjectRuleEffectiveDigest(ctx, "example")
	if err != nil || digestAfter == digestBefore {
		t.Fatalf("rule update did not change effective digest: before=%s after=%s err=%v", digestBefore, digestAfter, err)
	}
	after, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if after.CI.Release != model.WorkflowCIModeRequire || after.CI.Release == before.CI.Release {
		t.Fatalf("derived policy did not track the rule leaf: before=%#v after=%#v", before.CI, after.CI)
	}
	fast, err := s.ProjectWorkflowPolicyReadFast(ctx, "example")
	if err != nil || fast.CI.Release != model.WorkflowCIModeRequire {
		t.Fatalf("fast policy read did not track the rule leaf: %#v %v", fast.CI, err)
	}
}

func TestTSK384PolicyDerivationFailsClosedOnMissingLeaf(t *testing.T) {
	s, _ := tsk384ConfiguredFixture(t)
	ctx := context.Background()

	target := tsk384RuleByName(t, s, "ci.task")
	if _, err := s.RuleArchiveCurrent(ctx, RuleArchiveInput{
		ProjectID:  "example",
		RuleID:     target.ID,
		Reason:     "remove leaf",
		ArchivedBy: "lead",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectWorkflowPolicyRead(ctx, "example"); err == nil || !strings.Contains(err.Error(), `"ci.task"`) {
		t.Fatalf("missing required leaf did not fail closed: %v", err)
	}
	if _, err := s.ProjectWorkflowPolicyReadFast(ctx, "example"); err == nil || !strings.Contains(err.Error(), `"ci.task"`) {
		t.Fatalf("missing required leaf did not fail closed on fast read: %v", err)
	}
}

func TestTSK384WorkflowPolicyAdoptRejectsLeafDivergence(t *testing.T) {
	s, _ := tsk384ConfiguredFixture(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")

	policy, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	divergent := policy
	divergent.CI.Release = model.WorkflowCIModeDisabled
	if _, _, err := s.ProjectWorkflowPolicyAdopt(ctx, ProjectWorkflowPolicyInput{
		Policy: divergent,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil || !strings.Contains(err.Error(), "governed by durable rules") {
		t.Fatalf("leaf-divergent adopt was not rejected: %v", err)
	}
	policy.Revision++
	_, result, err := s.ProjectWorkflowPolicyAdopt(ctx, ProjectWorkflowPolicyInput{
		Policy: policy,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "adopted" && result.Status != "updated" {
		t.Fatalf("unexpected adopt status: %#v", result)
	}
}
