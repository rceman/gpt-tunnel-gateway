package main

import (
	"context"
	"path"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK667LiveGatewayDrainsExistingTextRelationOutbox(t *testing.T) {
	ctx := context.Background()
	relations := []model.Relation{
		{
			SchemaVersion: model.RelationSchemaVersion,
			ProjectID:     "example",
			Kind:          model.RelationKindCorrects,
			Source:        "EXM-TSK101",
			Target:        "EXM-TSK102",
			CreatedAt:     time.Date(2026, 9, 25, 13, 30, 0, 0, time.UTC),
			CreatedBy:     "planner",
		},
		{
			SchemaVersion: model.RelationSchemaVersion,
			ProjectID:     "example",
			Kind:          model.RelationKindCorrects,
			Source:        "EXM-TSK103",
			Target:        "EXM-TSK104",
			CreatedAt:     time.Date(2026, 9, 25, 13, 31, 0, 0, time.UTC),
			CreatedBy:     "planner",
		},
	}
	operationIDs := []string{"relation-" + relations[0].Identity(), "relation-" + relations[1].Identity()}
	gateway := testutil.NewLiveGateway(t, testutil.LiveGatewayHooks{
		BeforeStart: func(gateway *testutil.LiveGateway) {
			if err := (hub.Store{Config: gateway.Config}).Ensure(ctx); err != nil {
				t.Fatalf("initialize disposable Hub: %v", err)
			}
			db, err := sqlitestore.Open(gateway.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, relation := range relations {
				created, err := db.CreateRelation(ctx, false, relation.ProjectID, relation.Kind, relation.Source, relation.Target, relation.CreatedBy, relation.CreatedAt)
				if err != nil || !created {
					t.Fatalf("seed existing Shared relation: created=%v err=%v", created, err)
				}
			}
			if _, err := db.Shared.Exec(ctx, `UPDATE hub_outbox SET payload=CAST(payload AS TEXT) WHERE id=?`, operationIDs[0]); err != nil {
				t.Fatal(err)
			}
			rows, err := db.Shared.Query(ctx, `SELECT typeof(payload) FROM hub_outbox WHERE id=?`, operationIDs[0])
			if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "text" {
				t.Fatalf("disposable legacy relation row is not SQLite TEXT: rows=%#v err=%v", rows.Rows, err)
			}
		},
	})
	defer gateway.Stop()

	hubStore := hub.Store{Config: gateway.Config}
	deadline := time.Now().Add(20 * time.Second)
	allPublished := false
	var lastErr error
	for time.Now().Before(deadline) {
		if !gateway.DaemonRunning() {
			t.Fatalf("gatewayd exited before draining relation rows: %s", gateway.DaemonStderr())
		}
		allPublished = true
		for _, relation := range relations {
			relationPath := path.Join(hub.ProtocolRoot, "projects", relation.ProjectID, "relations", relation.Kind, relation.Source, relation.Target+".json")
			var published model.Relation
			lastErr = hubStore.ReadJSON(ctx, relationPath, &published)
			if lastErr != nil {
				allPublished = false
				break
			}
			if published != relation {
				t.Fatalf("published relation=%#v want=%#v", published, relation)
			}
		}
		if allPublished {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !allPublished {
		t.Fatalf("live daemon did not publish all queued relation rows: %v\nstderr=%s", lastErr, gateway.DaemonStderr())
	}
	time.Sleep(300 * time.Millisecond)
	gateway.Stop()

	db, err := sqlitestore.Open(gateway.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for index, operationID := range operationIDs {
		entry, found, err := db.ReadSharedOutboxEntry(ctx, operationID)
		if err != nil || !found || entry.PublishedAt == "" {
			t.Fatalf("canonical worker did not mark outbox row %s published: entry=%#v found=%v err=%v", operationID, entry, found, err)
		}
		rows, err := db.Shared.Query(ctx, `SELECT typeof(payload) FROM hub_outbox WHERE id=?`, operationID)
		storageClass := "blob"
		if index == 0 {
			storageClass = "text"
		}
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != storageClass {
			t.Fatalf("worker changed existing payload storage for %s: rows=%#v err=%v", operationID, rows.Rows, err)
		}
	}
	pending, err := db.PendingOutbox(ctx, 32)
	if err != nil || len(pending) != 0 {
		t.Fatalf("shared outbox did not drain after publication: pending=%#v err=%v", pending, err)
	}
}
