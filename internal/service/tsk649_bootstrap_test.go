package service

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK649ProjectOnboardEstablishesCompleteSharedBootstrap(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	code := "RDX"
	result, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "onboarded" {
		t.Fatalf("onboard result=%#v", result)
	}
	assertTSK649SharedBootstrap(t, s, db, filepath.Base(root), code)
	grant, err := db.ReadSessionBootstrapGrant(context.Background(), filepath.Base(root))
	if err != nil || grant.ProjectCode != code || grant.Role != "planner" || grant.Token == "" {
		t.Fatalf("onboard bootstrap grant=%#v err=%v", grant, err)
	}
}

func TestTSK649ProjectOnboardRepairsPartialSharedBootstrapWithoutReplacingRules(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	code := "RDX"
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	firstRules, err := db.Shared.Query(context.Background(), `SELECT id,payload FROM shared_rules WHERE json_extract(payload,'$.project_id')=? ORDER BY id`, filepath.Base(root))
	if err != nil || len(firstRules.Rows) != 0 {
		t.Fatalf("pre-repair Shared rules=%#v err=%v", firstRules.Rows, err)
	}
	retry, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Status != "already_registered" {
		t.Fatalf("repair result=%#v", retry)
	}
	assertTSK649SharedBootstrap(t, s, db, filepath.Base(root), code)
	before, err := db.Shared.Query(context.Background(), `SELECT id,payload FROM shared_rules WHERE json_extract(payload,'$.project_id')=? ORDER BY id`, filepath.Base(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := db.Shared.Query(context.Background(), `SELECT id,payload FROM shared_rules WHERE json_extract(payload,'$.project_id')=? ORDER BY id`, filepath.Base(root))
	if err != nil || len(after.Rows) != len(before.Rows) {
		t.Fatalf("idempotent Shared rules before=%#v after=%#v err=%v", before.Rows, after.Rows, err)
	}
	for i := range before.Rows {
		if before.Rows[i][0] != after.Rows[i][0] || !bytes.Equal(before.Rows[i][1].([]byte), after.Rows[i][1].([]byte)) {
			t.Fatalf("existing Shared rule changed at %d: before=%#v after=%#v", i, before.Rows[i], after.Rows[i])
		}
	}
}

func assertTSK649SharedBootstrap(t *testing.T, s *Service, db *sqlitestore.Databases, projectID, code string) {
	t.Helper()
	identifiers, err := db.ReadSharedProjectIdentifiers(context.Background(), projectID)
	if err != nil || identifiers.ProjectCode != code {
		t.Fatalf("Shared identifiers=%#v err=%v", identifiers, err)
	}
	if _, err := s.ProjectConfigurationRead(context.Background(), projectID); err != nil {
		t.Fatalf("Shared configuration unavailable: %v", err)
	}
	if _, err := s.ProjectWorkflowPolicyRead(context.Background(), projectID); err != nil {
		t.Fatalf("Shared workflow policy unavailable: %v", err)
	}
	rows, err := db.Shared.Query(context.Background(), `SELECT COUNT(*) FROM shared_rules WHERE json_extract(payload,'$.project_id')=? AND json_extract(payload,'$.name') IN ('agent.wait_for_ci','ci.release','ci.task','ci.task_merge','integration_branch','workflow_stage')`, projectID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(6) {
		t.Fatalf("Shared machine-policy leaves=%#v err=%v", rows.Rows, err)
	}
}
