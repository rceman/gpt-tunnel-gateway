package activation

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestRecoverySnapshotRestoresMatchingArtifactsAndDurableState(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "release")
	if err := os.Mkdir(releaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const version = "1.2.3"
	const sourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writeTestRelease(t, releaseDir, version, sourceSHA)
	previous := make(map[string][]byte, len(releaseartifacts.BinaryNames))
	for _, name := range releaseartifacts.BinaryNames {
		previous[name] = testReleaseBinary("0.9.0", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	}
	stateDir := filepath.Join(root, "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, store := range []*upstream.Store{db.Shared, db.Local} {
		if _, err := store.Exec(ctx, `CREATE TABLE activation_probe (value TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Exec(ctx, `INSERT INTO activation_probe(value) VALUES('before')`); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	installedDir := filepath.Join(root, "installed")
	if err := os.Mkdir(installedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := releaseartifacts.Paths(filepath.Join(installedDir, "gpt-tunnel-gatewayd"))
	for _, name := range releaseartifacts.BinaryNames {
		path := paths[name]
		if err := os.WriteFile(path, previous[name], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := CreateRecoverySnapshot(filepath.Join(root, "pid"), releaseDir, version, sourceSHA, previous)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Cleanup()
	if err := snapshot.CaptureDurableState(ctx, stateDir); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.ReplaceCandidate(paths); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.VerifyCandidate(paths); err != nil {
		t.Fatal(err)
	}
	db, err = sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*upstream.Store{db.Shared, db.Local} {
		if _, err := store.Exec(ctx, `INSERT INTO activation_probe(value) VALUES('after')`); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.RestoreDurableState(stateDir); err != nil {
		t.Fatal(err)
	}
	db, err = sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*upstream.Store{db.Shared, db.Local} {
		rows, err := store.Query(ctx, `SELECT value FROM activation_probe ORDER BY rowid`)
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "before" {
			t.Fatalf("restored durable state rows=%#v err=%v", rows, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.RestorePrevious(paths); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.VerifyPrevious(paths); err != nil {
		t.Fatal(err)
	}
	for _, name := range releaseartifacts.BinaryNames {
		got, err := os.ReadFile(paths[name])
		if err != nil || !bytes.Equal(got, previous[name]) {
			t.Fatalf("restored %s differs from the matching previous artifact: err=%v", name, err)
		}
	}
}

func TestRecoverySnapshotRejectsCorruptPreviousArtifact(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "release")
	if err := os.Mkdir(releaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const version = "1.2.3"
	const sourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writeTestRelease(t, releaseDir, version, sourceSHA)
	previous := make(map[string][]byte, len(releaseartifacts.BinaryNames))
	for _, name := range releaseartifacts.BinaryNames {
		previous[name] = testReleaseBinary("0.9.0", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	}
	snapshot, err := CreateRecoverySnapshot(filepath.Join(root, "pid"), releaseDir, version, sourceSHA, previous)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Cleanup()
	corruptPath := filepath.Join(snapshot.previousDir, releaseartifacts.BinaryNames[0])
	if err := os.WriteFile(corruptPath, []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.PreviousArtifacts(); err == nil || !strings.Contains(err.Error(), "failed verification") {
		t.Fatalf("corrupt previous artifact error=%v", err)
	}
}

func writeTestRelease(t *testing.T, directory, version, sourceSHA string) {
	t.Helper()
	var manifest strings.Builder
	for _, name := range releaseartifacts.BinaryNames {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, testReleaseBinary(version, sourceSHA), 0o700); err != nil {
			t.Fatal(err)
		}
		checksum, err := releaseartifacts.HashFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&manifest, "%s %s\n", checksum, name)
	}
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(manifest.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testReleaseBinary(version, sourceSHA string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n--version) printf '%%s\\n' '%s' ;;\n--source-sha) printf '%%s\\n' '%s' ;;\n*) exit 2 ;;\nesac\n", version, sourceSHA))
}
