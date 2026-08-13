package releaseartifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const testBinary = "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo %s; fi\n"

func TestReplaceAllReplacesEveryControlBinary(t *testing.T) {
	release, install := t.TempDir(), t.TempDir()
	paths := Paths(filepath.Join(install, "gpt-tunnel-gatewayd"))
	old := make(map[string][]byte, len(BinaryNames))
	for _, name := range BinaryNames {
		old[name] = []byte(fmt.Sprintf(testBinary, "0.6.10"))
		writeExecutable(t, paths[name], old[name])
		writeExecutable(t, filepath.Join(release, name), []byte(fmt.Sprintf(testBinary, "0.6.11")))
	}
	writeChecksums(t, release)
	if err := ValidateRelease(release, "0.6.11"); err != nil {
		t.Fatalf("release validation failed: %v", err)
	}
	if err := ReplaceAll(release, paths, old); err != nil {
		t.Fatalf("replace failed: %v", err)
	}
	if err := VerifyInstalled(release, paths); err != nil {
		t.Fatalf("installed set mismatch: %v", err)
	}
	for _, name := range BinaryNames {
		if got, err := BinaryVersion(paths[name]); err != nil || got != "0.6.11" {
			t.Fatalf("%s version = %q, err=%v", name, got, err)
		}
	}
}

func TestReplaceAllRestoresEarlierBinariesAfterCommitFailure(t *testing.T) {
	release, install := t.TempDir(), t.TempDir()
	paths := Paths(filepath.Join(install, "gpt-tunnel-gatewayd"))
	old := make(map[string][]byte, len(BinaryNames))
	for _, name := range BinaryNames {
		old[name] = []byte(fmt.Sprintf(testBinary, "0.6.10"))
		if name != "gpt-tunnelctl" {
			writeExecutable(t, paths[name], old[name])
		}
		writeExecutable(t, filepath.Join(release, name), []byte(fmt.Sprintf(testBinary, "0.6.11")))
	}
	if err := os.Mkdir(paths["gpt-tunnelctl"], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceAll(release, paths, old); err == nil {
		t.Fatal("replace unexpectedly succeeded")
	}
	for _, name := range []string{"gpt-tunnel-gatewayd", "gpt-tunnel"} {
		got, err := os.ReadFile(paths[name])
		if err != nil || string(got) != string(old[name]) {
			t.Fatalf("%s was not restored: err=%v", name, err)
		}
	}
}

func TestReplaceAllSupportsSameVersionDifferentBytes(t *testing.T) {
	release, install := t.TempDir(), t.TempDir()
	paths := Paths(filepath.Join(install, "gpt-tunnel-gatewayd"))
	old := make(map[string][]byte, len(BinaryNames))
	for _, name := range BinaryNames {
		old[name] = append([]byte("# previously installed\n"), []byte(fmt.Sprintf(testBinary, "0.6.11"))...)
		writeExecutable(t, paths[name], old[name])
		writeExecutable(t, filepath.Join(release, name), []byte(fmt.Sprintf(testBinary, "0.6.11")))
	}
	writeChecksums(t, release)
	if err := ValidateRelease(release, "0.6.11"); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceAll(release, paths, old); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalled(release, paths); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeChecksums(t *testing.T, dir string) {
	t.Helper()
	data := make([]byte, 0, 256)
	for _, name := range BinaryNames {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		data = append(data, []byte(hex.EncodeToString(sum[:])+"  "+name+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
