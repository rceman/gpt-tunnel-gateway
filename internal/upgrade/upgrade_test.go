package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
