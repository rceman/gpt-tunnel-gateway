package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func newCallbackDurableService(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	state := t.TempDir()
	db, err := sqlitestore.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	configuration := model.DefaultProjectConfiguration("example", now)
	payload, err := json.Marshal(configuration)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{
		OperationID: "seed-callback-config", EntityType: "project_configuration", EntityID: "example",
		ExpectedRevision: 0, Revision: 1, Kind: "seed", Payload: payload, CreatedAt: now, Create: true,
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.MarkSharedBootstrapComplete(context.Background(), sqlitestore.SharedBootstrapMarker{ProjectID: "example", HubRevision: "fixture", CompletedAt: now.Format(time.RFC3339Nano)}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	s := NewWithDurabilityDeferredWorkers(config.Config{
		StateDir: state,
		Projects: map[string]config.ProjectConfig{"example": {Root: filepath.Join(state, "project"), DefaultBranch: "main"}},
	}, db)
	return s, db
}

func TestCallbackRegistrySharedCASAndCompactedList(t *testing.T) {
	s, db := newCallbackDurableService(t)
	defer db.Close()
	ctx := authority.WithPlanner(context.Background())
	first := model.ProjectCallback{Callback: "z-last", Event: model.ProjectCallbackWorkFinishedEvent, URL: &model.ProjectCallbackURL{Method: "POST", URL: "https://example.invalid/z", Body: "{}"}}
	second := model.ProjectCallback{Callback: "a-first", Event: model.ProjectCallbackWorkFinishedEvent, Script: &model.ProjectCallbackScript{Path: "scripts/callback", Args: []string{"--once"}}}
	if result, err := s.CallbackRegister(ctx, "example", CallbackRegisterInput{Callback: first}); err != nil || result.Status != "registered" {
		t.Fatalf("register first=%#v err=%v", result, err)
	}
	if result, err := s.CallbackRegister(ctx, "example", CallbackRegisterInput{Callback: first}); err != nil || result.Status != "already_registered" {
		t.Fatalf("idempotent register=%#v err=%v", result, err)
	}
	if _, err := s.CallbackRegister(ctx, "example", CallbackRegisterInput{Callback: model.ProjectCallback{Callback: first.Callback, Event: first.Event, URL: &model.ProjectCallbackURL{Method: "PUT", URL: first.URL.URL, Body: first.URL.Body}}}); err == nil {
		t.Fatal("different registration definition was accepted")
	} else {
		var conflict *CallbackConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("conflict error=%T %v", err, err)
		}
	}
	if _, err := s.CallbackRegister(ctx, "example", CallbackRegisterInput{Callback: second}); err != nil {
		t.Fatal(err)
	}
	list, err := s.CallbackList(ctx, "example")
	if err != nil || len(list.Callbacks) != 2 || list.Callbacks[0].Key != "a-first" || list.Callbacks[1].Key != "z-last" {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	if list.Callbacks[0].Script == nil || list.Callbacks[0].URL != nil || list.Callbacks[1].URL == nil || list.Callbacks[1].URL.Method != "POST" {
		t.Fatalf("list summaries were not compact: %#v", list)
	}
	if _, err := s.CallbackRemove(ctx, "example", CallbackRemoveInput{Callback: "a-first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CallbackRemove(ctx, "example", CallbackRemoveInput{Callback: "a-first"}); err == nil {
		t.Fatal("unknown callback removal was accepted")
	} else {
		var notFound *CallbackNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("not-found error=%T %v", err, err)
		}
	}
}

func TestCallbackRegistryRequiresSharedAuthority(t *testing.T) {
	s := New(config.Config{Projects: map[string]config.ProjectConfig{"example": {}}})
	callback := model.ProjectCallback{Callback: "notify", Event: model.ProjectCallbackWorkFinishedEvent, URL: &model.ProjectCallbackURL{Method: "POST", URL: "https://example.invalid", Body: "{}"}}
	if _, err := s.CallbackRegister(authority.WithPlanner(context.Background()), "example", CallbackRegisterInput{Callback: callback}); err == nil {
		t.Fatal("callback registry used a non-Shared fallback")
	}
}

func TestAgentWorkFinishedCallbackDeliversOnceAfterStableIdle(t *testing.T) {
	s, db := newCallbackDurableService(t)
	defer db.Close()
	var bodies chan string
	bodies = make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	callback := model.ProjectCallback{Callback: "work-finished", Event: model.ProjectCallbackWorkFinishedEvent, URL: &model.ProjectCallbackURL{Method: "POST", URL: server.URL, Body: `{"callback":"work-finished"}`}}
	if _, err := s.CallbackRegister(authority.WithPlanner(context.Background()), "example", CallbackRegisterInput{Callback: callback}); err != nil {
		t.Fatal(err)
	}
	statusScript := filepath.Join(s.Config.StateDir, "airelay-status")
	if err := os.WriteFile(statusScript, []byte("#!/bin/sh\nprintf 'Controller: reachable\\nState: idle\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = statusScript
	s.Airelay.Timeout = time.Second
	epoch := sqlitestore.CallbackEpoch{ID: "agent-work-test", ProjectID: "example", AgentID: "coding-example", SessionKey: "example_master", ArmedAt: time.Now().UTC()}
	if err := db.ArmCallbackEpoch(context.Background(), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ObserveCallbackEpoch(context.Background(), epoch.ID, "running"); err != nil {
		t.Fatal(err)
	}
	if err := s.processCallbackEpoch(context.Background(), epoch); err != nil {
		t.Fatal(err)
	}
	if err := s.processCallbackEpoch(context.Background(), epoch); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-bodies:
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil || payload["callback"] != "work-finished" {
			t.Fatalf("callback body=%q err=%v", body, err)
		}
	case <-time.After(time.Second):
		t.Fatal("callback was not delivered")
	}
	select {
	case duplicate := <-bodies:
		t.Fatalf("callback delivered twice: %q", duplicate)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCallbackEventPayloadUsesADR83EntityReferences(t *testing.T) {
	payload, err := callbackEventPayload("agent-work-1", "gpt-tunnel-gateway", "coding")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	if value["project"] != "gpt-tunnel-gateway" || value["agent"] != "coding" || value["epoch"] != "agent-work-1" {
		t.Fatalf("payload=%#v", value)
	}
	for _, legacy := range []string{"project_id", "agent_id", "epoch_id"} {
		if _, ok := value[legacy]; ok {
			t.Fatalf("payload leaked legacy reference %q: %#v", legacy, value)
		}
	}
}

func TestAgentWorkFinishedCallbackFailureIsIsolated(t *testing.T) {
	s, db := newCallbackDurableService(t)
	defer db.Close()
	hits := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.URL.Path
		if r.URL.Path == "/failed" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	ctx := authority.WithPlanner(context.Background())
	for _, callback := range []model.ProjectCallback{
		{Callback: "failed", Event: model.ProjectCallbackWorkFinishedEvent, URL: &model.ProjectCallbackURL{Method: "POST", URL: server.URL + "/failed", Body: `{}`}},
		{Callback: "healthy", Event: model.ProjectCallbackWorkFinishedEvent, URL: &model.ProjectCallbackURL{Method: "POST", URL: server.URL + "/healthy", Body: `{}`}},
	} {
		if _, err := s.CallbackRegister(ctx, "example", CallbackRegisterInput{Callback: callback}); err != nil {
			t.Fatal(err)
		}
	}
	statusScript := filepath.Join(s.Config.StateDir, "airelay-status")
	if err := os.WriteFile(statusScript, []byte("#!/bin/sh\nprintf 'Controller: reachable\\nState: idle\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = statusScript
	s.Airelay.Timeout = time.Second
	epoch := sqlitestore.CallbackEpoch{ID: "agent-work-isolated", ProjectID: "example", AgentID: "coding-example", SessionKey: "example_master", ArmedAt: time.Now().UTC()}
	if err := db.ArmCallbackEpoch(context.Background(), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ObserveCallbackEpoch(context.Background(), epoch.ID, "running"); err != nil {
		t.Fatal(err)
	}
	if err := s.processCallbackEpoch(context.Background(), epoch); err != nil {
		t.Fatal("first idle observation: ", err)
	}
	if err := s.processCallbackEpoch(context.Background(), epoch); err == nil {
		t.Fatal("failed callback was not reported")
	}
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case path := <-hits:
			seen[path] = true
		case <-time.After(time.Second):
			t.Fatalf("callback paths=%v", seen)
		}
	}
	if !seen["/failed"] || !seen["/healthy"] {
		t.Fatalf("callback failure was not isolated: %v", seen)
	}
}
