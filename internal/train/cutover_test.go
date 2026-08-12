package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func validCutoverEvidence() CutoverEvidence {
	return CutoverEvidence{
		CurrentExecutionModel:       "legacy",
		MaterializationAcknowledged: true,
		PlanRetirementAcknowledged:  true,
		HistoricalCompatibilityOK:   true,
		IntegrationClean:            true,
		SourceHead:                  strings.Repeat("a", 40),
		MirrorHead:                  strings.Repeat("a", 40),
		RuntimeReady:                true,
		RuntimeVersionMatch:         true,
		RegisteredActions:           append([]string{}, RequiredCutoverActions...),
	}
}

func TestCutoverFailsClosedUntilAllIndependentProofsExist(t *testing.T) {
	evidence := validCutoverEvidence()
	if err := ValidateCutover(evidence); err != nil {
		t.Fatal(err)
	}
	cases := []func(*CutoverEvidence){
		func(e *CutoverEvidence) { e.MaterializationAcknowledged = false },
		func(e *CutoverEvidence) { e.PlanRetirementAcknowledged = false },
		func(e *CutoverEvidence) { e.ActiveLegacyRuns = 1 },
		func(e *CutoverEvidence) { e.ActiveTrains = 1 },
		func(e *CutoverEvidence) { e.HistoricalCompatibilityOK = false },
		func(e *CutoverEvidence) { e.SourceHead = strings.Repeat("b", 40) },
		func(e *CutoverEvidence) { e.RuntimeVersionMatch = false },
		func(e *CutoverEvidence) { e.RegisteredActions = e.RegisteredActions[:len(e.RegisteredActions)-1] },
	}
	for index, mutate := range cases {
		candidate := evidence
		mutate(&candidate)
		if err := ValidateCutover(candidate); err == nil {
			t.Fatalf("cutover case %d was accepted", index)
		}
	}
}

func TestCutoverConfigurationAndReceiptAreIdempotenceInputs(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	configuration := model.DefaultProjectConfiguration("gateway", now)
	updated, err := CutoverConfiguration(configuration, "gateway", now.Add(time.Minute))
	if err != nil || updated.ExecutionModel != "train_v2" || updated.Revision != configuration.Revision+1 {
		t.Fatalf("unexpected cutover configuration: %#v %v", updated, err)
	}
	receipt, err := NewCutoverReceipt("gateway", updated, strings.Repeat("a", 40), strings.Repeat("a", 40), true, true, "gateway", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateTrainV2CutoverReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := CutoverConfiguration(updated, "gateway", now); err == nil {
		t.Fatal("second configuration mutation was accepted")
	}
}

func TestCutoverActionRegistryRejectsDuplicateOrMissingActions(t *testing.T) {
	registered := append([]string{}, RequiredCutoverActions...)
	registered = append(registered, registered[0])
	if err := ValidateActionRegistry(RequiredCutoverActions, registered); err == nil {
		t.Fatal("duplicate action was accepted")
	}
	if err := ValidateActionRegistry(RequiredCutoverActions, registered[:len(registered)-2]); err == nil {
		t.Fatal("missing action was accepted")
	}
}
