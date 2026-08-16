package mcp

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/onboarding"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestMinimalProjectOnboardIsSessionlessAndReturnsSessionStartCode(t *testing.T) {
	server := newSessionTestServer(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "-a")
	value, err := server.tools()["project_onboard"].Execute(authority.WithDelivery(context.Background()), mustJSON(t, map[string]any{
		"project_id": "fresh-project", "root": root,
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(onboarding.MinimalResult)
	if !ok || result.ProjectID != "fresh-project" || result.ProjectCode == "" || result.NextStep == "" {
		t.Fatalf("minimal onboarding result=%#v", value)
	}
	code := result.ProjectCode
	started, err := server.Service.SessionStartByCode(authority.WithDelivery(context.Background()), code, "delivery", nil, nil)
	if err != nil {
		t.Fatalf("session_start after onboarding: %v", err)
	}
	if started.Session.ProjectID != "fresh-project" || started.Session.ProjectCode != code {
		t.Fatalf("session binding=%#v", started)
	}
}
