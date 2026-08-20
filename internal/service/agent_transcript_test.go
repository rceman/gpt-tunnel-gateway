package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestAgentTailUsesLocalSessionWithoutDurableAgentLookup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = tail ] && printf 'one\\ntwo\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Service{
		Config: config.Config{
			StateDir:     dir,
			MaxReadBytes: 1 << 20,
			Projects: map[string]config.ProjectConfig{
				"example": {
					Root:              filepath.Join(dir, "root"),
					Mirror:            filepath.Join(dir, "mirror.git"),
					Remote:            "origin",
					DefaultBranch:     "main",
					AirelaySessionKey: "example_master",
				},
			},
		},
		Airelay: airelay.Client{Command: script, Timeout: time.Second},
	}
	result, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		SessionID: "SP-FASTTAIL",
		Lines:     30,
	})
	if err != nil {
		t.Fatalf("local tail failed without Hub access: %v", err)
	}
	if !reflect.DeepEqual(result.Lines, []string{"one", "two"}) || !result.HasNewInfo {
		t.Fatalf("unexpected local tail result: %#v", result)
	}
	repeat, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		SessionID: "SP-FASTTAIL",
		Lines:     20,
	})
	if err != nil || len(repeat.Lines) != 0 || repeat.HasNewInfo {
		t.Fatalf("unchanged session tail was not deduplicated: %#v err=%v", repeat, err)
	}
	independent, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		SessionID: "SP-OTHER",
		Lines:     20,
	})
	if err != nil || !reflect.DeepEqual(independent.Lines, []string{"one", "two"}) || !independent.HasNewInfo {
		t.Fatalf("tail state leaked across durable sessions: %#v err=%v", independent, err)
	}
}

func TestAgentTailDeltaReturnsOnlyCurrentViewportSuffix(t *testing.T) {
	selected, hasNew, historyTruncated := agentTailDelta([]string{"old-2", "old-3"}, []string{"old-2", "old-3", "new-4"}, true)
	if !reflect.DeepEqual(selected, []string{"new-4"}) || !hasNew || historyTruncated {
		t.Fatalf("incremental tail delta=%#v new=%v truncated=%v", selected, hasNew, historyTruncated)
	}
	selected, hasNew, historyTruncated = agentTailDelta([]string{"old-2", "old-3"}, []string{"old-2", "old-3"}, true)
	if len(selected) != 0 || hasNew || historyTruncated {
		t.Fatalf("unchanged tail delta=%#v new=%v truncated=%v", selected, hasNew, historyTruncated)
	}
}

func TestAgentTailObservationKeyScopesSessionProjectAndTarget(t *testing.T) {
	s := &Service{}
	firstPath, firstLock := s.agentTailStateLocation("SP-ONE", "project-one", "project-one_master")
	samePath, sameLock := s.agentTailStateLocation("SP-ONE", "project-one", "project-one_master")
	if firstPath != samePath || firstLock != sameLock {
		t.Fatalf("identical observation identity was not stable: %q/%q vs %q/%q", firstPath, firstLock, samePath, sameLock)
	}
	variants := [][3]string{
		{"SP-TWO", "project-one", "project-one_master"},
		{"SP-ONE", "project-two", "project-one_master"},
		{"SP-ONE", "project-one", "project-two_master"},
	}
	for _, variant := range variants {
		path, lock := s.agentTailStateLocation(variant[0], variant[1], variant[2])
		if path == firstPath || lock == firstLock {
			t.Fatalf("observation identity was not isolated for %v: %q/%q", variant, path, lock)
		}
	}
}
