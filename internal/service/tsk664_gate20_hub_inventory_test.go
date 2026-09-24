package service

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type tsk664HubFamily struct {
	path             string
	disposition      string
	publisher        string
	restore          string
	replacement      string
	sequenceRevision string
}

var tsk664HubFamilies = []tsk664HubFamily{
	{"project.json", "KEEP", "Hub-first project register/update", "no full Shared project import", "Shared project identity", "project code and repository metadata; no entity allocator"},
	{"identifiers.json", "MIGRATE", "legacy Task/ADR writers", "bootstrap imports identity and reconciles canonical sequence high-water marks", "Shared project identity plus shared_entity_sequences", "project code; legacy NextTask/NextADR values are restore inputs only"},
	{"configuration", "KEEP", "Shared current/outbox/revision payloads use the bounded retired-field migration for watcher and workflow.gate_commands.test.train", "Hub current-state startup migration runs before Shared outbox recovery; restore remains strict canonical ProjectConfiguration", "canonical ProjectConfiguration without retired fields", "new restart-safe marker reruns after the TSK664 marker; configuration revision remains authoritative"},
	{"adrs", "KEEP", "ADR Shared outbox", "Hub-to-Shared current-state and revision restore", "shared_adrs plus revisions/events/relations", "ADR payload revision; shared_entity_sequences; migrated legacy shared_adr_sequences and NextADRNumber"},
	{"rules", "KEEP", "Rule Shared outbox", "Hub-to-Shared current-state and revision restore", "shared_rules plus revisions/events/relations", "Rule payload revision and shared_entity_sequences"},
	{"tasks-v2", "KEEP", "Task Shared outbox; TaskRevision paths are legacy", "Hub-to-Shared current-state and revision restore", "shared_tasks plus revisions/events/relations", "Task payload/store revision; shared_entity_sequences; migrated legacy task sequence and NextTaskNumber"},
	{"milestones", "KEEP", "Milestone Shared outbox", "Hub-to-Shared current-state and revision restore", "shared_milestones plus revisions/events", "Milestone payload revision and shared_entity_sequences"},
	{"tracks", "KEEP", "Track Shared outbox uses canonical semantic same-revision convergence and explicit divergence digests", "Hub-to-Shared current-state, revision, and lifecycle restore; already-applied equivalent delivery is terminal success", "shared_tracks plus revisions/events", "Track payload revision, canonical semantic digest, and shared_entity_sequences"},
	{"journals", "KEEP", "Journal Shared outbox", "Hub-to-Shared current-state and revision restore", "shared_journals plus revisions", "Journal sequence, payload sequence, and shared_entity_sequences"},
	{"relations", "KEEP", "Shared relation outbox", "Hub-to-Shared relation restore", "shared_relations", "directed endpoint tuple; no separate allocator"},
	{"entity-revisions", "KEEP", "Shared immutable-history publisher", "Hub immutable-history importer", "shared_entity_revisions", "per-entity logical revision and immutable history row"},
	{"lifecycle-events", "KEEP", "Shared lifecycle-event publisher", "Hub lifecycle-event importer", "shared_lifecycle_events", "operation_id plus entity revision; SQLite id is local ordering only"},
	{"tasks", "KEEP", "task/create writes tasks-v2; legacy tasks/{id} is only a duplicate-key guard; retired Task/supersede helpers are non-public", "Legacy Hub records are not imported into Shared; canonical tasks-v2 and Shared history are restored", "Shared shared_tasks plus revisions/lifecycle; TaskExecution remains Local", "Legacy task/state revisions are historical; shared_entity_sequences and Shared history are authoritative"},
	{"agents", "LOCAL_ONLY", "Local Agent registration/update only", "no Hub Agent restore; legacy Hub records ignored", "local_agents authority", "Agent timestamps and runtime bindings are Local"},
	{"messages", "LOCAL_ONLY", "legacy entity registry", "no Hub restore", "plaw_messages and Local Sessions", "message creation timestamp; Session ownership is Local"},
	{"operator-journal/events", "KEEP", "legacy evidence writers; current Task evidence readers", "legacy Task evidence reads only; no fresh restore", "Shared Journal/evidence import; retain immutable legacy provenance", "JRN ID sequence and occurred/recorded timestamps"},
	{"operator-journal/counter.json", "MIGRATE", "legacy operator Journal allocator", "Hub counter reconciles Shared Journal sequence high-water", "shared_entity_sequences.journal", "NextEventNumber is a restore high-water input"},
	{"trains-v2", "DELETE", "retired entity registry and legacy records", "no portable restore", "historical Git evidence; not current project semantics", "legacy Train revision and Git commit history"},
	{"train-v2-starts", "DELETE", "no current writer", "no restore", "historical Git evidence", "legacy start evidence only"},
	{"train-attempts", "DELETE", "no current writer", "no restore", "historical Git evidence", "legacy attempt evidence only"},
	{"plan", "KEEP", "plan/* actions are unregistered; new ProjectRegister does not write Plan; normal Plan mutations reject; internal v1 cutover is migration-only", "Not imported into Shared on fresh restore; historical Hub Git data is retained", "Shared Milestones/Tracks and milestone/plan are current authority", "Legacy Plan revision remains historical, not an entity allocator or current revision"},
	{"workflow-policy", "KEEP", "No legacy policy action is registered; ProjectWorkflowPolicyAdopt writes canonical ProjectConfiguration", "Reads require canonical ProjectConfiguration; Shared-durable reads also require named Rules; the legacy path is never read", "Named Shared Rules plus canonical ProjectConfiguration", "Legacy policy revision is historical; Shared Rule/config revisions are canonical"},
	{"runs", "LOCAL_ONLY", "historical runtime family", "no restore", "Local TaskExecution", "runtime sequence/revision is Local"},
	{"operations", "LOCAL_ONLY", "historical runtime family", "no restore", "local_operations", "operation sequence/revision is Local"},
	{"releases", "LOCAL_ONLY", "historical runtime family", "no restore", "Local operational state", "runtime sequence/revision is Local"},
	{"deployments", "LOCAL_ONLY", "historical runtime family", "no restore", "Local operational state", "runtime sequence/revision is Local"},
}

func TestTSK664Gate20HubFamiliesHaveExplicitDisposition(t *testing.T) {
	families := make(map[string]tsk664HubFamily, len(tsk664HubFamilies))
	for _, family := range tsk664HubFamilies {
		if family.path == "" || family.publisher == "" || family.restore == "" || family.replacement == "" || family.sequenceRevision == "" {
			t.Fatalf("incomplete Gate-20 Hub inventory row: %#v", family)
		}
		switch family.disposition {
		case "KEEP", "MIGRATE", "DELETE", "LOCAL_ONLY":
		default:
			t.Fatalf("Hub family %q has invalid Gate-20 disposition %q", family.path, family.disposition)
		}
		if _, exists := families[family.path]; exists {
			t.Fatalf("duplicate Hub family %q", family.path)
		}
		families[family.path] = family
	}
	for _, path := range []string{"tasks", "plan", "workflow-policy"} {
		family, ok := families[path]
		if !ok {
			t.Fatalf("retired Hub family %q has no Gate-20 disposition", path)
		}
		if family.disposition == "MIGRATE" {
			t.Errorf("Hub family %q remains an unexplained migration", path)
		}
	}
	configuration := families["configuration"]
	if configuration.disposition != "KEEP" || !strings.Contains(configuration.publisher, "watcher") || !strings.Contains(configuration.publisher, "workflow.gate_commands.test.train") || !strings.Contains(configuration.restore, "before Shared outbox recovery") {
		t.Errorf("ProjectConfiguration live defect migration is missing from Gate-20 inventory: %#v", configuration)
	}
	tracks := families["tracks"]
	if tracks.disposition != "KEEP" || !strings.Contains(tracks.publisher, "same-revision convergence") || !strings.Contains(tracks.publisher, "divergence digests") || !strings.Contains(tracks.restore, "terminal success") {
		t.Errorf("Track outbox convergence is missing from Gate-20 inventory: %#v", tracks)
	}
	for _, descriptor := range entity.Descriptors() {
		if _, ok := families[descriptor.Collection]; ok {
			continue
		}
		parts := strings.SplitN(descriptor.Collection, "/", 2)
		if _, ok := families[parts[0]]; !ok {
			t.Errorf("entity registry family %s collection %q has no Gate-20 classification", descriptor.Name, descriptor.Collection)
		}
	}

	s := &Service{}
	prefix := s.projectPrefix("example") + "/"
	paths := []string{
		s.projectPath("example"), s.projectIdentifiersPath("example"), s.planPath("example"),
		s.planSectionPath("example", "section"), s.projectConfigurationPath("example"),
		s.adrPath("example", "EXM-ADR1"), s.rulePath("example", "EXM-RUL1"),
		s.taskPath("example", "legacy"), s.taskAuthoringPath("example", "EXM-TSK1"),
		s.taskStatePath("example", "legacy"), s.taskIntegrationReceiptPath("example", "legacy"),
		s.workflowPolicyPath("example"), s.milestonePath("example", "EXM-MIL1"), s.trackPath("example", "EXM-TRK1"),
		s.relationPath(model.Relation{SchemaVersion: model.RelationSchemaVersion, ProjectID: "example", Kind: model.RelationKindCorrects, Source: "EXM-TSK1", Target: "EXM-TSK2", CreatedAt: time.Now().UTC(), CreatedBy: "planner"}),
		s.sharedRevisionPath("example", "task", "EXM-TSK1", 2), s.sharedLifecycleEventPath("example", "task", "EXM-TSK1", "task-op"),
		s.operatorEventsPrefix("example") + "/EXM-JRN1.json", s.projectPrefix("example") + "/operator-journal/counter.json",
		s.journalPath("example", "EXM-JRN1"), s.taskRevisionPrefix("example", "EXM-TSK1") + "/EXM-TSK1.REV2.json",
	}
	for _, path := range paths {
		if !strings.HasPrefix(path, prefix) {
			t.Errorf("sample Hub path %q escaped project prefix", path)
			continue
		}
		relative := strings.TrimPrefix(path, prefix)
		classified := false
		for family := range families {
			if relative == family || strings.HasPrefix(relative, family+"/") {
				classified = true
				break
			}
		}
		if !classified {
			t.Errorf("Hub path %q has no Gate-20 classification", relative)
		}
	}
}

type tsk664SequenceRevisionSource struct {
	source      string
	scope       string
	disposition string
}

var tsk664SequenceRevisionSources = []tsk664SequenceRevisionSource{
	{"shared_entity_sequences(entity_type,project_id)", "Shared", "KEEP"},
	{"shared_project_identifiers.next_task_number/next_adr_number/next_rule_number/next_journal_number", "Shared migration input", "MIGRATE"},
	{"shared_project_identifiers.next_train_number", "Shared historical migration input", "DELETE"},
	{"shared_task_sequences.next_task_number", "Shared", "MIGRATE"},
	{"shared_adr_sequences.next_adr_number", "Shared", "MIGRATE"},
	{"project/<project>/identifiers.json", "Hub", "MIGRATE"},
	{"project/<project>/operator-journal/counter.json", "Hub", "MIGRATE"},
	{"Shared projection revision plus payload logical revision", "Shared", "KEEP"},
	{"shared_entity_revisions(entity_type,entity_id,revision)", "Shared", "KEEP"},
	{"shared_lifecycle_events(operation_id,revision)", "Shared", "KEEP"},
	{"shared_project_configurations.revision", "Shared/Hub", "KEEP"},
	{"TaskRevision files under tasks-v2", "Hub", "KEEP"},
	{"operator-journal JRN identifier suffix", "Hub", "MIGRATE"},
	{"TaskExecution execution_revision and phase IDs", "Local after cutover", "LOCAL_ONLY"},
	{"local_operations.operation_number", "Local", "LOCAL_ONLY"},
	{"Hub Git commit SHA", "Hub Git history", "KEEP"},
}

func TestTSK664Gate20SequenceAndRevisionSourcesAreExplicit(t *testing.T) {
	seen := make(map[string]bool, len(tsk664SequenceRevisionSources))
	for _, source := range tsk664SequenceRevisionSources {
		if source.source == "" || source.scope == "" {
			t.Fatalf("incomplete sequence/revision inventory row: %#v", source)
		}
		switch source.disposition {
		case "KEEP", "MIGRATE", "DELETE", "LOCAL_ONLY":
		default:
			t.Fatalf("sequence/revision source %q has invalid Gate-20 disposition %q", source.source, source.disposition)
		}
		if seen[source.source] {
			t.Fatalf("duplicate sequence/revision source %q", source.source)
		}
		seen[source.source] = true
	}
}
