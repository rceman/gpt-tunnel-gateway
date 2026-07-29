package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Lock struct{ path string }

func Acquire(dir, name string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acquire lock %s: %w", name, err)
	}
	_, _ = fmt.Fprintf(f, "pid=%d\ncreated=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	_ = f.Close()
	return &Lock{path: path}, nil
}
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	return os.Remove(l.path)
}
