package runtime_log

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
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
	entries := make([]logEntry, 0)
	paths := make([]string, 0, s.retention()+1)
	paths = append(paths, s.path())
	for index := 1; index <= s.retention(); index++ {
		paths = append(paths, fmt.Sprintf("%s.%d", s.path(), index))
	}
	for fileRank, path := range paths {
		file, openErr := os.Open(path)
		if os.IsNotExist(openErr) {
			continue
		}
		if openErr != nil {
			return ReadResult{}, openErr
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024), 64<<10)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			var event Event
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Validate() != nil {
				result.MalformedLines++
				continue
			}
			if !matches(event, filter) {
				continue
			}
			entries = append(entries, logEntry{
				Event:      event,
				FileRank:   fileRank,
				LineNumber: lineNumber,
				Key:        logEntryKey(event, fileRank, lineNumber),
			})
		}
		scanErr := scanner.Err()
		file.Close()
		if scanErr != nil {
			return ReadResult{}, scanErr
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Event.Timestamp.Equal(entries[j].Event.Timestamp) {
			if entries[i].FileRank != entries[j].FileRank {
				return entries[i].FileRank < entries[j].FileRank
			}
			return entries[i].LineNumber > entries[j].LineNumber
		}
		return entries[i].Event.Timestamp.After(entries[j].Event.Timestamp)
	})
	page, nextCursor, hasMore, err := pageLogEntries(entries, filter.Limit, filter.Cursor)
	if err != nil {
		return ReadResult{}, err
	}
	result.Events = make([]Event, 0, len(page))
	for _, entry := range page {
		result.Events = append(result.Events, entry.Event)
	}
	result.NextCursor = nextCursor
	result.HasMore = hasMore
	return result, nil
}

type logEntry struct {
	Event      Event
	FileRank   int
	LineNumber int
	Key        string
}

func logEntryKey(event Event, fileRank, lineNumber int) string {
	return event.Timestamp.UTC().Format(time.RFC3339Nano) + "\x00" + strconv.Itoa(fileRank) + "\x00" + strconv.Itoa(lineNumber)
}

func pageLogEntries(entries []logEntry, limit int, rawCursor string) ([]logEntry, string, bool, error) {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, entry.Key)
	}
	after, err := pagination.Resolve(rawCursor, "runtime-log", keys)
	if err != nil {
		return nil, "", false, err
	}
	start := 0
	if after != "" {
		start = -1
		for index, entry := range entries {
			if entry.Key == after {
				start = index + 1
				break
			}
		}
		if start < 0 {
			return nil, "", false, fmt.Errorf("continuation cursor is no longer valid")
		}
	}
	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}
	page := entries[start:end]
	if end == len(entries) || len(page) == 0 {
		return page, "", false, nil
	}
	return page, pagination.Encode("runtime-log", page[len(page)-1].Key), true, nil
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
		(filter.Action == "" || event.Action == filter.Action) &&
		(filter.RequestID == "" || event.RequestID == filter.RequestID) &&
		(filter.SessionID == "" || event.SessionID == filter.SessionID) &&
		(filter.ProjectID == "" || event.ProjectID == filter.ProjectID) &&
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
