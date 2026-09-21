package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK655LiveGatewayOwnerLockAndCanonicalOperatorPath(t *testing.T) {
	gateway := testutil.NewLiveGateway(t, testutil.LiveGatewayHooks{
		BeforeStart: func(gateway *testutil.LiveGateway) {
			if err := (hub.Store{Config: gateway.Config}).Ensure(context.Background()); err != nil {
				t.Fatalf("initialize disposable Hub: %v", err)
			}
		},
	})
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatal("live gateway did not retain daemon SQLite ownership")
	}

	direct := gateway.RunCLI(testutil.LiveCommandOptions{}, "project", "onboard", "AIR", "agentir_worker")
	if direct.Err == nil || !strings.Contains(direct.Stderr, "already has an owner") {
		t.Fatalf("direct durability CLI did not hit the owner boundary: stderr=%q err=%v", direct.Stderr, direct.Err)
	}
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatal("owner lock was not active after direct CLI rejection")
	}

	canonical := gateway.MustCLI(t, testutil.LiveCommandOptions{}, "project", "list")
	var projects map[string]any
	if err := json.Unmarshal([]byte(canonical.Stdout), &projects); err != nil {
		t.Fatalf("canonical operator output is not JSON: %v\n%s", err, canonical.Stdout)
	}
	if _, ok := projects["projects"]; !ok {
		t.Fatalf("canonical operator output=%#v", projects)
	}
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatal("canonical operator path stopped or displaced daemon ownership")
	}

	evidence, err := json.Marshal(map[string]any{
		"persistence_owner":            "gpt-tunnel-gatewayd",
		"persistence_owner_executable": gateway.GatewayBinary,
		"cli_invoker":                  "gpt-tunnel",
		"cli_executable":               gateway.OperatorBinary,
		"owner_lock_active":            gateway.OwnerLockActive(),
		"daemon_running":               gateway.DaemonRunning(),
		"old_direct_path":              "project onboard AIR agentir_worker",
		"old_direct_path_error":        "already has an owner",
		"canonical_operator_path":      "project list",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Gate16 evidence: %s", evidence)
	gateway.Stop()
	if gateway.DaemonRunning() || gateway.OwnerLockActive() {
		t.Fatal("live gateway teardown left the owner lock active")
	}
}
