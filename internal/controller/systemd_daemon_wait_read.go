package controller

import (
	"context"
	"fmt"
	"time"
)

func (c Controller) waitDaemonReadiness(ctx context.Context) error {
	if err := waitURLContext(ctx, c.gatewayReadyURL(), true); err != nil {
		return fmt.Errorf("gateway readiness failed: %w", err)
	}
	if err := waitURLContext(ctx, c.tunnelReadyURL(), true); err != nil {
		return fmt.Errorf("tunnel readiness failed: %w", err)
	}
	return nil
}

func waitURLContext(ctx context.Context, url string, want bool) error {
	for {
		if checkURL(ctx, url) == want {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("readiness timeout for %s", url)
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("readiness timeout for %s", url)
		case <-timer.C:
		}
	}
}
