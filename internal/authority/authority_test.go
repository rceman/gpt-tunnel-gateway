package authority

import (
	"context"
	"testing"
)

func TestPlannerOrDeliveryBootstrapDoesNotBecomeRoleAuthority(t *testing.T) {
	ctx := WithPlannerOrDelivery(context.Background())
	if err := RequirePlannerOrDelivery(ctx); err != nil {
		t.Fatalf("bootstrap authority rejected: %v", err)
	}
	if err := RequirePlanner(ctx); err == nil {
		t.Fatal("combined bootstrap marker acquired planner-only authority")
	}
	if err := RequireDelivery(ctx); err == nil {
		t.Fatal("combined bootstrap marker acquired delivery-only authority")
	}
	if err := RequireRole(ctx, "planner"); err != nil {
		t.Fatalf("planner session bootstrap was rejected: %v", err)
	}
	if err := RequireRole(ctx, "delivery"); err != nil {
		t.Fatalf("delivery session bootstrap was rejected: %v", err)
	}
}
