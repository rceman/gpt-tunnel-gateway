package lockfile

import "testing"

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
