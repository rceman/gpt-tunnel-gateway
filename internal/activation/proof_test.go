package activation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestProveSourceRejectsInvalidSourceBeforeAnyActivation(t *testing.T) {
	_, err := ProveSource(context.Background(), config.Config{}, "", config.ProjectConfig{}, "")
	if err == nil || err.Error() != "activation source is incomplete" {
		t.Fatalf("ProveSource error = %v", err)
	}
}

func TestSHA256FileReturnsExactArtifactDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway")
	content := []byte("exact source artifact")
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(content))
	got, err := sha256File(path)
	if err != nil || got != want {
		t.Fatalf("sha256File = %q, %v; want %q", got, err, want)
	}
}
