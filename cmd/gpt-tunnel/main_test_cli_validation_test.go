package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCancelAcknowledgeCLIArgumentsAreStrict(t *testing.T) {
	if got, err := expectedStrict([]string{"--expected-hub-revision", strings.Repeat("a", 40)}); err != nil || got != strings.Repeat("a", 40) {
		t.Fatalf("valid acknowledgement arguments rejected: value=%q err=%v", got, err)
	}
	for _, args := range [][]string{
		{"--unknown", "value"},
		{"--expected-hub-revision"},
		{"--expected-hub-revision", "one", "--expected-hub-revision", "two"},
		{"run-id-extra"},
	} {
		if _, err := expectedStrict(args); err == nil {
			t.Fatalf("invalid acknowledgement arguments accepted: %#v", args)
		}
	}
}

func TestOnboardingIsNotExposedByCLI(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, forbidden := range []string{"onboard", "onboard-status", "onboard-recover", "ProjectOnboard"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("onboarding CLI surface contains %q", forbidden)
		}
	}
}

func TestWriteCompletionCLIArgumentsAreStrict(t *testing.T) {
	if got, err := completionFileStrict([]string{"--completion-file", "input.json"}); err != nil || got != "input.json" {
		t.Fatalf("valid completion arguments rejected: value=%q err=%v", got, err)
	}
	for _, args := range [][]string{
		{},
		{"--completion-file"},
		{"--completion-file", ""},
		{"--completion-file", "one", "--completion-file", "two"},
		{"--completion-file", "input.json", "extra"},
		{"--output", "manual.json"},
	} {
		if _, err := completionFileStrict(args); err == nil {
			t.Fatalf("invalid completion arguments accepted: %#v", args)
		}
	}
}

func TestProjectIdentifiersCLIArgumentsAreStrict(t *testing.T) {
	if got, err := expectedStrict([]string{"--expected-hub-revision", strings.Repeat("a", 40)}); err != nil || got != strings.Repeat("a", 40) {
		t.Fatalf("valid identifier arguments rejected: value=%q err=%v", got, err)
	}
	for _, args := range [][]string{
		{"--unknown", "value"},
		{"--expected-hub-revision"},
		{"--expected-hub-revision", "one", "--expected-hub-revision", "two"},
	} {
		if _, err := expectedStrict(args); err == nil {
			t.Fatalf("invalid identifier arguments accepted: %#v", args)
		}
	}
}

func TestProjectIdentifiersCLIRoutesAdoptAndRead(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	projectRecord := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	if _, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: projectRecord, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}}); err != nil {
		t.Fatal(err)
	}
	capture := func(fn func()) string {
		t.Helper()
		old := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		fn()
		_ = w.Close()
		os.Stdout = old
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	adopt := capture(func() { project(context.Background(), s, []string{"identifiers-adopt", "example", "GTW"}) })
	if !strings.Contains(adopt, `"project_code": "GTW"`) || !strings.Contains(adopt, `"status": "adopted"`) {
		t.Fatalf("unexpected identifier adoption output: %s", adopt)
	}
	read := capture(func() { project(context.Background(), s, []string{"identifiers-read", "example"}) })
	if !strings.Contains(read, `"project_code": "GTW"`) || !strings.Contains(read, `"next_task_number": 1`) || !strings.Contains(read, `"next_adr_number": 1`) {
		t.Fatalf("unexpected identifier read output: %s", read)
	}
}
