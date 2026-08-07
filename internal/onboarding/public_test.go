package onboarding

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicStatusProjectionRedactsLocalCapabilities(t *testing.T) {
	request := Request{GatewayStateDir: "/home/secret/state"}
	key := "project_master"
	receipt := Receipt{
		OperationID: "11111111-1111-1111-1111-111111111111", ProjectID: "example", RequestSHA256: strings.Repeat("a", 64),
		State: StatePrepared, RepositoryProof: RepositoryProof{Root: "/home/secret/project", GatewayStateDir: request.GatewayStateDir},
		SessionProof: SessionProof{Required: true, SessionKey: &key, Status: "active", ControllerProtocolVersion: receiptTestPositive(1)},
		Timestamps:   Timestamps{StartedAt: "2026-08-07T00:00:00Z", UpdatedAt: "2026-08-07T00:01:00Z"},
	}
	_ = receipt
	_ = request
	projection := StatusProjection{OperationID: "op", ProjectID: "example", State: StatePrepared, RecoveryStatus: "not_required"}
	data := string(mustJSONForPublicTest(projection))
	for _, forbidden := range []string{"/home/secret", "project_master", "gateway_state_dir", "mirror_path", "session_key"} {
		if strings.Contains(data, forbidden) {
			t.Fatalf("projection leaked %q: %s", forbidden, data)
		}
	}
}

func mustJSONForPublicTest(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
