package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentSendUsesConfiguredSessionAndDoesNotMutateHub(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+argsPath+"\"\nprintf 'delivered\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.AgentSend(context.Background(), "example", "hello agent")
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Delivered || receipt.ExitCode != 0 || receipt.ProjectID != "example" || receipt.Stdout != "delivered\n" {
		t.Fatalf("unexpected delivery receipt: %#v", receipt)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "prompt\nexample_master\nhello agent\n" {
		t.Fatalf("unexpected Airelay argv: %q", args)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || before != after {
		t.Fatalf("agent send mutated durable hub: before=%s after=%s err=%v", before, after, err)
	}
}

func TestAgentSendSerializesPerConfiguredSession(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.2\nprintf 'ok\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	firstErr := make(chan error, 1)
	go func() {
		_, err := s.AgentSend(context.Background(), "example", "first")
		firstErr <- err
	}()
	time.Sleep(40 * time.Millisecond)
	_, second := s.AgentSend(context.Background(), "example", "second")
	if second == nil || !strings.Contains(second.Error(), "already in progress") {
		t.Fatalf("concurrent send was not serialized: %v", second)
	}
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
}

func TestAgentTailSupportsDefaultAndViewport(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsPath + "\"\nprintf 'one\\ntwo\\nthree\\nfour\\nfive\\nsix\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	result, err := s.AgentTail(context.Background(), "example", 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID != "example" || result.Lines != 10 || result.Text != "one\ntwo\nthree\nfour\nfive\nsix\n" {
		t.Fatalf("unexpected agent tail: %#v", result)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "tail\nexample_master\n--lines\n10\n" {
		t.Fatalf("unexpected tail argv: %q", args)
	}
	result, err = s.AgentTail(context.Background(), "example", -1)
	if err != nil || result.Lines != -1 {
		t.Fatalf("unexpected full viewport: %#v err=%v", result, err)
	}
	args, err = os.ReadFile(argsPath)
	if err != nil || string(args) != "tail\nexample_master\n--lines\n30\n" {
		t.Fatalf("unexpected full viewport argv: %q err=%v", args, err)
	}

	transcriptScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsPath + "\"\nprintf 'history\\n'\n"
	if err := os.WriteFile(script, []byte(transcriptScript), 0o700); err != nil {
		t.Fatal(err)
	}
	transcript, err := s.AgentTranscript(context.Background(), "example", 0, 2)
	if err != nil || transcript.Lines != 50 || transcript.Skip != 2 || transcript.Text != "history\n" {
		t.Fatalf("unexpected transcript: %#v err=%v", transcript, err)
	}
	args, err = os.ReadFile(argsPath)
	if err != nil || string(args) != "transcript\nexample_master\n--lines\n50\n--skip\n2\n--order\ndesc\n" {
		t.Fatalf("unexpected transcript argv: %q err=%v", args, err)
	}
}

func TestAgentStatusExposesStateAndCapacityWarnings(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf 'Controller: reachable (5ms)\\nAirelay version: 0.1.54\\nProtocol version: 1\\nState: busy\\n⚠ Selected model is at capacity.\\n⚠ weekly limit left\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	result, err := s.AgentStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "running" || !result.ControllerReachable || result.AirelayVersion != "0.1.54" || result.ProtocolVersion != "1" || len(result.CapacityWarnings) != 2 || result.ExitCode != 0 {
		t.Fatalf("unexpected agent status: %#v", result)
	}
}

func TestAgentSessionRequiresConfiguredRegisteredProject(t *testing.T) {
	s, _, _ := testService(t)
	if _, err := s.AgentStatus(context.Background(), "caller_supplied_session"); err == nil || !strings.Contains(err.Error(), "unknown local project") {
		t.Fatalf("arbitrary session/project was accepted: %v", err)
	}
}
