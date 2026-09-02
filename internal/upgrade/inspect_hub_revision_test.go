package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

const inspectHubRevisionTestSHA = "0123456789abcdef0123456789abcdef01234567"

func TestInspectUsesExplicitHubRevisionWhenLocalStateCheckHasNone(t *testing.T) {
	c := inspectHubRevisionFixture(t)

	var calls int
	serviceHubRevisionFn = func(context.Context, config.Config) (string, error) {
		calls++
		return "  " + inspectHubRevisionTestSHA + "  ", nil
	}

	result, err := Inspect(context.Background(), c, "test-config.json")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" {
		t.Fatalf("Inspect status = %q, blockers=%#v", result.Status, result.Blockers)
	}
	if result.HubRevision != inspectHubRevisionTestSHA {
		t.Fatalf("Inspect hub revision = %q, want %q", result.HubRevision, inspectHubRevisionTestSHA)
	}
	if !result.RollbackReady {
		t.Fatalf("Inspect rollback_ready = false, result=%#v", result)
	}
	if calls != 1 {
		t.Fatalf("explicit Hub revision reads = %d, want 1", calls)
	}
	for _, blocker := range result.Blockers {
		if blocker.Code == "HUB_REVISION_UNAVAILABLE" {
			t.Fatalf("healthy explicit Hub revision produced false blocker: %#v", blocker)
		}
	}
}

func TestInspectFailsClosedWhenExplicitHubRevisionReadFails(t *testing.T) {
	for name, read := range map[string]func() (string, error){
		"error": func() (string, error) {
			return "", errors.New("test Hub unavailable")
		},
		"empty": func() (string, error) {
			return "  \t", nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := inspectHubRevisionFixture(t)
			serviceHubRevisionFn = func(context.Context, config.Config) (string, error) {
				return read()
			}

			result, err := Inspect(context.Background(), c, "test-config.json")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status == "ready" {
				t.Fatalf("Inspect became ready after Hub revision failure: %#v", result)
			}
			if result.HubRevision != "" {
				t.Fatalf("failed explicit Hub revision populated %q", result.HubRevision)
			}
			found := false
			for _, blocker := range result.Blockers {
				if blocker.Code == "HUB_REVISION_UNAVAILABLE" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing HUB_REVISION_UNAVAILABLE blocker: %#v", result.Blockers)
			}
			if result.RollbackReady {
				t.Fatal("rollback_ready remained true without authoritative Hub revision")
			}
		})
	}
}

func inspectHubRevisionFixture(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "gpt-tunnel-gateway")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"scripts/build-release.sh", "scripts/static-check.py"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("0.6.12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		GatewayID: "upgrade-test",
		StateDir:  filepath.Join(dir, "state"),
		Hub:       config.HubConfig{Branch: "main"},
		Controller: config.ControllerConfig{
			GatewayBinary:          filepath.Join(dir, "gateway"),
			TunnelClientBinary:     filepath.Join(dir, "tunnel-client"),
			TunnelEnvFile:          filepath.Join(dir, "tunnel.env"),
			TunnelHealthListenAddr: "127.0.0.1:8766",
		},
	}
	originals := struct {
		sourceRoot       func() (string, string, error)
		validateSource   func(string, string) error
		status           func(context.Context, config.Config, string) (controller.Status, error)
		installedVersion func(string) (string, error)
		env              func(string) error
		hubRevision      func(context.Context, config.Config) (string, error)
	}{sourceRootFn, validateSourceFn, inspectStatusFn, installedVersionFn, validateTunnelEnvFn, serviceHubRevisionFn}
	cleanup := func() {
		sourceRootFn = originals.sourceRoot
		validateSourceFn = originals.validateSource
		inspectStatusFn = originals.status
		installedVersionFn = originals.installedVersion
		validateTunnelEnvFn = originals.env
		serviceHubRevisionFn = originals.hubRevision
	}
	t.Cleanup(cleanup)
	sourceRootFn = func() (string, string, error) { return root, strings.Repeat("a", 40), nil }
	validateSourceFn = func(string, string) error { return nil }
	inspectStatusFn = func(context.Context, config.Config, string) (controller.Status, error) {
		return controller.Status{
			Gateway:          controller.ProcessStatus{Running: true, PID: 10, IdentityValid: true},
			Tunnel:           controller.ProcessStatus{Running: true, PID: 20, IdentityValid: true},
			GatewayReady:     true,
			TunnelReady:      true,
			InstalledVersion: "0.6.11",
			RunningVersion:   "0.6.11",
			VersionMatch:     true,
		}, nil
	}
	installedVersionFn = func(string) (string, error) { return "0.6.11", nil }
	validateTunnelEnvFn = func(string) error { return nil }
	serviceHubRevisionFn = serviceHubRevision
	return c
}
