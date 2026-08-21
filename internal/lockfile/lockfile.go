package lockfile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Lock struct{ file *os.File }

type ProcessEvidence struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable,omitempty"`
	Argv       string `json:"argv,omitempty"`
	UID        string `json:"uid,omitempty"`
	StartTicks string `json:"start_time_ticks,omitempty"`
}

type ContentionEvidence struct {
	Path       string            `json:"path"`
	CurrentPID int               `json:"current_pid"`
	Owners     []ProcessEvidence `json:"owners,omitempty"`
}

func (e ContentionEvidence) BoundedJSON() string {
	const maxBytes = 4096
	payload, err := json.Marshal(e)
	if err != nil {
		return `{"path":"","current_pid":0,"error":"marshal"}`
	}
	if len(payload) > maxBytes {
		payload = append(payload[:maxBytes-1], '}')
	}
	return string(payload)
}

// ReadContentionEvidence reads kernel flock ownership from /proc/locks. The
// lock file's text is deliberately not consulted because it is only metadata.
func ReadContentionEvidence(path string) ContentionEvidence {
	evidence := ContentionEvidence{Path: path, CurrentPID: os.Getpid()}
	device, inode, ok := deviceInode(path)
	if !ok {
		return evidence
	}
	file, err := os.Open("/proc/locks")
	if err != nil {
		return evidence
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 4096)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || fields[1] != "FLOCK" || fields[5] != device+":"+strconv.FormatUint(inode, 10) {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || len(evidence.Owners) >= 8 {
			continue
		}
		evidence.Owners = append(evidence.Owners, processEvidence(pid))
	}
	return evidence
}

func deviceInode(path string) (string, uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, false
	}
	dev := uint64(stat.Dev)
	major := (dev >> 8) & 0xfff
	major |= (dev >> 32) & 0xfffff000
	minor := dev & 0xff
	minor |= (dev >> 12) & 0xffffff00
	return fmt.Sprintf("%02x:%02x", major, minor), stat.Ino, true
}

func processEvidence(pid int) ProcessEvidence {
	evidence := ProcessEvidence{PID: pid}
	if executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		evidence.Executable = executable
	}
	if argv, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		evidence.Argv = strings.TrimSpace(string(bytes.ReplaceAll(argv, []byte{0}, []byte{' '})))
	}
	if status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					evidence.UID = fields[1]
				}
				break
			}
		}
	}
	if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if closeParen := bytes.LastIndexByte(stat, ')'); closeParen >= 0 {
			fields := strings.Fields(string(stat[closeParen+1:]))
			if len(fields) > 19 {
				evidence.StartTicks = fields[19]
			}
		}
	}
	return evidence
}

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
