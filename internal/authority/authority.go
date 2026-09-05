package authority

import (
	"context"
	"fmt"
)

type role string

const (
	planner        role = "planner"
	plannerOrAgent role = "planner_or_agent"
	operator       role = "operator"
)

type contextKey struct{}

func WithPlanner(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, planner)
}

// WithAgent marks the server-authorized Agent role used by a durable session.
// Agent sessions may be created only through the trusted bootstrap boundary.
func WithAgent(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, role("agent"))
}

// WithPlannerOrAgent is the daemon's narrowly scoped bootstrap authority.
// It can authorize creation of either durable project session role, but it is
// intentionally not accepted by role-specific checks.
func WithPlannerOrAgent(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, plannerOrAgent)
}

// BootstrapSessionAuthority upgrades an already trusted planner/agent
// server context only for session.start. It does not grant the combined
// marker to an untrusted request context.
func BootstrapSessionAuthority(ctx context.Context) (context.Context, error) {
	if err := RequirePlannerOrAgent(ctx); err != nil {
		return nil, err
	}
	return WithPlannerOrAgent(ctx), nil
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

func RequirePlannerOrAgent(ctx context.Context) error {
	v, ok := ctx.Value(contextKey{}).(role)
	if !ok || (v != planner && v != role("agent") && v != plannerOrAgent) {
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

func RequireAgent(ctx context.Context) error {
	if v, ok := ctx.Value(contextKey{}).(role); !ok || v != role("agent") {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireRole(ctx context.Context, wanted string) error {
	if v, ok := ctx.Value(contextKey{}).(role); ok && v == plannerOrAgent && (wanted == "planner" || wanted == "agent") {
		return nil
	}
	switch wanted {
	case "planner":
		return RequirePlanner(ctx)
	case "agent":
		return RequireAgent(ctx)
	default:
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
}
