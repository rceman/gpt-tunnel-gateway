package authority

import (
	"context"
	"fmt"
)

type role string

const (
	planner  role = "planner"
	delivery role = "delivery"
	operator role = "operator"
)

type contextKey struct{}

func WithPlanner(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, planner)
}

func WithDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, delivery)
}

func WithOperator(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, operator)
}

func RequirePlannerOrDelivery(ctx context.Context) error {
	v, ok := ctx.Value(contextKey{}).(role)
	if !ok || (v != planner && v != delivery) {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}

func RequireOnboarding(ctx context.Context) error {
	v, ok := ctx.Value(contextKey{}).(role)
	if !ok || (v != planner && v != delivery && v != operator) {
		return fmt.Errorf("AUTHORITY_UNAVAILABLE")
	}
	return nil
}
