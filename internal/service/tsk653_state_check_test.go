package service

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestTSK653StateCheckAcceptsManagedProjectWithoutLegacyRelay(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	current := config.EmptyManagedProjectRegistry()
	digest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := config.ManagedProjectRegistry{
		SchemaVersion: config.ManagedProjectRegistrySchemaVersion,
		Revision:      1,
		Projects: map[string]config.ManagedProjectEntry{
			"air": {
				Root:          root,
				RepositoryURL: "git@example.invalid:air.git",
				Remote:        "origin",
				DefaultBranch: "main",
				ProjectCode:   "AIR",
			},
		},
	}
	if _, err := config.WriteManagedProjectRegistry(stateDir, digest, registry); err != nil {
		t.Fatal(err)
	}
	result, err := New(config.Config{StateDir: stateDir}).StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || strings.Join(result.ConfiguredProjectIDs, ",") != "air" || strings.Join(result.DurableProjectIDs, ",") != "air" {
		t.Fatalf("managed AIR project state check=%#v", result)
	}
}

func TestTSK653StateCheckSeparatesManagedAndStaticRelayRequirements(t *testing.T) {
	stateDir := t.TempDir()
	managedRoot := t.TempDir()
	staticRoot := t.TempDir()
	staticMirror := t.TempDir()
	managed := config.ManagedProjectEntry{
		Root:          managedRoot,
		RepositoryURL: "git@example.invalid:air.git",
		Remote:        "origin",
		DefaultBranch: "main",
		ProjectCode:   "AIR",
	}
	resolution := ProjectResolution{
		Projects: map[string]config.ProjectConfig{
			"air": {
				Root:          managed.Root,
				Mirror:        config.ManagedProjectMirrorPath(stateDir, "air"),
				Remote:        managed.Remote,
				DefaultBranch: managed.DefaultBranch,
				ProjectCode:   managed.ProjectCode,
			},
			"missing-code": {
				Root:          managedRoot,
				Mirror:        config.ManagedProjectMirrorPath(stateDir, "missing-code"),
				Remote:        managed.Remote,
				DefaultBranch: managed.DefaultBranch,
			},
			"static-missing-relay": {
				Root:          staticRoot,
				Mirror:        staticMirror,
				Remote:        "origin",
				DefaultBranch: "main",
			},
			"static-ready": {
				Root:              staticRoot,
				Mirror:            staticMirror,
				Remote:            "origin",
				DefaultBranch:     "main",
				AirelaySessionKey: "static_worker",
			},
		},
		ManagedProjects: map[string]config.ManagedProjectEntry{
			"air":          managed,
			"missing-code": {Root: managedRoot, RepositoryURL: managed.RepositoryURL, Remote: managed.Remote, DefaultBranch: managed.DefaultBranch},
		},
	}
	result, err := New(config.Config{StateDir: stateDir}).stateCheckLocalWithoutDurability(StateCheckResult{}, []string{"air", "missing-code", "static-missing-relay", "static-ready"}, resolution)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid || strings.Join(result.DurableProjectIDs, ",") != "air,static-ready" {
		t.Fatalf("state check=%#v", result)
	}
	issues := map[string]string{}
	for _, issue := range result.Issues {
		issues[issue.ProjectID] = issue.Code
	}
	if issues["missing-code"] != "CONFIGURED_PROJECT_INVALID" || issues["static-missing-relay"] != "CONFIGURED_PROJECT_INVALID" || len(issues) != 2 {
		t.Fatalf("state issues=%#v", result.Issues)
	}
}
