package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
)

func TestActivatedJournalRequiresPriorHubCommittedJournalAndPreservesEvidence(t *testing.T) {
	fixture := newActivationFixture(t)
	prior, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	activated := prior
	activated.State = StateActivated
	activated.MirrorProof = &MirrorProof{
		Path:          config.ManagedProjectMirrorPath(fixture.coordinator.StateDir, fixture.request.ProjectID),
		RepositoryURL: fixture.request.RepositoryURL,
		Head:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	activatedAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	activated.Timestamps.ActivatedAt = &activatedAt
	activated.Timestamps.UpdatedAt = activatedAt
	activated.Recovery = Recovery{Status: RecoveryNotRequired}
	if _, err := writeActivatedJournalLocked(fixture.coordinator.StateDir, fixture.request, activated); !errors.Is(err, ErrPreparedJournalNotFound) {
		t.Fatalf("missing prior journal error = %v, want ErrPreparedJournalNotFound", err)
	}
}

func TestRecoveryEvidenceCannotMoveBackward(t *testing.T) {
	fixture := newActivationFixture(t)
	prior, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	step := RecoveryStepManagedMirror
	lastState := StateHubCommitted
	reason := "mirror later failed"
	action := RecoveryActionResumeActivation
	prior.State = StateRecoveryRequired
	prior.MirrorProof = &MirrorProof{
		Path:          config.ManagedProjectMirrorPath(fixture.coordinator.StateDir, fixture.request.ProjectID),
		RepositoryURL: fixture.request.RepositoryURL,
		Head:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	prior.Recovery = Recovery{
		Status:               RecoveryRequired,
		LastCompletedState:   &lastState,
		LastDurableStep:      &step,
		Reason:               &reason,
		SafeCorrectiveAction: &action,
	}
	updated := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	prior.Timestamps.UpdatedAt = updated
	if _, err := writeRecoveryJournalLocked(fixture.coordinator.StateDir, fixture.request, prior); err != nil {
		t.Fatal(err)
	}
	backward := prior
	backwardStep := RecoveryStepHubCommitted
	backward.Recovery.LastDurableStep = &backwardStep
	backward.Recovery.Reason = receiptTestString("earlier failure")
	if _, err := writeRecoveryJournalLocked(fixture.coordinator.StateDir, fixture.request, backward); err == nil {
		t.Fatal("backward recovery transition unexpectedly succeeded")
	}
}

func TestRecoveryJournalMissingDurableStepFailsClosed(t *testing.T) {
	fixture := newActivationFixture(t)
	hooks := testActivationHooks(t)
	hooks.Mirror = func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error) {
		return gitx.MirrorVerification{}, errors.New("injected mirror outage")
	}
	fixture.coordinator.Hooks = hooks
	if _, err := fixture.coordinator.Activate(trustedCoordinatorContext(), fixture.request, fixture.operation); err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	path, err := PreparedJournalPath(fixture.coordinator.StateDir, fixture.operation)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	var recovery map[string]json.RawMessage
	if err := json.Unmarshal(object["recovery"], &recovery); err != nil {
		t.Fatal(err)
	}
	delete(recovery, "last_durable_step")
	object["recovery"], err = json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	corrupt, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(corrupt, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOnboardingJournal(fixture.coordinator.StateDir, fixture.operation); err == nil {
		t.Fatal("corrupt recovery journal unexpectedly loaded")
	}
}

func TestDefaultSessionReadinessRequiresExplicitHealthyStateAndProtocol(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.coordinator.Airelay.Timeout = time.Second
	cases := []struct {
		state, protocol string
		exit            int
		wantErr         bool
	}{
		{state: "running", protocol: "2"},
		{state: "waiting", protocol: "2"},
		{state: "idle", protocol: "2"},
		{state: "free", protocol: "1"},
		{state: "error", protocol: "2", wantErr: true},
		{state: "unknown", protocol: "2", wantErr: true},
		{state: "idle", protocol: "", wantErr: true},
		{state: "idle", protocol: "0", wantErr: true},
		{state: "idle", protocol: "2", exit: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s-%s-exit-%d", tc.state, tc.protocol, tc.exit), func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "airelay-status.sh")
			protocolLine := ""
			if tc.protocol != "" {
				protocolLine = fmt.Sprintf("printf 'Protocol version: %s\\n'\n", tc.protocol)
			}
			body := fmt.Sprintf("#!/bin/sh\nprintf 'Controller: reachable\\n'\nprintf 'State: %s\\n'\n%sexit %d\n", tc.state, protocolLine, tc.exit)
			if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			fixture.coordinator.Airelay.Command = script
			_, err := fixture.coordinator.defaultSessionReadiness(context.Background(), fixture.request)
			if (err != nil) != tc.wantErr {
				t.Fatalf("readiness error = %v, wantErr=%t", err, tc.wantErr)
			}
		})
	}
}
