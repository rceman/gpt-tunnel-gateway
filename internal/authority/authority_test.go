package authority

import (
	"context"
	"testing"
)

func TestPlannerOrAgentBootstrapDoesNotBecomeRoleAuthority(t *testing.T) {
	ctx := WithPlannerOrAgent(context.Background())
	if err := RequirePlannerOrAgent(ctx); err != nil {
		t.Fatalf("bootstrap authority rejected: %v", err)
	}
	if err := RequirePlanner(ctx); err == nil {
		t.Fatal("combined bootstrap marker acquired planner-only authority")
	}
	if err := RequireRole(ctx, "planner"); err != nil {
		t.Fatalf("planner session bootstrap was rejected: %v", err)
	}
	if err := RequireRole(ctx, "agent"); err != nil {
		t.Fatalf("agent session bootstrap was rejected: %v", err)
	}
}

func TestBootstrapSessionAuthorityIsNarrowAndRequiresTrustedRoot(t *testing.T) {
	for name, root := range map[string]context.Context{
		"planner": WithPlanner(context.Background()),
		"agent":   WithAgent(context.Background()),
	} {
		bootstrapped, err := BootstrapSessionAuthority(root)
		if err != nil {
			t.Fatalf("%s bootstrap rejected: %v", name, err)
		}
		if err := RequirePlannerOrAgent(bootstrapped); err != nil {
			t.Fatalf("%s bootstrap lost combined session capability: %v", name, err)
		}
		if err := RequirePlanner(bootstrapped); err == nil {
			t.Fatalf("%s bootstrap became planner-only authority", name)
		}
	}
	if _, err := BootstrapSessionAuthority(context.Background()); err == nil {
		t.Fatal("untrusted session context was elevated")
	}
}
