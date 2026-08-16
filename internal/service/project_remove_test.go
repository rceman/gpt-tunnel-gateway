package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func registerManagedRemovalProject(t *testing.T) (*Service, string, string) {
	t.Helper()
	s, revision, _ := testServiceWithoutIdentifiers(t)
	root := t.TempDir()
	writeManagedServiceTestRegistry(t, s, map[string]config.ManagedProjectEntry{"removable": managedServiceTestEntry(root, "removable")})
	project := model.Project{
		SchemaVersion: 1, ID: "removable", RepositoryURL: "git@example.invalid:removable.git",
		DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner",
		WorkflowCommit: strings.Repeat("a", 40), Status: "active",
	}
	registered, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{
		Project: project,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatalf("register managed project: %v", err)
	}
	return s, root, registered.Hub.After
}

func TestProjectRemoveCleansManagedStateButPreservesExternalRootAndIsIdempotent(t *testing.T) {
	s, root, revision := registerManagedRemovalProject(t)
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	mirror := config.ManagedProjectMirrorPath(s.Config.StateDir, "removable")
	if err := os.MkdirAll(mirror, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "marker"), []byte("gateway-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := s.ProjectRemove(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectRemoveInput{
		ProjectID: "removable",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatalf("remove managed project: %v", err)
	}
	if result.AlreadyRemoved || !result.ExternalRootKept || result.Hub.After == "" {
		t.Fatalf("unexpected removal result: %#v", result)
	}
	if _, err := s.ProjectRead(context.Background(), "removable"); !IsNotFound(err) {
		t.Fatalf("removed project read error = %v, want not found", err)
	}
	if _, err := s.EffectiveProjectConfig("removable"); err == nil {
		t.Fatal("removed managed project remained in effective registry")
	}
	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Fatalf("managed mirror was not removed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "source.txt")); err != nil || string(data) != "external" {
		t.Fatalf("external source changed: data=%q err=%v", data, err)
	}
	retry, err := s.ProjectRemove(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectRemoveInput{ProjectID: "removable"})
	if err != nil {
		t.Fatalf("idempotent removal: %v", err)
	}
	if !retry.AlreadyRemoved {
		t.Fatalf("retry was not reported as already removed: %#v", retry)
	}
}

func TestProjectRemoveRefusesActiveSessionWithoutMutation(t *testing.T) {
	s, _, revision := registerManagedRemovalProject(t)
	store := session.NewStore(s.Config.StateDir)
	record, err := store.Create(session.CreateInput{ProjectID: "removable", ProjectCode: "REM", Role: session.RolePlanner, SessionType: session.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ProjectRemove(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectRemoveInput{
		ProjectID: "removable",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "PROJECT_REMOVE_ACTIVE_AUTHORITY") {
		t.Fatalf("active session was not rejected: %v", err)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || after != before {
		t.Fatalf("active-session rejection mutated Hub: before=%s after=%s err=%v", before, after, err)
	}
	if _, err := store.Get(record.ID); err != nil {
		t.Fatalf("active session disappeared after rejection: %v", err)
	}
}

func TestProjectRemoveRollsBackLocalStagingOnHubConflict(t *testing.T) {
	s, root, revision := registerManagedRemovalProject(t)
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	mirror := config.ManagedProjectMirrorPath(s.Config.StateDir, "removable")
	if err := os.MkdirAll(mirror, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "marker"), []byte("keep-on-rollback"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeRegistry, err := os.ReadFile(config.ManagedProjectRegistryPath(s.Config.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ProjectRemove(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectRemoveInput{
		ProjectID: "removable",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: "stale",
		},
	})
	if err == nil {
		t.Fatal("stale Hub revision unexpectedly removed project")
	}
	afterRegistry, err := os.ReadFile(config.ManagedProjectRegistryPath(s.Config.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRegistry) != string(beforeRegistry) {
		t.Fatal("managed registry changed after failed removal")
	}
	if _, err := os.Stat(filepath.Join(mirror, "marker")); err != nil {
		t.Fatalf("local mirror was not restored after failed removal: %v", err)
	}
	if _, err := s.ProjectRead(context.Background(), "removable"); err != nil {
		t.Fatalf("durable project disappeared after failed removal: %v", err)
	}
	_ = revision
}
