package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCandidateDebugActivateMCPNetworkE2E(t *testing.T) {
	fixture := prepareCandidateDebugActivation(t)
	finishCandidateDebugActivation(fixture)
}

func finishCandidateDebugActivation(f candidateDebugActivationFixture) {
	t := f.t
	activationTimeout := f.activationTimeout
	wantSource := f.wantSource
	sourceFixture := f.sourceFixture
	stateDir := f.stateDir
	pidDir := f.pidDir
	logDir := f.logDir
	installDir := f.installDir
	tunnelPID := f.tunnelPID
	initialPID := f.initialPID
	tunnelRequests := f.tunnelRequests
	failSecondHealth := f.failSecondHealth
	client := f.client
	sessionID := f.sessionID
	listenAddr := f.listenAddr
	first, err := client.call(sessionID, "debug/activate", map[string]any{"main_sha": wantSource})
	if err != nil {
		t.Fatalf("debug/activate response failed: %v", err)
	}
	if first.StatusCode != http.StatusOK {
		t.Fatalf("debug/activate success status=%d body=%s", first.StatusCode, first.Body)
	}
	if first.ContentLength != int64(len(first.Body)) {
		t.Fatalf("debug/activate success Content-Length=%d body_bytes=%d", first.ContentLength, len(first.Body))
	}
	firstResult := candidateMCPStructured(t, first.Body)
	if firstResult["source_head"] != wantSource || firstResult["activation"] != "accepted" || firstResult["outcome"] != "accepted" {
		t.Fatalf("debug/activate success result=%#v", firstResult)
	}
	newPID := waitCandidatePIDChange(filepath.Join(pidDir, "gateway.pid"), initialPID, activationTimeout)
	if newPID < 1 || newPID == initialPID {
		t.Fatalf("successful debug activation did not replace Gateway: old=%d new=%d", initialPID, newPID)
	}
	if err := waitCandidateHTTP(listenAddr, "/readyz", activationTimeout); err != nil {
		t.Fatal(err)
	}
	assertInstalledCandidate(t, installDir, wantSource, "0.6.14")
	if got := readCandidatePID(filepath.Join(pidDir, "tunnel.pid")); got != tunnelPID {
		t.Fatalf("debug activation changed Tunnel PID record: got=%d want=%d", got, tunnelPID)
	}
	if !processExists(tunnelPID) {
		t.Fatal("Tunnel process exited during successful Gateway-only activation")
	}
	waitCandidateDebugActivationSuccess(t, client, sessionID, wantSource, activationTimeout)

	artifactBefore := make(map[string]string, len(releaseartifacts.BinaryNames))
	for _, name := range releaseartifacts.BinaryNames {
		hash, hashErr := releaseartifacts.HashFile(filepath.Join(installDir, name))
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		artifactBefore[name] = hash
	}
	testutil.Git(t, sourceFixture, "-c", "user.name=debug-e2e", "-c", "user.email=debug-e2e@example.invalid", "commit", "--allow-empty", "-m", "debug-activation-failure")
	failureSource := strings.TrimSpace(testutil.Git(t, sourceFixture, "rev-parse", "HEAD"))
	if failureSource == wantSource || len(failureSource) != 40 {
		t.Fatalf("failure source=%q want a distinct exact commit", failureSource)
	}
	failSecondHealth.Store(true)
	tunnelRequests.Store(0)
	beforeRollbackPID := readCandidatePID(filepath.Join(pidDir, "gateway.pid"))
	rollback, err := client.call(sessionID, "debug/activate", map[string]any{"main_sha": failureSource})
	if err != nil {
		t.Fatalf("debug/activate rollback response failed: %v", err)
	}
	if rollback.StatusCode != http.StatusOK {
		t.Fatalf("debug/activate rollback status=%d body=%s", rollback.StatusCode, rollback.Body)
	}
	if rollback.ContentLength != int64(len(rollback.Body)) {
		t.Fatalf("debug/activate rollback Content-Length=%d body_bytes=%d", rollback.ContentLength, len(rollback.Body))
	}
	rollbackResult := candidateMCPStructured(t, rollback.Body)
	if rollbackResult["source_head"] != failureSource || rollbackResult["activation"] != "accepted" || rollbackResult["outcome"] != "accepted" {
		t.Fatalf("debug/activate rollback result=%#v", rollbackResult)
	}
	rolledBackPID := waitCandidateArtifactRestore(t, installDir, artifactBefore, filepath.Join(pidDir, "gateway.pid"), beforeRollbackPID, activationTimeout)
	if rolledBackPID < 1 || rolledBackPID == beforeRollbackPID {
		if entries, walkErr := os.ReadDir(logDir); walkErr == nil {
			for _, entry := range entries {
				data, readErr := os.ReadFile(filepath.Join(logDir, entry.Name()))
				if readErr == nil {
					t.Logf("debug activation log %s: %s", entry.Name(), data)
				}
			}
		}
		t.Fatalf("rollback did not restart the restored Gateway: before=%d after=%d", beforeRollbackPID, rolledBackPID)
	}
	if err := waitCandidateHTTP(listenAddr, "/readyz", activationTimeout); err != nil {
		t.Fatal(err)
	}
	waitCandidateDebugActivationFailureReceipt(t, stateDir, failureSource, activationTimeout)
	terminalFailure, err := client.call(sessionID, "debug/activate", map[string]any{"main_sha": failureSource})
	if err != nil {
		t.Fatalf("debug/activate terminal failure response returned EOF/transport error: %v", err)
	}
	if terminalFailure.StatusCode != http.StatusOK {
		t.Fatalf("debug/activate terminal failure status=%d body=%s", terminalFailure.StatusCode, terminalFailure.Body)
	}
	var terminalEnvelope map[string]any
	if err := json.Unmarshal(terminalFailure.Body, &terminalEnvelope); err != nil {
		t.Fatalf("debug/activate terminal failure JSON: %v: %s", err, terminalFailure.Body)
	}
	terminalResult, _ := terminalEnvelope["result"].(map[string]any)
	terminalStructured, _ := terminalResult["structuredContent"].(map[string]any)
	terminalError, _ := terminalStructured["error"].(map[string]any)
	if terminalStructured["ok"] != false || terminalError["code"] != "GATEWAY_DEBUG_ACTIVATION_FAILED" {
		t.Fatalf("debug/activate terminal failure=%#v", terminalEnvelope)
	}
	for _, name := range releaseartifacts.BinaryNames {
		got, hashErr := releaseartifacts.HashFile(filepath.Join(installDir, name))
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		if got != artifactBefore[name] {
			t.Fatalf("rollback artifact %s hash=%s want restored %s", name, got, artifactBefore[name])
		}
	}
	if !processExists(tunnelPID) {
		t.Fatal("Tunnel process exited during Gateway rollback")
	}
	postRollback, err := client.request("initialize", map[string]any{})
	if err != nil || postRollback.StatusCode != http.StatusOK {
		t.Fatalf("post-rollback MCP status=%d err=%v body=%s", postRollback.StatusCode, err, postRollback.Body)
	}
	t.Logf("debug_activation_source=%s gateway_pid_initial=%d gateway_pid_success=%d gateway_pid_rollback=%d tunnel_pid=%d tunnel_health_requests=%d", wantSource, initialPID, newPID, rolledBackPID, tunnelPID, tunnelRequests.Load())
}
