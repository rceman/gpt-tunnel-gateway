package authority

import (
	"context"
	"fmt"
)

type role string

const (
	planner           role = "planner"
	delivery          role = "delivery"
	plannerOrDelivery role = "planner_or_delivery"
	operator          role = "operator"
)

type contextKey struct{}

func WithPlanner(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, planner)
}

func WithDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, delivery)
}

// WithPlannerOrDelivery is the daemon's narrowly scoped bootstrap authority.
// It can authorize creation of either durable project session role, but it is
// intentionally not accepted by role-specific RequirePlanner/RequireDelivery
// checks.
func WithPlannerOrDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, plannerOrDelivery)
}

// BootstrapSessionAuthority upgrades an already trusted planner/delivery
// server context only for session.start. It does not grant the combined
// marker to an untrusted request context.
func BootstrapSessionAuthority(ctx context.Context) (context.Context, error) {
	if err := RequirePlannerOrDelivery(ctx); err != nil {
		return nil, err
	}
	return WithPlannerOrDelivery(ctx), nil
}

func WithOperator(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, operator)
}

// Attach copies only the server-owned authority marker from trusted into the
// request context. Serialized request data, headers and protocol metadata are
// never consulted.
func Attach(request, trusted context.Context) context.Context {
	if request == nil {
		request = context.Background()
	}
	if trusted == nil {
		return request
	}
	v, ok := trusted.Value(contextKey{}).(role)
	if !ok {
		return request
	}
	return context.WithValue(request, contextKey{}, v)
}

func RequirePlannerOrDelivery(ctx context.Context) error {
	v, ok := ctx.Value(contextKey{}).(role)
	if !ok || (v != planner && v != delivery && v != plannerOrDelivery) {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequirePlanner(ctx context.Context) error {
	if v, ok := ctx.Value(contextKey{}).(role); !ok || v != planner {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireDelivery(ctx context.Context) error {
	if v, ok := ctx.Value(contextKey{}).(role); !ok || v != delivery {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireRole(ctx context.Context, wanted string) error {
	if v, ok := ctx.Value(contextKey{}).(role); ok && v == plannerOrDelivery && (wanted == "planner" || wanted == "delivery") {
		return nil
	}
	switch wanted {
	case "planner":
		return RequirePlanner(ctx)
	case "delivery":
		return RequireDelivery(ctx)
	default:
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
}
