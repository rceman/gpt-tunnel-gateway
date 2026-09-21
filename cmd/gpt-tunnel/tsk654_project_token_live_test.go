package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK654ProjectTokenLiveDaemonCwdResolutionAndDurability(t *testing.T) {
	var adminSession string
	var isolatedRoot string
	gateway := testutil.NewLiveGateway(t, testutil.LiveGatewayHooks{
		BeforeStart: func(gateway *testutil.LiveGateway) {
			isolatedRemote, root, _ := testutil.RepoWithBareRemote(t)
			testutil.Git(t, root, "remote", "set-head", "origin", "main")
			isolatedRoot = root
			gateway.Config.Projects = map[string]config.ProjectConfig{
				"air": {
					Root: gateway.ProjectRoot, Mirror: gateway.StateDir + "/air.git", Remote: "origin", DefaultBranch: "main", ProjectCode: "AIR", AirelaySessionKey: "air_worker",
				},
				"iso": {
					Root: root, Mirror: gateway.StateDir + "/iso.git", Remote: "origin", DefaultBranch: "main", ProjectCode: "ISO", AirelaySessionKey: "iso_worker",
				},
			}
			gateway.WriteConfig(t)
			if err := (hub.Store{Config: gateway.Config}).Ensure(context.Background()); err != nil {
				t.Fatal(err)
			}
			db, err := sqlitestore.Open(gateway.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			record, err := durableSession.NewStoreWithGateway(db, gateway.Config.GatewayID).CreateAdmin(nil)
			_ = db.Close()
			if err != nil {
				t.Fatal(err)
			}
			adminSession = record.ID
			_ = isolatedRemote
		},
	})
	options := testutil.LiveCommandOptions{Dir: gateway.ProjectRoot, Env: map[string]string{"GPT_TUNNEL_ADMIN_SESSION": adminSession}}
	first := gateway.MustCLI(t, options, "project", "token")
	firstToken := strings.TrimSpace(first.Stdout)
	if firstToken == "" {
		t.Fatal("project token returned an empty token")
	}
	second := gateway.MustCLI(t, options, "project", "token")
	if strings.TrimSpace(second.Stdout) != firstToken {
		t.Fatal("repeated project token retrieval changed the static grant")
	}
	isolated := gateway.MustCLI(t, testutil.LiveCommandOptions{Dir: isolatedRoot, Env: map[string]string{"GPT_TUNNEL_ADMIN_SESSION": adminSession}}, "project", "token")
	isolatedToken := strings.TrimSpace(isolated.Stdout)
	if isolatedToken == "" || isolatedToken == firstToken {
		t.Fatal("cross-project token retrieval was not isolated")
	}
	gateway.Restart(t)
	afterRestart := gateway.MustCLI(t, options, "project", "token")
	if strings.TrimSpace(afterRestart.Stdout) != firstToken {
		t.Fatal("project token changed across daemon restart")
	}
	unregisteredRemote, unregisteredRoot, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, unregisteredRoot, "remote", "set-head", "origin", "main")
	unregistered := gateway.RunCLI(testutil.LiveCommandOptions{Dir: unregisteredRoot, Env: map[string]string{"GPT_TUNNEL_ADMIN_SESSION": adminSession}}, "project", "token")
	if unregistered.Err == nil || !strings.Contains(unregistered.Stderr, "not a registered project") {
		t.Fatalf("unregistered repository was not rejected: err=%v stderr=%q", unregistered.Err, unregistered.Stderr)
	}
	_ = unregisteredRemote
	nonRepo := gateway.RunCLI(testutil.LiveCommandOptions{Dir: t.TempDir(), Env: map[string]string{"GPT_TUNNEL_ADMIN_SESSION": adminSession}}, "project", "token")
	if nonRepo.Err == nil || !strings.Contains(nonRepo.Stderr, "not a Git repository") {
		t.Fatalf("non-repository cwd was not rejected: err=%v stderr=%q", nonRepo.Err, nonRepo.Stderr)
	}
	legacy := gateway.RunCLI(testutil.LiveCommandOptions{Dir: gateway.ProjectRoot, Env: map[string]string{"GPT_TUNNEL_ADMIN_SESSION": adminSession}}, "session", "token", "AIR")
	if legacy.Err == nil || !strings.Contains(legacy.Stderr, "usage: gpt-tunnel") {
		t.Fatalf("retired session token surface was not rejected: err=%v stderr=%q", legacy.Err, legacy.Stderr)
	}
	projects, err := gateway.MCPCall(context.Background(), "projects", map[string]any{"gateway": "HOM"})
	if err != nil {
		t.Fatal(err)
	}
	guide, err := gateway.MCPCall(context.Background(), "guide", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]any{projects, guide})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), firstToken) || strings.Contains(string(encoded), isolatedToken) {
		t.Fatal("project token leaked through public MCP projections")
	}
	gateway.Stop()
	db, err := sqlitestore.Open(gateway.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, projectID := range []string{"air", "iso"} {
		rows, err := db.Local.Query(context.Background(), "SELECT project_id FROM local_session_bootstrap_grants WHERE project_id=?", projectID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows.Rows) != 1 {
			t.Fatalf("project %s has %d bootstrap grants, want exactly one", projectID, len(rows.Rows))
		}
	}
}
