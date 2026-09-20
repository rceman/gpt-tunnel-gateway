package airelay

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureDetachedStartsOnceThenReusesHealthyWorker(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	starts := filepath.Join(dir, "starts")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n  detached)\n    if [ ! -f \"" + state + "\" ]; then printf '%s\\n' '[]'; else printf '%s\\n' '[{\"sessionKey\":\"widget_worker\",\"runtimeId\":\"runtime-1\",\"profile\":\"codex\",\"controllerReachable\":true}]'; fi\n    ;;\n  session-status)\n    if [ ! -f \"" + state + "\" ]; then exit 1; fi\n    printf '%s\\n' '{\"sessionKey\":\"widget_worker\",\"profile\":\"codex\",\"controllerReachable\":true,\"state\":\"idle\"}'\n    ;;\n  history) printf '%s\\n' '[]' ;;\n  start) touch \"" + state + "\"; printf '%s\\n' started >> \"" + starts + "\" ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	client := Client{
		Command: script,
		Timeout: time.Second,
	}
	first, err := client.EnsureDetached(context.Background(), "codex", "widget_worker")
	if err != nil || first.Status != "started" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := client.EnsureDetached(context.Background(), "codex", "widget_worker")
	if err != nil || second.Status != "reused" {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	data, err := os.ReadFile(starts)
	if err != nil || strings.Count(string(data), "started\n") != 1 {
		t.Fatalf("starts=%q err=%v", data, err)
	}
}

func TestEnsureDetachedFailsClosedOnProfileConflict(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	stops := filepath.Join(dir, "stops")
	if err := os.WriteFile(state, []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n  sessions|history) printf '%s\\n' '[]' ;;\n  stop) printf '%s\\n' stopped >> \"" + stops + "\" ;;\n  detached) printf '%s\\n' '[{\"sessionKey\":\"widget_worker\",\"runtimeId\":\"runtime-1\",\"profile\":\"devin\",\"controllerReachable\":true}]' ;;\n  *) printf '%s\\n' '{\"sessionKey\":\"widget_worker\",\"profile\":\"devin\",\"controllerReachable\":true,\"state\":\"idle\"}' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	client := Client{
		Command: script,
		Timeout: time.Second,
	}
	if _, err := client.EnsureDetached(context.Background(), "codex", "widget_worker"); err == nil || !strings.Contains(err.Error(), "conflicting profile") {
		t.Fatalf("profile conflict was not rejected: %v", err)
	}
	if _, err := os.Stat(stops); !os.IsNotExist(err) {
		t.Fatalf("pre-existing conflicting Worker was stopped: err=%v", err)
	}
}

func TestEnsureDetachedRecoversStoppedWorker(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	starts := filepath.Join(dir, "starts")
	stops := filepath.Join(dir, "stops")
	if err := os.WriteFile(state, []byte("stopped"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n  detached)\n    if [ -f \"" + state + "\" ]; then printf '%s\\n' '[{\"sessionKey\":\"widget_worker\",\"runtimeId\":\"runtime-1\",\"profile\":\"codex\",\"controllerReachable\":true}]'; else printf '%s\\n' '[]'; fi\n    ;;\n  session-status)\n    if [ ! -f \"" + state + "\" ]; then exit 1; fi\n    if [ -f \"" + starts + "\" ]; then printf '%s\\n' '{\"sessionKey\":\"widget_worker\",\"profile\":\"codex\",\"controllerReachable\":true,\"state\":\"idle\"}'; else printf '%s\\n' '{\"sessionKey\":\"widget_worker\",\"profile\":\"codex\",\"controllerReachable\":true,\"state\":\"stopped\"}'; fi\n    ;;\n  history) printf '%s\\n' '[]' ;;\n  start) touch \"" + state + "\"; touch \"" + starts + "\" ;;\n  stop) touch \"" + stops + "\" ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	client := Client{
		Command: script,
		Timeout: time.Second,
	}
	result, err := client.EnsureDetached(context.Background(), "codex", "widget_worker")
	if err != nil || !result.Started {
		t.Fatalf("stopped Worker was not resumed: result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(stops); !os.IsNotExist(err) {
		t.Fatalf("successful recovery unexpectedly stopped Worker: err=%v", err)
	}
}

func TestEnsureDetachedStopsOnlyAWorkerStartedByThisCallOnVerificationFailure(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	starts := filepath.Join(dir, "starts")
	stops := filepath.Join(dir, "stops")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n  detached) if [ -f \"" + starts + "\" ]; then printf '%s\\n' '[]'; else printf '%s\\n' '[]'; fi ;;\n  session-status) exit 1 ;;\n  history) printf '%s\\n' '[]' ;;\n  start) touch \"" + state + "\"; touch \"" + starts + "\" ;;\n  stop) touch \"" + stops + "\" ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	client := Client{
		Command: script,
		Timeout: time.Second,
	}
	if _, err := client.EnsureDetached(context.Background(), "codex", "widget_worker"); err == nil || !strings.Contains(err.Error(), "started Worker") {
		t.Fatalf("post-start verification unexpectedly succeeded: %v", err)
	}
	if _, err := os.Stat(stops); err != nil {
		t.Fatalf("new Worker rollback did not stop the runtime: %v", err)
	}
}

func TestStopDetachedUsesOnlyTheServerOwnedKeyArgument(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + args + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (Client{
		Command: script,
		Timeout: time.Second,
	}).StopDetached(context.Background(), "widget_worker"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args)
	if err != nil || string(data) != "stop\nwidget_worker\n" {
		t.Fatalf("stop args=%q err=%v", data, err)
	}
}
