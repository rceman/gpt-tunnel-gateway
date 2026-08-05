package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func managedServiceTestEntry(root, projectID string) config.ManagedProjectEntry {
	return config.ManagedProjectEntry{
		Root:              root,
		RepositoryURL:     "git@example.invalid:" + projectID + ".git",
		Remote:            "origin",
		DefaultBranch:     "main",
		AirelaySessionKey: projectID + "_master",
	}
}

func writeManagedServiceTestRegistry(t *testing.T, s *Service, projects map[string]config.ManagedProjectEntry) config.ManagedProjectRegistry {
	t.Helper()
	current, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		t.Fatalf("load current managed registry: %v", err)
	}
	expected, err := current.Digest()
	if err != nil {
		t.Fatalf("digest current managed registry: %v", err)
	}
	next := config.ManagedProjectRegistry{SchemaVersion: config.ManagedProjectRegistrySchemaVersion, Revision: current.Revision + 1, Projects: projects}
	if _, err := config.WriteManagedProjectRegistry(s.Config.StateDir, expected, next); err != nil {
		t.Fatalf("write managed registry: %v", err)
	}
	return next
}

func TestProjectResolutionAbsentRegistryPreservesStaticBehaviorAndFreshMaps(t *testing.T) {
	s, _, _ := testService(t)
	path := config.ManagedProjectRegistryPath(s.Config.StateDir)
	resolution, err := s.resolveProjects()
	if err != nil {
		t.Fatalf("resolve static projects without registry: %v", err)
	}
	if resolution.ManagedRegistryRevision != 0 || len(resolution.Projects) != 1 {
		t.Fatalf("unexpected absent-registry resolution: %#v", resolution)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("read resolution created registry, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Config.StateDir, "locks")); !os.IsNotExist(err) {
		t.Fatalf("read resolution created registry lock directory, stat error=%v", err)
	}

	resolution.Projects["example"] = config.ProjectConfig{}
	next, err := s.resolveProjects()
	if err != nil {
		t.Fatalf("resolve static projects again: %v", err)
	}
	if next.Projects["example"].Root != s.Config.Projects["example"].Root {
		t.Fatalf("effective map mutation changed service config")
	}
}

func TestProjectResolutionManagedEntryAndRevisionReplacementAreDynamic(t *testing.T) {
	s, _, _ := testService(t)
	rootOne := t.TempDir()
	rootTwo := t.TempDir()
	entryOne := managedServiceTestEntry(rootOne, "managed")
	registry := writeManagedServiceTestRegistry(t, s, map[string]config.ManagedProjectEntry{"managed": entryOne})

	project, err := s.projectConfig("managed")
	if err != nil {
		t.Fatalf("resolve managed project without recreating service: %v", err)
	}
	if project.Root != rootOne {
		t.Fatalf("managed root = %q, want %q", project.Root, rootOne)
	}
	ids, resolution, err := s.effectiveProjectIDs()
	if err != nil {
		t.Fatalf("resolve effective project IDs: %v", err)
	}
	if strings.Join(ids, ",") != "example,managed" {
		t.Fatalf("effective project IDs = %v", ids)
	}
	if resolution.ManagedRegistryRevision != registry.Revision || resolution.ManagedRegistryDigest == "" {
		t.Fatalf("missing managed registry metadata: %#v", resolution)
	}

	current, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		t.Fatalf("load registry for replacement: %v", err)
	}
	expected, err := current.Digest()
	if err != nil {
		t.Fatalf("digest registry for replacement: %v", err)
	}
	replacement := config.ManagedProjectRegistry{SchemaVersion: config.ManagedProjectRegistrySchemaVersion, Revision: current.Revision + 1, Projects: map[string]config.ManagedProjectEntry{"managed": managedServiceTestEntry(rootTwo, "managed")}}
	if _, err := config.WriteManagedProjectRegistry(s.Config.StateDir, expected, replacement); err != nil {
		t.Fatalf("replace managed registry: %v", err)
	}
	project, err = s.projectConfig("managed")
	if err != nil {
		t.Fatalf("resolve replaced managed project: %v", err)
	}
	if project.Root != rootTwo {
		t.Fatalf("replaced managed root = %q, want %q", project.Root, rootTwo)
	}
	_, resolution, err = s.effectiveProjectIDs()
	if err != nil {
		t.Fatalf("resolve replaced effective IDs: %v", err)
	}
	if resolution.ManagedRegistryRevision != replacement.Revision {
		t.Fatalf("replacement revision = %d, want %d", resolution.ManagedRegistryRevision, replacement.Revision)
	}
}

func TestProjectResolutionFailsClosedWithoutStaticFallback(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "malformed", data: "{"},
		{name: "null", data: `{"schema_version":1,"revision":0,"projects":null}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, _, _ := testService(t)
			if err := os.MkdirAll(s.Config.StateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := config.ManagedProjectRegistryPath(s.Config.StateDir)
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.projectConfig("example"); err == nil {
				t.Fatalf("%s registry error silently fell back to static config", test.name)
			}
		})
	}

	s, _, _ := testService(t)
	staticRoot := s.Config.Projects["example"].Root
	writeManagedServiceTestRegistry(t, s, map[string]config.ManagedProjectEntry{"managed": managedServiceTestEntry(staticRoot, "managed")})
	if _, err := s.projectConfig("example"); err == nil || !strings.Contains(err.Error(), "duplicate project root") {
		t.Fatalf("registry root collision was not fail-closed: %v", err)
	}
}

func TestValidateConfiguredProjectRecordsRequiresManagedProjectAndPlan(t *testing.T) {
	s, _, _ := testService(t)
	root := t.TempDir()
	writeManagedServiceTestRegistry(t, s, map[string]config.ManagedProjectEntry{"managed": managedServiceTestEntry(root, "managed")})
	if err := s.ValidateConfiguredProjectRecords(context.Background()); err == nil || !strings.Contains(err.Error(), "managed") {
		t.Fatalf("missing managed durable record was not rejected: %v", err)
	}

	hubRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	project := model.Project{SchemaVersion: 1, ID: "managed", RepositoryURL: "git@example.invalid:managed.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "b1a45b1e9475ab29dfd3e84d523b70897c7b8918", Status: "active"}
	if _, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{Project: project, WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}}); err != nil {
		t.Fatalf("register managed durable project: %v", err)
	}
	if err := s.ValidateConfiguredProjectRecords(context.Background()); err != nil {
		t.Fatalf("managed durable project and plan were not accepted: %v", err)
	}
}
