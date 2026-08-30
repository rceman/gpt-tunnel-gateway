package callbackdelivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestDeliverScriptUsesCleanCanonicalCheckoutAndRejectsEscape(t *testing.T) {
	_, root, _ := testutil.RepoWithBareRemote(t)
	scriptPath := filepath.Join(root, "callback.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ncat > callback-result.json\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "callback.sh")
	testutil.Git(t, root, "commit", "-m", "callback")
	project := config.ProjectConfig{Root: root, DefaultBranch: "main"}
	callback := model.ProjectCallback{Callback: "script", Event: model.ProjectCallbackWorkFinishedEvent, Script: &model.ProjectCallbackScript{Path: "callback.sh", Args: []string{}}}
	if err := Deliver(context.Background(), callback, project, gitx.Runner{MaxReadBytes: 1 << 20}, []byte(`{"event":"agent.work_finished"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "callback-result.json"))
	if err != nil || string(data) != `{"event":"agent.work_finished"}` {
		t.Fatalf("script result=%q err=%v", data, err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	callback.Script.Path = "escape/callback.sh"
	if err := Deliver(context.Background(), callback, project, gitx.Runner{MaxReadBytes: 1 << 20}, nil); err == nil {
		t.Fatal("symlink escape was accepted")
	}
	if !strings.Contains(string(data), `agent.work_finished`) {
		t.Fatalf("script did not receive event payload: %q", data)
	}
}

func TestDeliverCombinedTargetsRunsBothIndependently(t *testing.T) {
	_, root, _ := testutil.RepoWithBareRemote(t)
	resultPath := filepath.Join(root, "combined-result.json")
	scriptPath := filepath.Join(root, "combined.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ncat > combined-result.json\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "combined.sh")
	testutil.Git(t, root, "commit", "-m", "combined callback")
	var requests atomic.Int32
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	project := config.ProjectConfig{Root: root, DefaultBranch: "main"}
	callback := model.ProjectCallback{
		Callback: "combined", Event: model.ProjectCallbackWorkFinishedEvent,
		URL:    &model.ProjectCallbackURL{Method: "POST", URL: server.URL, Body: `{"configured":true}`},
		Script: &model.ProjectCallbackScript{Path: "combined.sh", Args: []string{}},
	}
	event := []byte(`{"event":"agent.work_finished"}`)
	if err := Deliver(context.Background(), callback, project, gitx.Runner{MaxReadBytes: 1 << 20}, event); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || body != `{"configured":true}` {
		t.Fatalf("HTTP delivery requests=%d body=%q", requests.Load(), body)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil || string(data) != string(event) {
		t.Fatalf("script delivery=%q err=%v", data, err)
	}
}

func TestDeliverCombinedTargetFailureDoesNotSkipOtherTarget(t *testing.T) {
	_, root, _ := testutil.RepoWithBareRemote(t)
	resultPath := filepath.Join(root, "failure-isolation-result.json")
	scriptPath := filepath.Join(root, "failure-isolation.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ncat > failure-isolation-result.json\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "failure-isolation.sh")
	testutil.Git(t, root, "commit", "-m", "failure isolation callback")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The script target must still run when the URL target fails.
		// The response body is intentionally empty and bounded.
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	project := config.ProjectConfig{Root: root, DefaultBranch: "main"}
	callback := model.ProjectCallback{
		Callback: "failure-isolation", Event: model.ProjectCallbackWorkFinishedEvent,
		URL:    &model.ProjectCallbackURL{Method: "POST", URL: server.URL, Body: `{}`},
		Script: &model.ProjectCallbackScript{Path: "failure-isolation.sh", Args: []string{}},
	}
	if err := Deliver(context.Background(), callback, project, gitx.Runner{MaxReadBytes: 1 << 20}, []byte(`{"event":"agent.work_finished"}`)); err == nil {
		t.Fatal("combined delivery hid the failed HTTP target")
	}
	data, err := os.ReadFile(resultPath)
	if err != nil || !strings.Contains(string(data), "agent.work_finished") {
		t.Fatalf("script delivery after HTTP result=%q err=%v", data, err)
	}
}

func TestDeliverCombinedTimeoutDoesNotSkipOtherTarget(t *testing.T) {
	_, root, _ := testutil.RepoWithBareRemote(t)
	resultPath := filepath.Join(root, "timeout-isolation-result.json")
	scriptPath := filepath.Join(root, "timeout-isolation.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ncat > timeout-isolation-result.json\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "timeout-isolation.sh")
	testutil.Git(t, root, "commit", "-m", "timeout isolation callback")
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	project := config.ProjectConfig{Root: root, DefaultBranch: "main"}
	callback := model.ProjectCallback{
		Callback: "timeout-isolation", Event: model.ProjectCallbackWorkFinishedEvent,
		URL:    &model.ProjectCallbackURL{Method: "POST", URL: server.URL, Body: `{}`},
		Script: &model.ProjectCallbackScript{Path: "timeout-isolation.sh", Args: []string{}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := Deliver(ctx, callback, project, gitx.Runner{MaxReadBytes: 1 << 20}, []byte(`{"event":"agent.work_finished"}`)); err == nil {
		t.Fatal("timed-out HTTP target was not reported")
	}
	data, err := os.ReadFile(resultPath)
	if err != nil || !strings.Contains(string(data), "agent.work_finished") {
		t.Fatalf("script delivery after HTTP timeout=%q err=%v", data, err)
	}
}
