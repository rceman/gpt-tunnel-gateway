package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileBoundedRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "completion.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1025)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileBounded(path, 1024); err == nil {
		t.Fatal("oversized input accepted")
	}
}
