package controller

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

func (c Controller) runtimeEvent(event runtime_log.Event) {
	if c.Config.StateDir == "" {
		return
	}
	_ = runtime_log.New(c.Config.StateDir).Append(event)
}

func (c Controller) processEvent(name, binary, level, event string, pid int, message string, cause error) {
	record := runtime_log.Event{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Component: name,
		Event:     event,
		PID:       pid,
		Source:    filepath.Base(binary),
		Message:   message,
	}
	if cause != nil {
		// Keep filesystem paths, URLs, and command output out of the durable log.
		record.Error = fmt.Sprintf("%T", cause)
	}
	c.runtimeEvent(record)
}
