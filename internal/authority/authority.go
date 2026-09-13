package authority

import (
	"context"
	"fmt"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type role string

const (
	planner                 role = durableSession.RolePlanner
	plannerOrManagedRuntime role = "planner_or_managed_runtime"
	operator                role = "operator"
	managedRuntime          role = "agent"
)

type contextKey struct{}

func WithPlanner(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, planner)
}

// WithAgent marks the server-authorized generic managed runtime.
func WithAgent(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, managedRuntime)
}

func WithLead(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, role(durableSession.RoleLead))
}

func WithAdvisor(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, role(durableSession.RoleAdvisor))
}

func WithWorker(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, role(durableSession.RoleWorker))
}

// WithPlannerOrManagedRuntime is the daemon's narrowly scoped bootstrap authority.
// It can authorize creation of any canonical durable project Session role, but it
// is intentionally not accepted by role-specific checks.
func WithPlannerOrManagedRuntime(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, plannerOrManagedRuntime)
}

// BootstrapSessionAuthority upgrades an already trusted Planner or managed
// runtime context only for session.start. It does not grant the combined marker
// to an untrusted request context.
func BootstrapSessionAuthority(ctx context.Context) (context.Context, error) {
	if err := RequirePlannerOrManagedRuntime(ctx); err != nil {
		return nil, err
	}
	return WithPlannerOrManagedRuntime(ctx), nil
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

func RequirePlannerOrManagedRuntime(ctx context.Context) error {
	v, ok := ctx.Value(contextKey{}).(role)
	if !ok || (v != planner && v != managedRuntime && v != plannerOrManagedRuntime) {
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
	if v, ok := ctx.Value(contextKey{}).(role); !ok || v != managedRuntime {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireLead(ctx context.Context) error {
	if v, ok := ctx.Value(contextKey{}).(role); !ok || v != role(durableSession.RoleLead) {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireAdvisor(ctx context.Context) error {
	if v, ok := ctx.Value(contextKey{}).(role); !ok || v != role(durableSession.RoleAdvisor) {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireWorker(ctx context.Context) error {
	if v, ok := ctx.Value(contextKey{}).(role); !ok || v != role(durableSession.RoleWorker) {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireRole(ctx context.Context, wanted string) error {
	if v, ok := ctx.Value(contextKey{}).(role); ok && v == plannerOrManagedRuntime && durableSession.IsWorkflowRole(wanted) {
		return nil
	}
	switch wanted {
	case durableSession.RolePlanner:
		return RequirePlanner(ctx)
	case durableSession.RoleLead:
		return RequireLead(ctx)
	case durableSession.RoleAdvisor:
		return RequireAdvisor(ctx)
	case durableSession.RoleWorker:
		return RequireWorker(ctx)
	default:
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
}
