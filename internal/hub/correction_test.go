package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONRejectsSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "protocol")); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(root, "protocol/v4/x.json", map[string]any{"x": true}); err == nil {
		t.Fatal("symlink traversal accepted")
	}
}
