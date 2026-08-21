package lockfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockRejectsConcurrentOwnerAndReacquiresAfterRelease(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir, "hub")
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Acquire(dir, "hub"); err == nil {
		second.Release()
		t.Fatal("concurrent lock owner accepted")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(dir, "hub")
	if err != nil {
		t.Fatalf("persistent lock file blocked reacquisition: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReadContentionEvidenceUsesKernelOwner(t *testing.T) {
	dir := t.TempDir()
	owned, err := Acquire(dir, "hub")
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Release()

	path := filepath.Join(dir, "hub.lock")
	evidence := ReadContentionEvidence(path)
	if evidence.Path != path || evidence.CurrentPID != os.Getpid() {
		t.Fatalf("unexpected evidence identity: %+v", evidence)
	}
	if len(evidence.Owners) == 0 {
		t.Skip("kernel flock table unavailable")
	}
	for _, owner := range evidence.Owners {
		if owner.PID != os.Getpid() {
			continue
		}
		if owner.Executable == "" || owner.Argv == "" || owner.UID == "" || owner.StartTicks == "" {
			t.Fatalf("incomplete live owner evidence: %+v", owner)
		}
		if len(evidence.BoundedJSON()) > 4096 {
			t.Fatal("contention evidence exceeded bound")
		}
		return
	}
	t.Fatalf("kernel evidence did not identify current owner: %+v", evidence.Owners)
}
