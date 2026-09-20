package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestAdminRepositoryValidationRequiresCanonicalAllowlistedGitHubIdentity(t *testing.T) {
	s := &Service{Config: config.Config{Admin: config.AdminConfig{GitHubAllowedOwners: []string{"Acme"}}}}
	got, err := s.validateAdminRepository("acme/widget")
	if err != nil || got.Owner != "acme" || got.ProjectID != "widget" || got.URL != "git@github.com:acme/widget.git" {
		t.Fatalf("repository=%#v err=%v", got, err)
	}
	for _, value := range []string{"https://github.com/acme/widget", "acme/widget.git", "../widget", "other/widget", "acme/widget.name"} {
		if _, err := s.validateAdminRepository(value); err == nil {
			t.Fatalf("repository %q was accepted", value)
		}
	}
}

func TestAdminHarnessUsesDefaultAndConfiguredProfiles(t *testing.T) {
	defaultService := &Service{}
	if got, err := defaultService.adminHarness(""); err != nil || got != "codex" {
		t.Fatalf("default harness=%q err=%v", got, err)
	}
	if _, err := defaultService.adminHarness("devin"); err == nil {
		t.Fatal("unconfigured harness was accepted")
	}
	configured := &Service{Config: config.Config{Admin: config.AdminConfig{WorkerHarnesses: []string{"codex", "devin"}}}}
	if got, err := configured.adminHarness("devin"); err != nil || got != "devin" {
		t.Fatalf("configured harness=%q err=%v", got, err)
	}
}

func TestAdminRemoteIdentityNormalizesGitHubTransportForms(t *testing.T) {
	expected := adminRepository{
		Owner: "acme",
		Name:  "widget",
	}
	for _, remote := range []string{
		"git@github.com:acme/widget.git",
		"https://github.com/acme/widget.git",
		"https://github.com/Acme/Widget",
	} {
		if !sameGitHubRepository(remote, expected) {
			t.Fatalf("remote %q did not match expected identity", remote)
		}
	}
	for _, remote := range []string{"https://evil.example/acme/widget", "https://user:secret@github.com/acme/widget.git", "git@github.com:other/widget.git"} {
		if sameGitHubRepository(remote, expected) {
			t.Fatalf("remote %q matched unexpectedly", remote)
		}
	}
}

func TestAdminWorkerRollbackStopsOnlyAWorkerStartedByThisCall(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	stop := filepath.Join(dir, "stop")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n  detached) if [ -f \"" + state + "\" ]; then printf '%s\\n' '[{\"sessionKey\":\"widget_worker\",\"runtimeId\":\"runtime-1\",\"profile\":\"codex\",\"controllerReachable\":true}]'; else printf '%s\\n' '[]'; fi ;;\n  session-status) if [ -f \"" + state + "\" ]; then printf '%s\\n' '{\"sessionKey\":\"widget_worker\",\"profile\":\"codex\",\"controllerReachable\":true,\"state\":\"idle\"}'; else exit 1; fi ;;\n  history) printf '%s\\n' '[]' ;;\n  start) touch \"" + state + "\" ;;\n  stop) touch \"" + stop + "\" ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	service := &Service{
		Config:  config.Config{StateDir: dir},
		Airelay: airelay.Client{Command: script, Timeout: time.Second},
	}
	if err := service.ensureAdminWorker(context.Background(), "invalid!", "widget_worker", "codex"); err == nil {
		t.Fatal("invalid project unexpectedly bound a newly started Worker")
	}
	if _, err := os.Stat(stop); err != nil {
		t.Fatalf("newly started unbound Worker was not rolled back: %v", err)
	}
}
