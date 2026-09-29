package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectRetirementCancelsPendingConfigurationAndExcludesHistoryFromMigration(t *testing.T) {
	ctx := context.Background()
	db, err := OpenWithObserverDeferredProjectConfigurationMigration(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	for _, projectID := range []string{"agentir", "reposuite-mcp"} {
		currentPayload := projectRetirementLegacyPayload(t, projectID, 3)
		updatedAt := now.Format(time.RFC3339Nano)
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, projectID, 3, currentPayload, updatedAt); err != nil {
			t.Fatal(err)
		}
		for revision := int64(4); revision <= 5; revision++ {
			id := fmt.Sprintf("%s-config-%d", projectID, revision)
			payload := projectRetirementLegacyPayload(t, projectID, revision)
			if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, id, "project_configuration", projectID, projectID, revision, "project-configuration-update", payload, updatedAt); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", projectID, projectID, 1, "update", "planner", "legacy history", []byte(`[]`), []byte(`not-json`), updatedAt); err != nil {
			t.Fatal(err)
		}
		pending, err := db.pendingProjectConfigurationOutboxForRetirement(ctx, projectID)
		if err != nil {
			t.Fatal(err)
		}
		wantDigest := pendingProjectConfigurationDigest(pending)
		preliminary := model.ProjectRetirement{
			SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: projectID, Revision: 1,
			Reason: "stale project retirement", Actor: "gatewayd", RetiredAt: now,
			CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(nil),
		}
		if err := db.BeginLocalProjectRetirement(ctx, preliminary); err != nil {
			t.Fatal(err)
		}
		retirement, created, err := db.RetireSharedProject(ctx, projectID, preliminary.Reason, now)
		if err != nil || !created {
			t.Fatalf("retire %s: record=%#v created=%v err=%v", projectID, retirement, created, err)
		}
		if retirement.ConfigurationRevision != 5 || retirement.CancelledConfigPublications != 2 || retirement.CancelledConfigOutboxSHA256 != wantDigest {
			t.Fatalf("retirement boundary=%#v", retirement)
		}
		if err := model.ValidateProjectRetirement(retirement); err != nil {
			t.Fatalf("invalid retirement evidence: %v", err)
		}
		if err := db.PutSharedProjection(ctx, "project_configuration", SharedEntity{
			ID:        projectID,
			Revision:  6,
			Payload:   currentPayload,
			UpdatedAt: updatedAt,
		}); !errors.Is(err, ErrLocalProjectRetired) {
			t.Fatalf("retired Shared projection accepted a current configuration: %v", err)
		}
		if _, err := db.ReadSharedProjectIdentifiers(ctx, projectID); !errors.Is(err, ErrProjectRetirementConflict) {
			t.Fatalf("retired Shared project identity remained readable: %v", err)
		}
		if rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_project_configurations WHERE id=?`, projectID); err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(0) {
			t.Fatalf("current ProjectConfiguration remains active: rows=%#v err=%v", rows, err)
		}
		rows, err := db.Shared.Query(ctx, `SELECT COUNT(*),COUNT(cancelled_at),COUNT(cancellation_reason),MIN(cancellation_reason),MAX(LENGTH(cancellation_reason)) FROM hub_outbox WHERE entity_type='project_configuration' AND entity_id=?`, projectID)
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(2) || rows.Rows[0][1] != int64(2) || rows.Rows[0][2] != int64(2) || rows.Rows[0][3] != "project retired" || rows.Rows[0][4] != int64(len("project retired")) {
			t.Fatalf("pending publications were not explicitly cancelled: rows=%#v err=%v", rows, err)
		}
		history, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=?`, projectID)
		if err != nil || len(history.Rows) != 1 || history.Rows[0][0] != int64(1) {
			t.Fatalf("configuration history was removed: rows=%#v err=%v", history, err)
		}
		if err := db.BeginLocalProjectRetirement(ctx, retirement); err != nil {
			t.Fatalf("idempotent Local retirement: %v", err)
		}
		replayed, created, err := db.RetireSharedProject(ctx, projectID, preliminary.Reason, now.Add(time.Hour))
		if err != nil || created || replayed.ProjectID != projectID || replayed.CancelledConfigOutboxSHA256 != retirement.CancelledConfigOutboxSHA256 {
			t.Fatalf("idempotent Shared retirement: record=%#v created=%v err=%v", replayed, created, err)
		}
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, projectID, 3, []byte(`not-json`), updatedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, projectID+"-stale-after-retirement", "project_configuration", projectID, projectID, 6, "project-configuration-update", []byte(`not-json`), updatedAt); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("migration scanned retired current/outbox/history rows: %v", err)
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	complete, err := db.ProjectConfigurationMigrationComplete(ctx)
	if err != nil || !complete {
		t.Fatalf("ProjectConfiguration migration complete=%v err=%v", complete, err)
	}
	for _, projectID := range []string{"agentir", "reposuite-mcp"} {
		if rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=?`, projectID); err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(1) {
			t.Fatalf("migration changed immutable %s history: rows=%#v err=%v", projectID, rows, err)
		}
	}
}

func TestImportHubProjectRetirementExcludesMalformedConfigurationSourcesBeforeMigration(t *testing.T) {
	ctx := context.Background()
	db, err := OpenWithObserverDeferredProjectConfigurationMigration(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	projectID := "retired-import"
	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, projectID, 3, []byte(`not-json`), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", projectID, projectID, 3, "update", "planner", "preserved history", []byte(`[]`), []byte(`not-json`), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "retired-import-outbox", "project_configuration", projectID, projectID, 3, "project-configuration-update", []byte(`not-json`), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	pending, err := db.pendingProjectConfigurationOutboxForImport(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	retirement := model.ProjectRetirement{
		SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: projectID, Revision: 1,
		Reason: "imported Hub retirement", Actor: "gatewayd", RetiredAt: now,
		ConfigurationRevision: 3, CancelledConfigPublications: len(pending),
		CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(pending),
	}
	if err := model.ValidateProjectRetirement(retirement); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportSharedProjectRetirement(ctx, retirement); err != nil {
		t.Fatalf("import Hub retirement over malformed stale configuration: %v", err)
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("migration scanned imported retired sources: %v", err)
	}
	current, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_project_configurations WHERE id=?`, projectID)
	if err != nil || len(current.Rows) != 1 || current.Rows[0][0] != int64(0) {
		t.Fatalf("retired current configuration remains: rows=%#v err=%v", current, err)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT cancelled_at,cancellation_reason FROM hub_outbox WHERE id=?`, "retired-import-outbox")
	if err != nil || len(outbox.Rows) != 1 || outbox.Rows[0][0] == "" || outbox.Rows[0][1] == "" {
		t.Fatalf("retired outbox row lacks cancellation evidence: rows=%#v err=%v", outbox, err)
	}
	history, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=?`, projectID)
	if err != nil || len(history.Rows) != 1 || history.Rows[0][0] != int64(1) {
		t.Fatalf("import deleted immutable history: rows=%#v err=%v", history, err)
	}
}

func TestLocalProjectRetirementFailsClosedAndFencesAdmission(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	const projectID = "agentir"
	oldPayload := []byte(`{"project_id":"agentir","session_type":"chatgpt","status":"active"}`)
	session := LocalSession{
		ID:          "HOM_GTW_W_ABCDEFGH",
		ProjectID:   projectID,
		SessionType: "chatgpt",
		Payload:     oldPayload,
		UpdatedAt:   now.Format(time.RFC3339Nano),
		Status:      "active",
	}
	if err := db.CreateLocalSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSessionBootstrapGrant(ctx, SessionBootstrapGrant{
		ProjectID:   projectID,
		ProjectCode: "AIR",
		GatewayID:   "gateway-test",
		Role:        "planner",
	}); err != nil {
		t.Fatal(err)
	}
	retirement := model.ProjectRetirement{
		SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: projectID, Revision: 1,
		Reason: "stale project retirement", Actor: "gatewayd", RetiredAt: now,
		CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(nil),
	}
	if err := db.BeginLocalProjectRetirement(ctx, retirement); !errors.Is(err, ErrProjectRetirementUnsafe) {
		t.Fatalf("retirement with active Session err=%v", err)
	}
	if _, found, err := db.ReadLocalProjectRetirement(ctx, projectID); err != nil || found {
		t.Fatalf("unsafe retirement created a tombstone: found=%v err=%v", found, err)
	}
	newPayload := []byte(`{"project_id":"agentir","session_type":"chatgpt","status":"ended"}`)
	if err := db.UpdateLocalSession(ctx, session.ID, oldPayload, newPayload, now.Add(time.Minute).Format(time.RFC3339Nano), "ended"); err != nil {
		t.Fatal(err)
	}
	operation, err := db.AllocateLocalOperation(ctx, projectID, "AIR", strings.Repeat("b", 64), "task-authoring-update", now)
	if err != nil {
		t.Fatal(err)
	}
	operation.Status = "completed"
	operation.UpdatedAt = now.Add(2 * time.Minute)
	if err := db.UpdateLocalOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTaskExecutionState(ctx, model.TaskExecutionState{
		TaskID: "AIR-TSK1", ProjectID: projectID, TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionFailed, Stage: "code", Worktree: "WT-TSK1-cccccccc", BaseHead: strings.Repeat("b", 40),
		Head: strings.Repeat("c", 40), Branch: "task/AIR-TSK1-retirement-test", Agent: "coder-agentir", ExecutionRevision: 1, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ArmCallbackEpoch(ctx, CallbackEpoch{
		ID:         "epoch-completed",
		ProjectID:  projectID,
		SessionKey: "agentir_worker",
		ArmedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimCallbackEpochWithoutHook(ctx, "epoch-completed", now.Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("terminal callback claim=%v err=%v", claimed, err)
	}
	if err := db.BeginLocalProjectRetirement(ctx, retirement); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReadSessionBootstrapGrant(ctx, projectID); !errors.Is(err, ErrSessionBootstrapGrantNotFound) {
		t.Fatalf("retirement retained Session bootstrap grant: %v", err)
	}
	if err := db.CreateLocalSession(ctx, LocalSession{
		ID:          "HOM_GTW_W_IJKLMNOP",
		ProjectID:   projectID,
		SessionType: "chatgpt",
		Payload:     oldPayload,
		UpdatedAt:   now.Format(time.RFC3339Nano),
		Status:      "active",
	}); err == nil {
		t.Fatal("retired project accepted a new Session")
	}
	if _, err := db.AllocateLocalOperation(ctx, projectID, "AIR", strings.Repeat("a", 64), "task-authoring-update", now); !errors.Is(err, ErrLocalProjectRetired) {
		t.Fatalf("retired project accepted a new Operation: %v", err)
	}
	if err := db.ArmCallbackEpoch(ctx, CallbackEpoch{
		ID:         "epoch-retired",
		ProjectID:  projectID,
		SessionKey: "agentir_worker",
		ArmedAt:    now,
	}); !errors.Is(err, ErrLocalProjectRetired) {
		t.Fatalf("retired project accepted a callback epoch: %v", err)
	}
	agentPayload, err := json.Marshal(model.Agent{
		SchemaVersion: model.AgentSchemaVersion, ProjectID: projectID, AgentID: "coder-agentir", Role: model.AgentRoleCoding,
		Enabled: true, RecommendedReasoning: model.ReasoningHigh, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(ctx, LocalAgent{
		ProjectID: projectID,
		AgentID:   "coder-agentir",
		Payload:   agentPayload,
		UpdatedAt: now.Format(time.RFC3339Nano),
	}); !errors.Is(err, ErrLocalProjectRetired) {
		t.Fatalf("retired project accepted a Local Agent projection: %v", err)
	}
	if _, err := db.ReadLocalAgent(ctx, projectID, "coder-agentir"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired Local Agent projection remained visible: %v", err)
	}
	if _, err := db.ReadLocalSession(ctx, session.ID); !errors.Is(err, ErrLocalSessionNotFound) {
		t.Fatalf("retired Local Session remained visible: %v", err)
	}
	if sessions, err := db.ListLocalSessions(ctx); err != nil || len(sessions) != 0 {
		t.Fatalf("retired Local Sessions remain visible: sessions=%#v err=%v", sessions, err)
	}
	if _, err := db.ReadLocalOperation(ctx, operation.OperationID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired Local Operation remained visible: %v", err)
	}
	if operations, err := db.ListLocalOperations(ctx, projectID); err != nil || len(operations) != 0 {
		t.Fatalf("retired Local Operations remain visible: operations=%#v err=%v", operations, err)
	}
	if _, found, err := db.ReadTaskExecutionState(ctx, projectID, "AIR-TSK1"); err != nil || found {
		t.Fatalf("retired TaskExecution state remained visible: found=%v err=%v", found, err)
	}
	if states, err := db.ListTaskExecutionStates(ctx, projectID); err != nil || len(states) != 0 {
		t.Fatalf("retired TaskExecution states remain visible: states=%#v err=%v", states, err)
	}
	if _, err := db.ReadCallbackEpoch(ctx, "epoch-completed"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired callback epoch remained visible: %v", err)
	}
	if epochs, err := db.PendingCallbackEpochs(ctx, 20); err != nil || len(epochs) != 0 {
		t.Fatalf("retired callback epochs remain visible: epochs=%#v err=%v", epochs, err)
	}
}

func TestLocalProjectRetirementBlocksNonterminalOperationExecutionAndCallback(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	for _, name := range []string{"operation", "execution", "callback"} {
		t.Run(name, func(t *testing.T) {
			db, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			switch name {
			case "operation":
				if _, err := db.AllocateLocalOperation(ctx, "agentir", "AIR", strings.Repeat("a", 64), "task-authoring-update", now); err != nil {
					t.Fatal(err)
				}
			case "execution":
				if err := db.CreateTaskExecutionState(ctx, model.TaskExecutionState{
					TaskID: "AIR-TSK1", ProjectID: "agentir", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
					Status: model.TaskExecutionDispatched, Stage: "code", Worktree: "WT-TSK1-cccccccc", BaseHead: strings.Repeat("b", 40),
					Head: strings.Repeat("c", 40), Branch: "task/AIR-TSK1-retirement", Agent: "coder-agentir", ExecutionRevision: 1, UpdatedAt: now,
				}); err != nil {
					t.Fatal(err)
				}
			case "callback":
				if err := db.ArmCallbackEpoch(ctx, CallbackEpoch{
					ID:         "epoch-pending",
					ProjectID:  "agentir",
					AgentID:    "coder-agentir",
					SessionID:  "HOM_GTW_W_ABCDEFGH",
					SessionKey: "agentir_worker",
					ArmedAt:    now,
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Local.Exec(ctx, `UPDATE local_callback_epochs SET emitted_at=?,hook_outcome='pending' WHERE epoch_id=?`, now.Format(time.RFC3339Nano), "epoch-pending"); err != nil {
					t.Fatal(err)
				}
			}
			retirement := model.ProjectRetirement{
				SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: "agentir", Revision: 1,
				Reason: "stale project retirement", Actor: "gatewayd", RetiredAt: now,
				CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(nil),
			}
			if err := db.BeginLocalProjectRetirement(ctx, retirement); !errors.Is(err, ErrProjectRetirementUnsafe) {
				t.Fatalf("retirement with nonterminal %s authority err=%v", name, err)
			}
		})
	}
}

func TestDebugProjectRetirementAcceptsOnlyExactLegacyCallbackEpochs(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)

	t.Run("exact legacy set is evidenced and preserved", func(t *testing.T) {
		db, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for i := 1; i <= 13; i++ {
			insertProjectRetirementCallbackEpoch(t, db, fmt.Sprintf("legacy-%02d", i), "reposuite-mcp", now.Add(time.Duration(i)*time.Second), nil, "", nil, "", now)
		}
		evidence, err := db.ReadProjectRetirementLegacyEpochEvidence(ctx, "reposuite-mcp")
		if err != nil || evidence.Count != 13 || evidence.SHA256 == "" {
			t.Fatalf("legacy epoch evidence=%#v err=%v", evidence, err)
		}
		retirement := model.ProjectRetirement{
			SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: "reposuite-mcp", Revision: 1,
			Reason: "owner-retired project", Actor: "gatewayd", RetiredAt: now,
			CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(nil),
			LegacyCallbackEpochCount:    evidence.Count, LegacyCallbackEpochSHA256: evidence.SHA256,
		}
		if err := db.BeginLocalProjectRetirement(ctx, retirement); !errors.Is(err, ErrProjectRetirementUnsafe) {
			t.Fatalf("ordinary retirement gate accepted legacy epochs: %v", err)
		}
		if err := db.BeginDebugProjectRetirementWithLegacyEpochEvidence(ctx, retirement); err != nil {
			t.Fatalf("debug retirement rejected exact legacy epochs: %v", err)
		}
		if err := db.BeginLocalProjectRetirement(ctx, retirement); err != nil {
			t.Fatalf("idempotent retirement replay rejected recorded legacy evidence: %v", err)
		}
		retained, err := db.ReadProjectRetirementLegacyEpochEvidence(ctx, "reposuite-mcp")
		if err != nil || retained != evidence {
			t.Fatalf("legacy epochs were not preserved: got=%#v want=%#v err=%v", retained, evidence, err)
		}
		rows, err := db.Local.Query(ctx, `SELECT COUNT(*),SUM(emitted_at IS NOT NULL),COUNT(operation_id),MIN(hook_outcome),COUNT(hook_completed_at),SUM(session_id='') FROM local_callback_epochs WHERE project_id=?`, "reposuite-mcp")
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(13) || rows.Rows[0][1] != int64(13) || rows.Rows[0][2] != int64(0) || rows.Rows[0][3] != "" || rows.Rows[0][4] != int64(0) || rows.Rows[0][5] != int64(13) {
			t.Fatalf("retirement mutated legacy callback rows: rows=%#v err=%v", rows, err)
		}
	})

	for _, tc := range []struct {
		name          string
		emittedAt     any
		operationID   any
		outcome       string
		hookCompleted any
		sessionID     string
	}{
		{name: "operation link", emittedAt: now.Format(time.RFC3339Nano), operationID: "GTW-OPR1", outcome: ""},
		{name: "session link", emittedAt: now.Format(time.RFC3339Nano), outcome: "", sessionID: "HOM_GTW_W_ABCDEFGH"},
		{name: "hook completion timestamp", emittedAt: now.Format(time.RFC3339Nano), outcome: "", hookCompleted: now.Format(time.RFC3339Nano)},
		{name: "nonempty hook outcome", emittedAt: now.Format(time.RFC3339Nano), outcome: "pending"},
		{name: "missing emitted timestamp", outcome: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			insertProjectRetirementCallbackEpoch(t, db, "near-miss", "reposuite-mcp", tc.emittedAt, tc.operationID, tc.outcome, tc.hookCompleted, tc.sessionID, now)
			evidence, err := db.ReadProjectRetirementLegacyEpochEvidence(ctx, "reposuite-mcp")
			if err != nil || evidence.Count != 0 {
				t.Fatalf("near-miss epoch entered legacy evidence: evidence=%#v err=%v", evidence, err)
			}
			retirement := model.ProjectRetirement{
				SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: "reposuite-mcp", Revision: 1,
				Reason: "owner-retired project", Actor: "gatewayd", RetiredAt: now,
				CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(nil),
			}
			if err := db.BeginDebugProjectRetirementWithLegacyEpochEvidence(ctx, retirement); !errors.Is(err, ErrProjectRetirementUnsafe) {
				t.Fatalf("debug retirement did not hard-block near-miss epoch: %v", err)
			}
		})
	}

	for _, name := range []string{"active Session", "nonterminal Operation"} {
		t.Run(name+" remains a blocker", func(t *testing.T) {
			db, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			insertProjectRetirementCallbackEpoch(t, db, "legacy-linked", "reposuite-mcp", now.Format(time.RFC3339Nano), nil, "", nil, "", now)
			if name == "active Session" {
				if err := db.CreateLocalSession(ctx, LocalSession{
					ID:          "HOM_GTW_W_ABCDEFGH",
					ProjectID:   "reposuite-mcp",
					SessionType: "chatgpt",
					Payload:     []byte(`{"project_id":"reposuite-mcp","session_type":"chatgpt","status":"active"}`),
					UpdatedAt:   now.Format(time.RFC3339Nano),
					Status:      "active",
				}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := db.AllocateLocalOperation(ctx, "reposuite-mcp", "RSM", strings.Repeat("a", 64), "task-authoring-update", now); err != nil {
				t.Fatal(err)
			}
			evidence, err := db.ReadProjectRetirementLegacyEpochEvidence(ctx, "reposuite-mcp")
			if err != nil {
				t.Fatal(err)
			}
			retirement := model.ProjectRetirement{
				SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: "reposuite-mcp", Revision: 1,
				Reason: "owner-retired project", Actor: "gatewayd", RetiredAt: now,
				CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(nil),
				LegacyCallbackEpochCount:    evidence.Count, LegacyCallbackEpochSHA256: evidence.SHA256,
			}
			if err := db.BeginDebugProjectRetirementWithLegacyEpochEvidence(ctx, retirement); !errors.Is(err, ErrProjectRetirementUnsafe) {
				t.Fatalf("debug retirement bypassed %s: %v", name, err)
			}
		})
	}

	for _, outcome := range []string{"completed", "failed", "unbound"} {
		t.Run("normal terminal outcome "+outcome, func(t *testing.T) {
			db, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			insertProjectRetirementCallbackEpoch(t, db, "terminal", "reposuite-mcp", now.Format(time.RFC3339Nano), nil, outcome, nil, "", now)
			retirement := model.ProjectRetirement{
				SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: "reposuite-mcp", Revision: 1,
				Reason: "owner-retired project", Actor: "gatewayd", RetiredAt: now,
				CancelledConfigOutboxSHA256: pendingProjectConfigurationDigest(nil),
			}
			if err := db.BeginLocalProjectRetirement(ctx, retirement); err != nil {
				t.Fatalf("normal terminal epoch was newly blocked: %v", err)
			}
		})
	}
}

func insertProjectRetirementCallbackEpoch(t *testing.T, db *Databases, id, projectID string, emittedAt, operationID any, outcome string, hookCompletedAt any, sessionID string, now time.Time) {
	t.Helper()
	if at, ok := emittedAt.(time.Time); ok {
		emittedAt = at.Format(time.RFC3339Nano)
	}
	_, err := db.Local.Exec(context.Background(), `INSERT INTO local_callback_epochs(epoch_id,project_id,agent_id,session_key,armed_at,busy_seen,idle_observations,emitted_at,operation_id,hook_outcome,hook_completed_at,session_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, projectID, "coder-"+projectID, "project_worker", now.Format(time.RFC3339Nano), 1, 2, emittedAt, operationID, outcome, hookCompletedAt, sessionID)
	if err != nil {
		t.Fatal(err)
	}
}

func projectRetirementLegacyPayload(t *testing.T, projectID string, revision int64) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"schema_version": 2, "project_id": projectID, "revision": revision, "legacy": true})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
