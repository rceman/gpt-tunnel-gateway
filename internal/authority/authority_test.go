package authority

import (
	"context"
	"testing"
)

func TestPlannerOrManagedRuntimeBootstrapDoesNotBecomeRoleAuthority(t *testing.T) {
	ctx := WithPlannerOrManagedRuntime(context.Background())
	if err := RequirePlannerOrManagedRuntime(ctx); err != nil {
		t.Fatalf("bootstrap authority rejected: %v", err)
	}
	if err := RequirePlanner(ctx); err == nil {
		t.Fatal("combined bootstrap marker acquired planner-only authority")
	}
	if err := RequireRole(ctx, "planner"); err != nil {
		t.Fatalf("planner session bootstrap was rejected: %v", err)
	}
	if err := RequireRole(ctx, "agent"); err == nil {
		t.Fatal("Agent compatibility role acquired workflow-role authority")
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
		if err := RequirePlannerOrManagedRuntime(bootstrapped); err != nil {
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
