package lockfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Lock struct{ file *os.File }

var ErrReadOnlyUnavailable = errors.New("read-only lock unavailable")

// IsBusy reports only an owned-lock collision. Callers that retry acquisition
// must propagate every filesystem, permission, and lock-file corruption error.
func IsBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// AcquireReadOnly coordinates with writers without creating or modifying the
// lock file. Read-only callers must run after the owning controller has
// created the lock files during startup.
func AcquireReadOnly(dir, name string) (*Lock, error) {
	path := filepath.Join(dir, name+".lock")
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrReadOnlyUnavailable
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		f.Close()
		return nil, ErrReadOnlyUnavailable
	}
	return &Lock{file: f}, nil
}

func Acquire(dir, name string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", name, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire lock %s: %w", name, err)
	}
	if err := f.Truncate(0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "pid=%d\ncreated=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	return &Lock{file: f}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
