package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONRejectsSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "gpt-tunnel")); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(root, ProtocolRoot+"/x.json", map[string]any{"x": true}); err == nil {
		t.Fatal("symlink traversal accepted")
	}
}
