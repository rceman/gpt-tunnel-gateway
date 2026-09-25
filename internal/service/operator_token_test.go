package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestOperatorTokenIsStableOwnerPrivateAndValidated(t *testing.T) {
	stateDir := t.TempDir()
	token, err := EnsureOperatorToken(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != operatorTokenBytes*2 {
		t.Fatalf("operator token length=%d", len(token))
	}
	path := filepath.Join(stateDir, OperatorTokenFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("operator token permissions=%#o", info.Mode().Perm())
	}
	repeated, err := EnsureOperatorToken(stateDir)
	if err != nil || repeated != token {
		t.Fatalf("operator token changed: same=%v err=%v", repeated == token, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOperatorToken(stateDir); err == nil {
		t.Fatal("permissive operator token file was accepted")
	}
	repaired, err := EnsureOperatorToken(stateDir)
	if err != nil || repaired != token {
		t.Fatalf("owner-private token repair changed token: same=%v err=%v", repaired == token, err)
	}
	info, err = os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("repaired operator token permissions=%v err=%v", info, err)
	}
	svc := New(config.Config{StateDir: stateDir})
	if !svc.ValidOperatorToken(token) || svc.ValidOperatorToken("invalid") {
		t.Fatal("operator token validation did not distinguish the owner token")
	}
}
