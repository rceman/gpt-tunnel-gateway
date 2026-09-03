package releaseartifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testBinary = "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo %s; fi\n"

func TestBinarySourceRevisionRequiresExactEmbeddedSHA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	writeExecutable(t, path, []byte("#!/bin/sh\nif [ \"$1\" = \"--source-sha\" ]; then printf '27ce3ba4d9a27bc469501337618db9d7b351da6f\\n'; fi\n"))

	got, modified, err := BinarySourceRevision(path)
	if err != nil || modified || got != "27ce3ba4d9a27bc469501337618db9d7b351da6f" {
		t.Fatalf("source revision = %q, modified=%v, err=%v", got, modified, err)
	}
}

func TestBinarySourceRevisionRejectsMissingMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	writeExecutable(t, path, []byte("#!/bin/sh\nexit 0\n"))
	if _, _, err := BinarySourceRevision(path); err == nil {
		t.Fatal("missing source marker was accepted")
	}
}

func TestBinaryProbeBoundsChildOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	writeExecutable(t, path, []byte("#!/bin/sh\nhead -c 20000 /dev/zero\n"))
	if _, err := BinaryVersion(path); err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("unbounded probe output was accepted: %v", err)
	}
}

func TestBinaryProbeHonorsCallerDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	writeExecutable(t, path, []byte("#!/bin/sh\nexec sleep 2\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := BinaryVersionContext(ctx, path); err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("caller deadline was not propagated: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("probe exceeded caller deadline by too much: %s", elapsed)
	}
}

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

func TestSnapshotAndRestoreAllPreservesTheCompleteArtifactSet(t *testing.T) {
	install := t.TempDir()
	paths := Paths(filepath.Join(install, "gpt-tunnel-gatewayd"))
	old := make(map[string][]byte, len(BinaryNames))
	for _, name := range BinaryNames {
		old[name] = []byte("old-" + name)
		writeExecutable(t, paths[name], old[name])
	}
	snapshot, err := SnapshotAll(paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range BinaryNames {
		writeExecutable(t, paths[name], []byte("candidate-"+name))
	}
	if err := RestoreAll(paths, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, name := range BinaryNames {
		got, err := os.ReadFile(paths[name])
		if err != nil || string(got) != string(old[name]) {
			t.Fatalf("%s restore = %q, err=%v", name, got, err)
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
