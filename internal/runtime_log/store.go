package runtime_log

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

type Store struct {
	StateDir  string
	MaxBytes  int64
	Retention int
}

func New(stateDir string) Store {
	return Store{
		StateDir:  stateDir,
		MaxBytes:  DefaultMaxBytes,
		Retention: DefaultRetention,
	}
}

func (s Store) directory() string { return filepath.Join(s.StateDir, "runtime") }
func (s Store) path() string      { return filepath.Join(s.directory(), "events.jsonl") }
func (s Store) lockDir() string   { return filepath.Join(s.StateDir, "locks") }

func (s Store) Append(event Event) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	event.Message = SanitizeText(event.Message)
	event.Error = SanitizeText(event.Error)
	if err := event.Validate(); err != nil {
		return err
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if int64(len(line)) > s.maxBytes() {
		return fmt.Errorf("runtime event exceeds log capacity")
	}
	if err := os.MkdirAll(s.directory(), 0o700); err != nil {
		return err
	}
	lease, err := lockfile.Acquire(s.lockDir(), "runtime-log")
	if err != nil {
		return err
	}
	defer lease.Release()
	if err := s.rotateIfNeeded(int64(len(line))); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(line)
	return err
}

func (s Store) Read(filter Filter) (ReadResult, error) {
	filter, err := normalizeFilter(filter)
	if err != nil {
		return ReadResult{}, err
	}
	result := ReadResult{Events: []Event{}}
	paths := make([]string, 0, s.retention()+1)
	paths = append(paths, s.path())
	for index := 1; index <= s.retention(); index++ {
		paths = append(paths, fmt.Sprintf("%s.%d", s.path(), index))
	}
	for _, path := range paths {
		file, openErr := os.Open(path)
		if os.IsNotExist(openErr) {
			continue
		}
		if openErr != nil {
			return ReadResult{}, openErr
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024), 64<<10)
		for scanner.Scan() {
			var event Event
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Validate() != nil {
				result.MalformedLines++
				continue
			}
			if !matches(event, filter) {
				continue
			}
			result.Events = append(result.Events, event)
			if len(result.Events) >= filter.Limit {
				break
			}
		}
		scanErr := scanner.Err()
		file.Close()
		if scanErr != nil {
			return ReadResult{}, scanErr
		}
		if len(result.Events) >= filter.Limit {
			break
		}
	}
	return result, nil
}

func (s Store) rotateIfNeeded(incoming int64) error {
	info, err := os.Stat(s.path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size()+incoming <= s.maxBytes() {
		return nil
	}
	for index := s.retention(); index >= 1; index-- {
		source := fmt.Sprintf("%s.%d", s.path(), index)
		destination := fmt.Sprintf("%s.%d", s.path(), index+1)
		if index == s.retention() {
			_ = os.Remove(destination)
		}
		if err := os.Rename(source, destination); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(s.path(), s.path()+".1"); err != nil {
		return err
	}
	return nil
}

func (s Store) maxBytes() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

func (s Store) retention() int {
	if s.Retention > 0 && s.Retention <= 16 {
		return s.Retention
	}
	return DefaultRetention
}

func matches(event Event, filter Filter) bool {
	return (filter.Level == "" || event.Level == filter.Level) &&
		(filter.Component == "" || event.Component == filter.Component) &&
		(filter.Event == "" || event.Event == filter.Event) &&
		(filter.OperationID == "" || event.OperationID == filter.OperationID)
}

func EventFor(ctxValues map[string]string, level, component, name string) Event {
	event := Event{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Component: component,
		Event:     name,
	}
	event.RequestID = ctxValues["request_id"]
	event.OperationID = ctxValues["operation_id"]
	event.SessionID = ctxValues["session_id"]
	event.ProjectID = ctxValues["project_id"]
	return event
}
