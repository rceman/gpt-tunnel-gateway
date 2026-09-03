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

func TestDebugActivationCandidateRequiresExactSourceAndSameVersion(t *testing.T) {
	dir := t.TempDir()
	wantSource := strings.Repeat("a", 40)
	wantVersion := "0.6.14"
	for _, name := range releaseartifacts.BinaryNames {
		path := filepath.Join(dir, name)
		contents := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n--version) printf '%s\\n' ;;\n--source-sha) printf '%s\\n' ;;\n*) exit 0 ;;\nesac\n", wantVersion, wantSource)
		if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := make([]byte, 0)
	for _, name := range releaseartifacts.BinaryNames {
		hash, err := releaseartifacts.HashFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		manifest = append(manifest, []byte(fmt.Sprintf("%s  %s\n", hash, name))...)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := releaseartifacts.ValidateRelease(dir, wantVersion); err != nil {
		t.Fatalf("same-version candidate rejected: %v", err)
	}
	if err := validateReleaseSource(dir, wantSource); err != nil {
		t.Fatalf("exact-source candidate rejected: %v", err)
	}
	if err := releaseartifacts.ValidateRelease(dir, "0.6.15"); err == nil {
		t.Fatal("different-version candidate was accepted")
	}
}
