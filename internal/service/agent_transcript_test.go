package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestAgentTailUsesExplicitSessionWithoutDurableAgentLookup(t *testing.T) {
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
		SessionID:  "SP-FASTTAIL",
		SessionKey: "example_master",
		Lines:      30,
	})
	if err != nil {
		t.Fatalf("local tail failed without Hub access: %v", err)
	}
	if !reflect.DeepEqual(result.Lines, []string{"one", "two"}) || !result.HasNewInfo {
		t.Fatalf("unexpected local tail result: %#v", result)
	}
	repeat, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		SessionID:  "SP-FASTTAIL",
		SessionKey: "example_master",
		Lines:      20,
	})
	if err != nil || len(repeat.Lines) != 0 || repeat.HasNewInfo {
		t.Fatalf("unchanged session tail was not deduplicated: %#v err=%v", repeat, err)
	}
	independent, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		SessionID:  "SP-OTHER",
		SessionKey: "example_master",
		Lines:      20,
	})
	if err != nil || !reflect.DeepEqual(independent.Lines, []string{"one", "two"}) || !independent.HasNewInfo {
		t.Fatalf("tail state leaked across durable sessions: %#v err=%v", independent, err)
	}
}

func TestAgentTailRequiresValidExplicitSession(t *testing.T) {
	s := &Service{Config: config.Config{StateDir: t.TempDir(), Projects: map[string]config.ProjectConfig{
		"example": {AirelaySessionKey: "configured-fallback"},
	}}}
	for _, session := range []string{"", "bad session", "bad\nvalue"} {
		_, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{SessionKey: session})
		if err == nil {
			t.Fatalf("session %q was accepted without a valid exact session", session)
		}
	}
}

func TestResolveAgentTailSessionUsesDurableBindingAndRejectsInvalidRecords(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlitestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewWithDurabilityDeferredWorkers(config.Config{
		StateDir: dir,
		Projects: map[string]config.ProjectConfig{"example": {AirelaySessionKey: "configured-fallback"}},
	}, db)
	store := durableSession.NewStoreWithDurability(db)
	ref := "durable-agent-ref"
	active, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveAgentTailSession(context.Background(), "example", active.ID)
	if err != nil || resolved != ref {
		t.Fatalf("durable Agent binding=%q err=%v, want %q", resolved, err, ref)
	}
	wrongProject, err := store.Create(durableSession.CreateInput{ProjectID: "other", ProjectCode: "OTH", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.End(ended.ID); err != nil {
		t.Fatal(err)
	}
	nilRef, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	invalidRef := "not a valid ref"
	invalidRecord := durableSession.Record{
		SchemaVersion: durableSession.SchemaVersion, ID: "SA-ABC-1234", ProjectID: "example", ProjectCode: "EXM",
		Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &invalidRef,
		Status: durableSession.StatusActive, CreatedAt: time.Now().UTC(), StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(invalidRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateLocalSession(context.Background(), sqlitestore.LocalSession{ID: invalidRecord.ID, Payload: payload, UpdatedAt: invalidRecord.UpdatedAt.Format(time.RFC3339Nano), Status: invalidRecord.Status}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, sessionID string
	}{
		{"unknown", "SA-ABC-9999"},
		{"wrong project", wrongProject.ID},
		{"planner role", planner.ID},
		{"inactive", ended.ID},
		{"nil ref", nilRef.ID},
		{"invalid ref", invalidRecord.ID},
		{"invalid selector", "not-an-agent-session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := s.ResolveAgentTailSession(context.Background(), "example", test.sessionID); err == nil {
				t.Fatalf("session %q was accepted", test.sessionID)
			}
		})
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

func TestAgentTailContinuationPreservesUnreadBacklogAcrossBudgets(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ ! -f '"+filepath.Join(dir, "seen")+"' ]; then touch '"+filepath.Join(dir, "seen")+"'; printf 'one\\ntwo\\n'; else printf 'one\\ntwo\\nthree\\nfour\\nfive\\nsix\\nseven\\neight\\nnine\\nten\\neleven\\ntwelve\\nthirteen\\nfourteen\\nfifteen\\n'; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Service{
		Config:  config.Config{StateDir: dir, Projects: map[string]config.ProjectConfig{"example": {Root: filepath.Join(dir, "root"), Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}},
		Airelay: airelay.Client{Command: script, Timeout: time.Second},
	}
	input := AgentTailInput{
		SessionID:       "SP-BACKLOG",
		SessionKey:      "example_master",
		Lines:           3,
		PreserveBacklog: true,
	}
	first, err := s.AgentTailPage(context.Background(), "example", input)
	if err != nil || len(first.Lines) != 2 {
		t.Fatalf("initial continuation tail=%#v err=%v", first, err)
	}
	second, err := s.AgentTailPage(context.Background(), "example", input)
	if err != nil || !reflect.DeepEqual(second.Lines, []string{"three", "four", "five"}) || !second.Overflow {
		t.Fatalf("first backlog chunk=%#v err=%v", second, err)
	}
	third, err := s.AgentTailPage(context.Background(), "example", input)
	if err != nil || !reflect.DeepEqual(third.Lines, []string{"six", "seven", "eight"}) || !third.Overflow {
		t.Fatalf("second backlog chunk skipped unread lines: %#v err=%v", third, err)
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
