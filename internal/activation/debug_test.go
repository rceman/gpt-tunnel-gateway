package activation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestDebugActivateRequiresConfiguredMainBranchBeforeMutation(t *testing.T) {
	_, sourceRoot, sourceHead := testutil.RepoWithBareRemote(t)
	testutil.Git(t, sourceRoot, "checkout", "-b", "debug-recovery")
	_, err := DebugActivate(context.Background(), config.Config{}, "", config.ProjectConfig{Root: sourceRoot}, sourceHead)
	if err == nil || !strings.Contains(err.Error(), "requires the configured source branch to be main") {
		t.Fatalf("non-main debug activation error=%v", err)
	}
}

func TestValidateReleaseSourceRequiresExactProvenanceForEveryControlArtifact(t *testing.T) {
	dir := t.TempDir()
	want := strings.Repeat("a", 40)
	for _, name := range releaseartifacts.BinaryNames {
		path := filepath.Join(dir, name)
		contents := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--source-sha\" ]; then printf '%s\\n'; fi\n", want)
		if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateReleaseSource(dir, want); err != nil {
		t.Fatalf("exact release provenance was rejected: %v", err)
	}
	path := filepath.Join(dir, releaseartifacts.BinaryNames[0])
	contents := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--source-sha\" ]; then printf '%s\\n'; fi\n", strings.Repeat("b", 40))
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseSource(dir, want); err == nil {
		t.Fatal("mismatched release provenance was accepted")
	}
}
