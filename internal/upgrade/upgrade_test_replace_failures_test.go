package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceAllDirectorySyncFailureCleansStaging(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalSync := stageSyncDir
	t.Cleanup(func() { stageSyncDir = originalSync })
	stageSyncDir = func(string) error { return os.ErrPermission }
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("sync failure accepted")
	}
	for _, name := range binaryOrder {
		got, _ := os.ReadFile(paths[name])
		if string(got) != string(old[name]) {
			t.Fatalf("%s was not restored", name)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(paths["gpt-tunnel"]))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gpt-tunnel-upgrade-stage-") {
			t.Fatalf("staging file remains: %s", entry.Name())
		}
	}
}

func TestReplaceAllPropagatesStagingCleanupFailure(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalCopy, originalRemove := stageCopy, stageRemove
	t.Cleanup(func() { stageCopy, stageRemove = originalCopy, originalRemove })
	calls := 0
	stageCopy = func(src, dst string) (string, error) {
		calls++
		if calls == 1 {
			return stageOne(src, dst)
		}
		return "", os.ErrPermission
	}
	stageRemove = func(string) error { return os.ErrPermission }
	if err := replaceAll(release, paths, old); err == nil || !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("cleanup failure not propagated: %v", err)
	}
}

func TestReplaceAllStageFailureCleansStaging(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalCopy := stageCopy
	t.Cleanup(func() { stageCopy = originalCopy })
	calls := 0
	stageCopy = func(src, dst string) (string, error) {
		calls++
		if calls == 2 {
			return "", os.ErrPermission
		}
		return stageOne(src, dst)
	}
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("stage failure accepted")
	}
	entries, err := os.ReadDir(filepath.Dir(paths["gpt-tunnel"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnel-gatewayd"]) && filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnel"]) && filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnelctl"]) {
			if len(entry.Name()) > 0 && entry.Name()[0] == '.' {
				t.Fatalf("staging file remains: %s", entry.Name())
			}
		}
	}
}
