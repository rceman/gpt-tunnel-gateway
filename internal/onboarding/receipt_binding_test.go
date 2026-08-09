package onboarding

import (
	"strings"
	"testing"
)

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
