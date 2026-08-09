package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func (c Controller) Doctor(ctx context.Context) error {
	s, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if !s.Gateway.Running || !s.GatewayReady {
		return fmt.Errorf("gateway is not healthy")
	}
	if !s.Tunnel.Running || !s.TunnelReady {
		return fmt.Errorf("tunnel is not healthy")
	}
	if !s.VersionMatch {
		return fmt.Errorf("installed and running gateway versions differ")
	}
	return nil
}
func (c Controller) Logs(name string, lines int) (string, error) {
	if lines < 1 || lines > 10000 {
		return "", fmt.Errorf("invalid line count")
	}
	paths := []string{}
	switch name {
	case "gateway":
		paths = []string{c.logPath("gateway")}
	case "tunnel":
		paths = []string{c.logPath("tunnel")}
	case "all", "":
		paths = []string{c.logPath("gateway"), c.logPath("tunnel")}
	default:
		return "", fmt.Errorf("unknown log name")
	}
	var b strings.Builder
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parts := strings.Split(string(data), "\n")
		start := 0
		if len(parts) > lines {
			start = len(parts) - lines
		}
		fmt.Fprintf(&b, "==> %s <==\n%s\n", p, strings.Join(parts[start:], "\n"))
	}
	return b.String(), nil
}
