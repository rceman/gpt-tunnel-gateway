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
		Recovery: Recovery{
			Status: "not_required",
		},
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

func receiptProtocolJSON(t *testing.T, request Request, spelling string) []byte {
	t.Helper()
	data := receiptJSON(t, preparedReceiptForTest(t, request))
	old := []byte(`"controller_protocol_version":1`)
	if !bytes.Contains(data, old) {
		t.Fatalf("prepared receipt did not contain the protocol field")
	}
	return bytes.Replace(data, old, []byte(`"controller_protocol_version":`+spelling), 1)
}
