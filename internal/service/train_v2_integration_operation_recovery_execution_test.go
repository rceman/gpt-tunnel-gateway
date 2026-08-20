package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestMigratedRecoveryCompletesThroughTrainV2IntegrateAndReceipt(t *testing.T) {
	s, input, head := prepareCanonicalRecoveryIntegration(t, "GTW-TRN307")
	receipt, _, err := s.TrainV2Integrate(context.Background(), input)
	if err != nil || receipt.Status != "completed" {
		t.Fatalf("canonical recovery integration failed: receipt=%#v err=%v", receipt, err)
	}
	stored, err := s.readTrainV2IntegrationReceipt(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || stored.Status != "completed" || stored.IntegrationHead != head {
		t.Fatalf("completion receipt=%#v err=%v", stored, err)
	}
	train, err := s.TrainV2Read(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || train.Status != model.TrainV2Completed {
		t.Fatalf("Train was not marked integrated: %#v err=%v", train, err)
	}
}
func TestMigratedRecoveryCompetingOwnerIsBlockedByTrainV2Integrate(t *testing.T) {
	s, input, _ := prepareCanonicalRecoveryIntegration(t, "GTW-TRN308")
	owner, err := s.acquireTrainV2IntegrationLock(context.Background(), input.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = owner.Release()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := s.TrainV2Integrate(ctx, input)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("TrainV2Integrate bypassed competing owner: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}
	released = true
	if err := <-result; err != nil {
		t.Fatalf("TrainV2Integrate failed after competing owner released: %v", err)
	}
}
func TestIntegrationOperationRejectsMigratedRecoverySourceOrTargetDrift(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN304",
	}
	source := strings.Repeat("c", 40)
	target := strings.Repeat("d", 40)
	old, _ := seedMigratedRecoveryEvidence(t, s, revision, input.TrainID, source, target)
	if _, err := s.integrationOperation(context.Background(), input, strings.Repeat("e", 40), "main", target, false, nowUTC()); err == nil || !strings.Contains(err.Error(), "migrated identity") {
		t.Fatalf("source drift was accepted: %v", err)
	}
	current, err := s.readIntegrationOperation(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || current.OperationID != old.OperationID || current.Phase != trainv2.IntegrationPhaseRecoveryRequired {
		t.Fatalf("source drift mutated recovery operation: %#v err=%v", current, err)
	}
	if _, err := s.integrationOperation(context.Background(), input, source, "main", strings.Repeat("e", 40), false, nowUTC()); err == nil || !strings.Contains(err.Error(), "migrated identity") {
		t.Fatalf("target drift was accepted: %v", err)
	}
	current, err = s.readIntegrationOperation(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || current.OperationID != old.OperationID || current.Phase != trainv2.IntegrationPhaseRecoveryRequired {
		t.Fatalf("target drift mutated recovery operation: %#v err=%v", current, err)
	}
}
func TestMigratedRecoveryOperationReachesCompletedPhase(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN305",
	}
	source := strings.Repeat("c", 40)
	target := strings.Repeat("d", 40)
	seedMigratedRecoveryEvidence(t, s, revision, input.TrainID, source, target)
	operation, err := s.integrationOperation(context.Background(), input, source, "main", target, false, nowUTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{
		trainv2.IntegrationPhasePreComplete,
		trainv2.IntegrationPhaseIntegratePending,
		trainv2.IntegrationPhaseIntegrateComplete,
		trainv2.IntegrationPhasePostPending,
		trainv2.IntegrationPhaseCompleted,
	} {
		operation, err = s.advanceIntegrationOperation(context.Background(), operation, phase, phase+" evidence")
		if err != nil {
			t.Fatalf("advance to %s: %v", phase, err)
		}
	}
	current, err := s.readIntegrationOperation(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || current.OperationID != operation.OperationID || current.Phase != trainv2.IntegrationPhaseCompleted {
		t.Fatalf("completed recovery operation=%#v err=%v", current, err)
	}
}
func TestMigratedRecoveryCompetingIntegrationOwnerWaitsForProjectLock(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	first, err := s.acquireTrainV2IntegrationLock(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = first.Release()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		second, err := s.acquireTrainV2IntegrationLock(ctx, "example")
		if err == nil {
			_ = second.Release()
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("competing integration owner was admitted early: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	released = true
	if err := <-result; err != nil {
		t.Fatalf("queued recovery owner did not acquire after release: %v", err)
	}
}
