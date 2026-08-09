package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
