package sqlitestore

import (
	"context"
	"testing"
)

func TestSessionBootstrapGrantIsDurableAndUniquePerProject(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := db.EnsureSessionBootstrapGrant(ctx, SessionBootstrapGrant{
		ProjectID:   "example",
		ProjectCode: "EXM",
		GatewayID:   "HOM",
		Role:        "planner",
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	second, err := db.EnsureSessionBootstrapGrant(ctx, SessionBootstrapGrant{
		ProjectID:   "example",
		ProjectCode: "EXM",
		GatewayID:   "HOM",
		Role:        "planner",
	})
	if err != nil || second.Token != first.Token {
		db.Close()
		t.Fatalf("retry grant=%#v first=%#v err=%v", second, first, err)
	}
	byToken, err := db.ReadSessionBootstrapGrantByToken(ctx, first.Token)
	if err != nil || byToken.ProjectID != "example" || byToken.Role != "planner" {
		db.Close()
		t.Fatalf("token lookup=%#v err=%v", byToken, err)
	}
	if _, err := db.EnsureSessionBootstrapGrant(ctx, SessionBootstrapGrant{
		ProjectID:   "example",
		ProjectCode: "ZZZ",
		GatewayID:   "HOM",
		Role:        "planner",
	}); err == nil {
		db.Close()
		t.Fatal("conflicting project grant was accepted")
	}
	if _, err := db.EnsureSessionBootstrapGrant(ctx, SessionBootstrapGrant{
		ProjectID:   "other",
		ProjectCode: "OTH",
		GatewayID:   "HOM",
		Role:        "planner",
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reopened, err := db.ReadSessionBootstrapGrant(ctx, "example")
	if err != nil || reopened.Token != first.Token {
		t.Fatalf("reopened grant=%#v first=%#v err=%v", reopened, first, err)
	}
	rows, err := db.Local.Query(ctx, `SELECT COUNT(*) FROM local_session_bootstrap_grants`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(2) {
		t.Fatalf("grant count=%#v err=%v", rows.Rows, err)
	}
}
