package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func makeUpgradeFixtures(t *testing.T) (string, map[string]string, map[string][]byte) {
	t.Helper()
	release, paths, old := t.TempDir(), map[string]string{}, map[string][]byte{}
	dest := t.TempDir()
	for _, name := range binaryOrder {
		src := filepath.Join(release, name)
		if err := os.WriteFile(src, []byte("#!/bin/sh\nprintf '0.2.3\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dest, name)
		data := []byte("old-" + name)
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
		paths[name], old[name] = path, data
	}
	return release, paths, old
}

func TestReplaceAllStagesBeforeCommitAndRestoresCommitFailure(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalRename, originalCopy := stageRename, stageCopy
	t.Cleanup(func() { stageRename, stageCopy = originalRename, originalCopy })
	commits := 0
	stageRename = func(src, dst string) error {
		commits++
		if commits == 2 {
			return os.ErrPermission
		}
		return os.Rename(src, dst)
	}
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("commit failure accepted")
	}
	for _, name := range binaryOrder {
		got, err := os.ReadFile(paths[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(old[name]) {
			t.Fatalf("%s was not restored", name)
		}
	}
}

func TestReplaceAllSucceedsAndCoversEveryStagePosition(t *testing.T) {
	for _, position := range binaryOrder {
		t.Run(position, func(t *testing.T) {
			release, paths, old := makeUpgradeFixtures(t)
			originalCopy := stageCopy
			t.Cleanup(func() { stageCopy = originalCopy })
			calls := 0
			stageCopy = func(src, dst string) (string, error) {
				calls++
				if binaryOrder[calls-1] == position {
					return "", os.ErrPermission
				}
				return stageOne(src, dst)
			}
			if err := replaceAll(release, paths, old); err == nil {
				t.Fatal("stage failure accepted")
			}
			for _, name := range binaryOrder {
				got, _ := os.ReadFile(paths[name])
				if string(got) != string(old[name]) {
					t.Fatalf("%s changed", name)
				}
			}
		})
	}
	// A normal transaction commits all three staged files in deterministic order.
	release, paths, old := makeUpgradeFixtures(t)
	if err := replaceAll(release, paths, old); err != nil {
		t.Fatal(err)
	}
	for _, name := range binaryOrder {
		got, _ := os.ReadFile(paths[name])
		if string(got) != "#!/bin/sh\nprintf '0.2.3\\n'\n" {
			t.Fatalf("%s was not committed", name)
		}
	}
}

func TestReplaceAllRenameFailureAfterEachCommitRestoresAll(t *testing.T) {
	for failure := 1; failure <= len(binaryOrder); failure++ {
		t.Run(fmt.Sprintf("rename-%d", failure), func(t *testing.T) {
			release, paths, old := makeUpgradeFixtures(t)
			originalRename := stageRename
			t.Cleanup(func() { stageRename = originalRename })
			calls := 0
			stageRename = func(src, dst string) error {
				calls++
				if calls == failure {
					return os.ErrPermission
				}
				return os.Rename(src, dst)
			}
			if err := replaceAll(release, paths, old); err == nil {
				t.Fatal("rename failure accepted")
			}
			for _, name := range binaryOrder {
				got, _ := os.ReadFile(paths[name])
				if string(got) != string(old[name]) {
					t.Fatalf("%s was not restored", name)
				}
			}
		})
	}
}

func TestReplaceAllDirectorySyncFailureCleansStaging(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalSync := stageSyncDir
	t.Cleanup(func() { stageSyncDir = originalSync })
	stageSyncDir = func(string) error { return os.ErrPermission }
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("sync failure accepted")
	}
	for _, name := range binaryOrder {
		got, _ := os.ReadFile(paths[name])
		if string(got) != string(old[name]) {
			t.Fatalf("%s was not restored", name)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(paths["gpt-tunnel"]))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gpt-tunnel-upgrade-stage-") {
			t.Fatalf("staging file remains: %s", entry.Name())
		}
	}
}

func TestReplaceAllPropagatesStagingCleanupFailure(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalCopy, originalRemove := stageCopy, stageRemove
	t.Cleanup(func() { stageCopy, stageRemove = originalCopy, originalRemove })
	calls := 0
	stageCopy = func(src, dst string) (string, error) {
		calls++
		if calls == 1 {
			return stageOne(src, dst)
		}
		return "", os.ErrPermission
	}
	stageRemove = func(string) error { return os.ErrPermission }
	if err := replaceAll(release, paths, old); err == nil || !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("cleanup failure not propagated: %v", err)
	}
}

func TestRollbackBackupCleanupPolicy(t *testing.T) {
	dir := t.TempDir()
	original := removeUpgradeBackup
	t.Cleanup(func() { removeUpgradeBackup = original })
	if err := os.WriteFile(filepath.Join(dir, "binary"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupRollbackBackup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("successful rollback retained backup: %v", err)
	}

	dir = t.TempDir()
	removeUpgradeBackup = func(string) error { return os.ErrPermission }
	if err := cleanupRollbackBackup(dir); err == nil {
		t.Fatal("cleanup failure not reported")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("failed cleanup did not retain backup: %v", err)
	}
}

func TestTunnelClientOwnershipRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel-client")
	if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, os.Getuid()+1, -1); err != nil {
		t.Skipf("cannot create foreign-owned fixture: %v", err)
	}
	if err := validateOwnedExecutable(path, "tunnel-client"); err == nil {
		t.Fatal("foreign-owned tunnel-client accepted")
	}
}

func TestReplaceAllStageFailureCleansStaging(t *testing.T) {
	release, paths, old := makeUpgradeFixtures(t)
	originalCopy := stageCopy
	t.Cleanup(func() { stageCopy = originalCopy })
	calls := 0
	stageCopy = func(src, dst string) (string, error) {
		calls++
		if calls == 2 {
			return "", os.ErrPermission
		}
		return stageOne(src, dst)
	}
	if err := replaceAll(release, paths, old); err == nil {
		t.Fatal("stage failure accepted")
	}
	entries, err := os.ReadDir(filepath.Dir(paths["gpt-tunnel"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnel-gatewayd"]) && filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnel"]) && filepath.Base(entry.Name()) != filepath.Base(paths["gpt-tunnelctl"]) {
			if len(entry.Name()) > 0 && entry.Name()[0] == '.' {
				t.Fatalf("staging file remains: %s", entry.Name())
			}
		}
	}
}

func TestVersionOrderingRejectsSameAndDowngrade(t *testing.T) {
	if compareVersion("0.2.3", "0.2.3") != 0 {
		t.Fatal("same version must compare equal")
	}
	if compareVersion("0.2.2", "0.2.3") >= 0 {
		t.Fatal("downgrade must compare lower")
	}
	if compareVersion("0.2.4", "0.2.3") <= 0 {
		t.Fatal("upgrade must compare higher")
	}
}

func TestValidateReleaseRequiresExactArtifactsAndChecksums(t *testing.T) {
	dir := t.TempDir()
	names := []string{"gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl"}
	lines := ""
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '0.2.3\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		sum := sha256.Sum256(data)
		lines += hex.EncodeToString(sum[:]) + "  " + name + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unexpected"), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err == nil {
		t.Fatal("unexpected release artifact accepted")
	}
}

func TestParseVersionRejectsNonCanonical(t *testing.T) {
	for _, value := range []string{"v0.2.3", "0.2", "0.2.3-beta", "01.2.3"} {
		if _, err := parseVersion(value); err == nil {
			t.Fatalf("accepted invalid version %q", value)
		}
	}
	if got, err := parseVersion("0.2.3"); err != nil || got != "0.2.3" {
		t.Fatalf("canonical version parse failed: %q %v", got, err)
	}
}

func TestValidateReleaseRejectsDuplicateAndTraversalManifest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf '0.2.3\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "gpt-tunnel"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:]) + "  gpt-tunnel\n"
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(line+line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err == nil {
		t.Fatal("duplicate manifest accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(line+strings.Repeat("0", 64)+"  ../gpt-tunnel-gatewayd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(dir, "0.2.3"); err == nil {
		t.Fatal("traversal manifest accepted")
	}
}

func TestSmokeTimeoutIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	addr := strings.TrimPrefix(server.URL, "http://")
	c := config.Config{ListenAddr: addr, GatewayID: "home", StateDir: t.TempDir(), Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
	start := time.Now()
	if err := smoke(ctx, c, "0.2.3", "0.2.2"); err == nil {
		t.Fatal("timeout server accepted")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("smoke exceeded timeout bound")
	}
}

func TestSmokeRejectsMalformedJSONRPCAndToolContracts(t *testing.T) {
	validInit := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","serverInfo":{"version":"0.2.3"}}}`
	cases := []struct{ name, body string }{
		{"jsonrpc-version", `{"jsonrpc":"1.0","id":1,"result":{}}`},
		{"mismatched-id", `{"jsonrpc":"2.0","id":9,"result":{}}`},
		{"top-level-error", `{"jsonrpc":"2.0","id":1,"error":{"code":-1}}`},
		{"missing-result", `{"jsonrpc":"2.0","id":1}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://"), GatewayID: "home", StateDir: t.TempDir(), Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
			if err := smoke(context.Background(), c, "0.2.3", "0.2.2"); err == nil {
				t.Fatal("malformed response accepted")
			}
		})
	}
	contractCases := []struct {
		name       string
		list, ping map[string]any
	}{
		{"malformed-tool-descriptor", map[string]any{"tools": []any{"bad"}}, nil},
		{"invalid-input-schema", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "array"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}}}}, nil},
		{"invalid-annotations", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": "yes"}}}}, nil},
		{"ping-error-result", nil, map[string]any{"isError": true, "structuredContent": map[string]any{}}},
		{"capability-error-result", nil, map[string]any{"isError": true, "structuredContent": map[string]any{}}},
	}
	for _, test := range contractCases {
		t.Run(test.name, func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				body := validInit
				if count == 2 {
					payload := test.list
					if payload == nil {
						payload = map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}}}}
					}
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "result": payload})
					body = string(bodyBytes)
				} else if count == 3 && test.ping != nil {
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 3, "result": test.ping})
					body = string(bodyBytes)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://"), GatewayID: "home", StateDir: t.TempDir(), Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
			if err := smoke(context.Background(), c, "0.2.3", "0.2.2"); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
}
