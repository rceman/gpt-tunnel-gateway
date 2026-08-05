package onboarding

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func receiptTestRequest(t *testing.T) Request {
	t.Helper()
	data, err := os.ReadFile("testdata/airelay-request.json")
	if err != nil {
		t.Fatalf("read request fixture: %v", err)
	}
	request, err := DecodeRequest(data)
	if err != nil {
		t.Fatalf("decode request fixture: %v", err)
	}
	return request
}

func receiptTestString(value string) *string {
	return &value
}

func receiptTestPositive(value uint64) *PositiveInteger {
	positive := PositiveInteger(value)
	return &positive
}

func preparedReceiptForTest(t *testing.T, request Request) Receipt {
	t.Helper()
	requestDigest, err := RequestDigest(request)
	if err != nil {
		t.Fatalf("request digest: %v", err)
	}
	paths := []string{
		"gpt-tunnel/v1/projects/" + request.ProjectID + "/project.json",
		"gpt-tunnel/v1/projects/" + request.ProjectID + "/plan/current.json",
		"gpt-tunnel/v1/projects/" + request.ProjectID + "/identifiers.json",
	}
	receipt := Receipt{
		SchemaVersion: PositiveInteger(1),
		OperationID:   "11111111-1111-1111-1111-111111111111",
		RequestSHA256: requestDigest,
		State:         StatePrepared,
		ProjectID:     request.ProjectID,
		RepositoryProof: RepositoryProof{
			Root:            request.Root,
			Remote:          request.Remote,
			RepositoryURL:   request.RepositoryURL,
			DefaultBranch:   request.DefaultBranch,
			Branch:          request.DefaultBranch,
			Head:            strings.Repeat("a", 40),
			GatewayStateDir: request.GatewayStateDir,
		},
		WorktreeProof: WorktreeProof{
			Clean:        true,
			StatusSHA256: strings.Repeat("b", 64),
		},
		RegistryDigests: RegistryDigests{
			ManagedBeforeSHA256: strings.Repeat("c", 64),
			ManagedAfterSHA256:  strings.Repeat("d", 64),
			ProjectSHA256:       strings.Repeat("e", 64),
			PlanSHA256:          strings.Repeat("f", 64),
			IdentifiersSHA256:   strings.Repeat("0", 64),
		},
		Hub: HubProof{
			Before: request.ExpectedHubRevision,
			Paths:  paths,
		},
		Timestamps: Timestamps{
			StartedAt:  "2026-08-05T09:00:00Z",
			PreparedAt: receiptTestString("2026-08-05T09:01:00Z"),
			UpdatedAt:  "2026-08-05T09:01:00Z",
		},
		Recovery: Recovery{Status: "not_required"},
	}
	if request.Airelay.SessionRequired {
		receipt.SessionProof = SessionProof{
			Required:                  true,
			SessionKey:                request.Airelay.SessionKey,
			Status:                    "active",
			ControllerProtocolVersion: receiptTestPositive(1),
		}
	} else {
		receipt.SessionProof = SessionProof{Status: "not_required"}
	}
	return receipt
}

func receiptJSON(t *testing.T, receipt Receipt) []byte {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	return data
}

func receiptObject(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("unmarshal receipt object: %v", err)
	}
	return object
}

func receiptObjectJSON(t *testing.T, object map[string]json.RawMessage) []byte {
	t.Helper()
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal receipt object: %v", err)
	}
	return data
}

func TestValidatePreparedReceiptAndCanonicalDigest(t *testing.T) {
	request := receiptTestRequest(t)
	receipt := preparedReceiptForTest(t, request)
	canonical, err := CanonicalPreparedReceiptJSON(receipt, request)
	if err != nil {
		t.Fatalf("canonical prepared receipt: %v", err)
	}
	if len(canonical) == 0 || canonical[len(canonical)-1] == '\n' {
		t.Fatalf("canonical receipt must be compact and have no trailing newline: %q", canonical)
	}
	decoded, err := DecodeReceipt(canonical)
	if err != nil {
		t.Fatalf("decode canonical receipt: %v", err)
	}
	if decoded.State != StatePrepared || decoded.ProjectID != request.ProjectID {
		t.Fatalf("decoded receipt lost identity: %#v", decoded)
	}
	digestOne, err := PreparedReceiptDigest(receipt, request)
	if err != nil {
		t.Fatalf("receipt digest: %v", err)
	}
	digestTwo, err := PreparedReceiptDigest(decoded, request)
	if err != nil {
		t.Fatalf("decoded receipt digest: %v", err)
	}
	if digestOne != digestTwo || len(digestOne) != 64 {
		t.Fatalf("receipt digest is not stable SHA-256: %q != %q", digestOne, digestTwo)
	}
	if !bytes.Equal(canonical, receiptJSON(t, decoded)) {
		t.Fatalf("canonical receipt did not round-trip byte-for-byte")
	}
}

func TestDecodeReceiptStrictShape(t *testing.T) {
	request := receiptTestRequest(t)
	base := receiptJSON(t, preparedReceiptForTest(t, request))

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "unknown top-level field",
			data: func() []byte {
				object := receiptObject(t, base)
				object["unexpected"] = json.RawMessage(`true`)
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "unknown nested field",
			data: func() []byte {
				object := receiptObject(t, base)
				var nested map[string]json.RawMessage
				if err := json.Unmarshal(object["worktree_proof"], &nested); err != nil {
					t.Fatalf("unmarshal worktree proof: %v", err)
				}
				nested["unexpected"] = json.RawMessage(`1`)
				object["worktree_proof"] = receiptObjectJSON(t, nested)
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "missing required field",
			data: func() []byte {
				object := receiptObject(t, base)
				delete(object, "recovery")
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "null field",
			data: func() []byte {
				object := receiptObject(t, base)
				object["project_id"] = json.RawMessage(`null`)
				return receiptObjectJSON(t, object)
			}(),
		},
		{
			name: "wrong field type",
			data: func() []byte {
				object := receiptObject(t, base)
				object["schema_version"] = json.RawMessage(`"1"`)
				return receiptObjectJSON(t, object)
			}(),
		},
		{name: "trailing JSON", data: append(append([]byte(nil), base...), []byte(`{}`)...)},
		{name: "invalid UTF-8", data: append(append([]byte(nil), base...), 0xff)},
		{
			name: "duplicate top-level field",
			data: func() []byte {
				trimmed := bytes.TrimSpace(base)
				return append(append([]byte(nil), trimmed[:len(trimmed)-1]...), []byte(`,"operation_id":"22222222-2222-2222-2222-222222222222"}`)...)
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeReceipt(test.data); err == nil {
				t.Fatalf("DecodeReceipt accepted invalid %s", test.name)
			}
		})
	}
}

func TestDecodeReceiptRequiresBooleanPresence(t *testing.T) {
	request := receiptTestRequest(t)
	base := receiptJSON(t, preparedReceiptForTest(t, request))
	for _, field := range []struct {
		object string
		key    string
	}{
		{object: "worktree_proof", key: "clean"},
		{object: "session_proof", key: "required"},
	} {
		t.Run(field.object+"/"+field.key, func(t *testing.T) {
			object := receiptObject(t, base)
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(object[field.object], &nested); err != nil {
				t.Fatalf("unmarshal nested object: %v", err)
			}
			delete(nested, field.key)
			object[field.object] = receiptObjectJSON(t, nested)
			if _, err := DecodeReceipt(receiptObjectJSON(t, object)); err == nil {
				t.Fatalf("DecodeReceipt accepted missing %s.%s", field.object, field.key)
			}
		})
	}
}

func TestValidatePreparedReceiptRequestBinding(t *testing.T) {
	request := receiptTestRequest(t)
	base := preparedReceiptForTest(t, request)
	tests := []struct {
		name   string
		mutate func(*Receipt, *Request)
	}{
		{name: "request digest", mutate: func(receipt *Receipt, _ *Request) { receipt.RequestSHA256 = strings.Repeat("1", 64) }},
		{name: "project", mutate: func(receipt *Receipt, _ *Request) { receipt.ProjectID = "other-project" }},
		{name: "root", mutate: func(receipt *Receipt, _ *Request) { receipt.RepositoryProof.Root += "/other" }},
		{name: "remote", mutate: func(receipt *Receipt, _ *Request) { receipt.RepositoryProof.Remote = "upstream" }},
		{name: "repository URL", mutate: func(receipt *Receipt, _ *Request) {
			receipt.RepositoryProof.RepositoryURL = "https://example.invalid/repo.git"
		}},
		{name: "default branch", mutate: func(receipt *Receipt, _ *Request) { receipt.RepositoryProof.DefaultBranch = "develop" }},
		{name: "branch", mutate: func(receipt *Receipt, _ *Request) { receipt.RepositoryProof.Branch = "develop" }},
		{name: "gateway state directory", mutate: func(receipt *Receipt, _ *Request) { receipt.RepositoryProof.GatewayStateDir += "/other" }},
		{name: "hub revision", mutate: func(receipt *Receipt, _ *Request) { receipt.Hub.Before = strings.Repeat("2", 40) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := base
			requestCopy := request
			test.mutate(&receipt, &requestCopy)
			if err := ValidatePreparedReceipt(receipt, requestCopy); err == nil {
				t.Fatalf("ValidatePreparedReceipt accepted mismatched %s", test.name)
			}
		})
	}
}

func TestValidatePreparedReceiptProofRules(t *testing.T) {
	request := receiptTestRequest(t)
	base := preparedReceiptForTest(t, request)
	tests := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "dirty worktree", mutate: func(receipt *Receipt) { receipt.WorktreeProof.Clean = false }},
		{name: "invalid status digest", mutate: func(receipt *Receipt) { receipt.WorktreeProof.StatusSHA256 = strings.Repeat("A", 64) }},
		{name: "equal managed digests", mutate: func(receipt *Receipt) {
			receipt.RegistryDigests.ManagedAfterSHA256 = receipt.RegistryDigests.ManagedBeforeSHA256
		}},
		{name: "invalid registry digest", mutate: func(receipt *Receipt) { receipt.RegistryDigests.PlanSHA256 = "not-a-digest" }},
		{name: "duplicate hub path", mutate: func(receipt *Receipt) { receipt.Hub.Paths[1] = receipt.Hub.Paths[0] }},
		{name: "foreign hub path", mutate: func(receipt *Receipt) { receipt.Hub.Paths[0] = "gpt-tunnel/v1/projects/other/project.json" }},
		{name: "extra hub path", mutate: func(receipt *Receipt) { receipt.Hub.Paths = append(receipt.Hub.Paths, "extra.json") }},
		{name: "hub after", mutate: func(receipt *Receipt) { receipt.Hub.After = receiptTestString(strings.Repeat("1", 40)) }},
		{name: "created project", mutate: func(receipt *Receipt) { receipt.CreatedProject = &CreatedProject{} }},
		{name: "mirror proof", mutate: func(receipt *Receipt) { receipt.MirrorProof = &MirrorProof{} }},
		{name: "later timestamp", mutate: func(receipt *Receipt) { receipt.Timestamps.ActivatedAt = receiptTestString("2026-08-05T09:02:00Z") }},
		{name: "recovery reason", mutate: func(receipt *Receipt) { receipt.Recovery.Reason = receiptTestString("unexpected") }},
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
}

func TestValidatePreparedReceiptSessionRules(t *testing.T) {
	request := receiptTestRequest(t)
	base := preparedReceiptForTest(t, request)
	if request.Airelay.SessionRequired {
		tests := []struct {
			name   string
			mutate func(*Receipt)
		}{
			{name: "wrong status", mutate: func(receipt *Receipt) { receipt.SessionProof.Status = "idle" }},
			{name: "missing key", mutate: func(receipt *Receipt) { receipt.SessionProof.SessionKey = nil }},
			{name: "wrong key", mutate: func(receipt *Receipt) { receipt.SessionProof.SessionKey = receiptTestString("wrong_session") }},
			{name: "missing protocol", mutate: func(receipt *Receipt) { receipt.SessionProof.ControllerProtocolVersion = nil }},
			{name: "zero protocol", mutate: func(receipt *Receipt) { receipt.SessionProof.ControllerProtocolVersion = receiptTestPositive(0) }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				receipt := base
				test.mutate(&receipt)
				if err := ValidatePreparedReceipt(receipt, request); err == nil {
					t.Fatalf("ValidatePreparedReceipt accepted invalid required session %s", test.name)
				}
			})
		}
	}

	optionalRequest := request
	optionalRequest.Airelay.SessionRequired = false
	optionalRequest.Airelay.SessionKey = nil
	optionalReceipt := preparedReceiptForTest(t, optionalRequest)
	if err := ValidatePreparedReceipt(optionalReceipt, optionalRequest); err != nil {
		t.Fatalf("optional session receipt rejected: %v", err)
	}
	optionalReceipt.SessionProof.SessionKey = receiptTestString("unexpected")
	if err := ValidatePreparedReceipt(optionalReceipt, optionalRequest); err == nil {
		t.Fatalf("optional session accepted a session key")
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
