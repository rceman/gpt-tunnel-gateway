package sqlitestore

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTSK538FreshSharedBaselineIsCompleteAndMinimal(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationMarker(t, db.Shared, sharedBaselineVersion, sharedBaselineName)
	assertFinalSharedSchema(t, db.Shared)
	assertAbsentObjects(t, db.Shared, []string{"shared_agents", "shared_watcher_guides", "shared_journal_sequences", "shared_journal_supersessions", "shared_integration_operations"})
	rows, err := db.Shared.Query(context.Background(), `SELECT status FROM replication_state WHERE id=1`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "synced" {
		t.Fatalf("replication seed=%#v err=%v", rows.Rows, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertMigrationMarker(t, reopened.Shared, sharedBaselineVersion, sharedBaselineName)
}

func TestTSK538FreshLocalBaselineIsCompleteAndMinimal(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertMigrationMarker(t, db.Local, localBaselineVersion, localBaselineName)
	assertFinalLocalSchema(t, db.Local)
	assertAbsentObjects(t, db.Local, []string{"local_inter_session_messages"})
}

func TestTSK538ActiveTimestampIdentitiesAreExplicitAndNonSequential(t *testing.T) {
	identities := []struct {
		version int64
		name    string
	}{
		{sharedBaselineVersion, sharedBaselineName}, {sharedBridgeVersion, sharedBridgeName},
		{localBaselineVersion, localBaselineName}, {localBridgeVersion, localBridgeName},
	}
	seen := map[int64]bool{}
	for _, identity := range identities {
		text := strconv.FormatInt(identity.version, 10)
		if len(text) != 12 {
			t.Fatalf("version %d is not YYYYMMDDHHMM", identity.version)
		}
		if _, err := time.ParseInLocation("200601021504", text, time.UTC); err != nil {
			t.Fatal(err)
		}
		if identity.name == "" || strings.Contains(identity.name, "_v") {
			t.Fatalf("bad timestamp identity=%#v", identity)
		}
		if seen[identity.version] {
			t.Fatalf("duplicate active version %d", identity.version)
		}
		seen[identity.version] = true
	}
}
