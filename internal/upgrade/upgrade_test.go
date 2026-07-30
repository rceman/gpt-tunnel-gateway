package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

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
