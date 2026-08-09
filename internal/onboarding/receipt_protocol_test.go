package onboarding

import (
	"bytes"
	"strings"
	"testing"
)

func TestControllerProtocolJSONNumberParity(t *testing.T) {
	request := receiptTestRequest(t)
	if !request.Airelay.SessionRequired {
		t.Fatal("protocol parity fixture must require a session")
	}
	for _, spelling := range []string{"1.0", "1e0"} {
		t.Run("accept_"+spelling, func(t *testing.T) {
			decoded, err := DecodeReceipt(receiptProtocolJSON(t, request, spelling))
			if err != nil {
				t.Fatalf("DecodeReceipt rejected integral protocol spelling %s: %v", spelling, err)
			}
			if err := ValidatePreparedReceipt(decoded, request); err != nil {
				t.Fatalf("ValidatePreparedReceipt rejected integral protocol spelling %s: %v", spelling, err)
			}
		})
	}
	for _, spelling := range []string{
		"true",
		"1.5",
		"0",
		"-1",
		"NaN",
		"Infinity",
		"9007199254740992",
		"1e999999",
	} {
		t.Run("reject_"+spelling, func(t *testing.T) {
			if _, err := DecodeReceipt(receiptProtocolJSON(t, request, spelling)); err == nil {
				t.Fatalf("DecodeReceipt accepted invalid protocol value %s", spelling)
			}
		})
	}
}

func TestValidatePreparedReceiptRejectsProgrammaticUnsafeProtocol(t *testing.T) {
	request := receiptTestRequest(t)
	receipt := preparedReceiptForTest(t, request)
	receipt.SessionProof.ControllerProtocolVersion = receiptTestPositive(MaxSafeInteger + 1)
	if err := ValidatePreparedReceipt(receipt, request); err == nil {
		t.Fatal("ValidatePreparedReceipt accepted protocol above the JSON-safe maximum")
	}
}

func TestValidatePreparedReceiptTimestampsAndState(t *testing.T) {
	request := receiptTestRequest(t)
	base := preparedReceiptForTest(t, request)
	tests := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "missing prepared time", mutate: func(receipt *Receipt) { receipt.Timestamps.PreparedAt = nil }},
		{name: "started equals prepared", mutate: func(receipt *Receipt) { receipt.Timestamps.StartedAt = *receipt.Timestamps.PreparedAt }},
		{name: "prepared after updated", mutate: func(receipt *Receipt) { receipt.Timestamps.UpdatedAt = "2026-08-05T09:00:30Z" }},
		{name: "timezone missing", mutate: func(receipt *Receipt) { receipt.Timestamps.StartedAt = "2026-08-05T09:00:00" }},
		{name: "later recovery state", mutate: func(receipt *Receipt) { receipt.Recovery.Status = "recovery_required" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := base
			test.mutate(&receipt)
			if err := ValidatePreparedReceipt(receipt, request); err == nil {
				t.Fatalf("ValidatePreparedReceipt accepted invalid %s", test.name)
			}
		})
	}

	lowercaseT := base
	lowercaseT.Timestamps.StartedAt = "2026-08-05t09:00:00Z"
	lowercaseT.Timestamps.PreparedAt = receiptTestString("2026-08-05t09:01:00Z")
	lowercaseT.Timestamps.UpdatedAt = "2026-08-05t09:01:00Z"
	if err := ValidatePreparedReceipt(lowercaseT, request); err != nil {
		t.Fatalf("lowercase t should be accepted: %v", err)
	}

	for _, state := range []ReceiptState{StateHubCommitted, StateActivated, StateRecoveryRequired, StateRolledBack} {
		receipt := base
		receipt.State = state
		if err := ValidatePreparedReceipt(receipt, request); err == nil || !strings.Contains(err.Error(), "unsupported receipt state") {
			t.Fatalf("state %q did not produce explicit unsupported-state error: %v", state, err)
		}
	}
}

func TestDecodeReceiptAcceptsIntegralJSONNumberSpellings(t *testing.T) {
	request := receiptTestRequest(t)
	data := receiptJSON(t, preparedReceiptForTest(t, request))
	data = bytes.Replace(data, []byte(`"schema_version":1`), []byte(`"schema_version":1.0`), 1)
	decoded, err := DecodeReceipt(data)
	if err != nil {
		t.Fatalf("integral JSON number spelling should decode: %v", err)
	}
	if err := ValidatePreparedReceipt(decoded, request); err != nil {
		t.Fatalf("integral JSON number spelling should validate: %v", err)
	}
	for _, spelling := range []string{`false`, `1.5`, `0`, `18446744073709551616`} {
		invalid := bytes.Replace(data, []byte(`"schema_version":1.0`), []byte(`"schema_version":`+spelling), 1)
		if _, err := DecodeReceipt(invalid); err == nil {
			t.Fatalf("DecodeReceipt accepted invalid schema_version %s", spelling)
		}
	}
}
