package upgrade

import (
	"fmt"
	"os"
	"testing"
)

func TestReplaceAllStagesBeforeCommitAndRestoresCommitFailure(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalRename, originalCopy := stageRename, stageCopy
	t.Cleanup(func() { stageRename, stageCopy = originalRename, originalCopy })
	commits := 0
	stageRename = func(src, dst string) error {
		commits++
		if commits == 2 {
			return os.ErrPermission
		}
		return os.Rename(src, dst)
	}
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("commit failure accepted")
	}
	for _, name := range binaryOrder {
		got, err := os.ReadFile(paths[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(old[name]) {
			t.Fatalf("%s was not restored", name)
		}
	}
}

func TestReplaceAllSucceedsAndCoversEveryStagePosition(t *testing.T) {
	for _, position := range binaryOrder {
		t.Run(position, func(t *testing.T) {
			release, paths, old := makeUpgradeFixtures(t)
			originalCopy := stageCopy
			t.Cleanup(func() { stageCopy = originalCopy })
			calls := 0
			stageCopy = func(src, dst string) (string, error) {
				calls++
				if binaryOrder[calls-1] == position {
					return "", os.ErrPermission
				}
				return stageOne(src, dst)
			}
			if err := replaceAll(release, paths, old); err == nil {
				t.Fatal("stage failure accepted")
			}
			for _, name := range binaryOrder {
				got, _ := os.ReadFile(paths[name])
				if string(got) != string(old[name]) {
					t.Fatalf("%s changed", name)
				}
			}
		})
	}
	// A normal transaction commits all three staged files in deterministic order.
	release, paths, old := makeUpgradeFixtures(t)
	if err := replaceAll(release, paths, old); err != nil {
		t.Fatal(err)
	}
	for _, name := range binaryOrder {
		got, _ := os.ReadFile(paths[name])
		if string(got) != "#!/bin/sh\nprintf '0.2.3\\n'\n" {
			t.Fatalf("%s was not committed", name)
		}
	}
}

func TestReplaceAllRenameFailureAfterEachCommitRestoresAll(t *testing.T) {
	for failure := 1; failure <= len(binaryOrder); failure++ {
		t.Run(fmt.Sprintf("rename-%d", failure), func(t *testing.T) {
			release, paths, old := makeUpgradeFixtures(t)
			originalRename := stageRename
			t.Cleanup(func() { stageRename = originalRename })
			calls := 0
			stageRename = func(src, dst string) error {
				calls++
				if calls == failure {
					return os.ErrPermission
				}
				return os.Rename(src, dst)
			}
			if err := replaceAll(release, paths, old); err == nil {
				t.Fatal("rename failure accepted")
			}
			for _, name := range binaryOrder {
				got, _ := os.ReadFile(paths[name])
				if string(got) != string(old[name]) {
					t.Fatalf("%s was not restored", name)
				}
			}
		})
	}
}
