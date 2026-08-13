package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
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
	if lines < 1 || lines > runtime_log.MaxLimit {
		return "", fmt.Errorf("invalid line count")
	}
	filter := runtime_log.Filter{Limit: lines}
	switch name {
	case "gateway":
		filter.Component = "gateway"
	case "tunnel":
		filter.Component = "tunnel"
	case "all", "":
	default:
		return "", fmt.Errorf("unknown log name")
	}
	result, err := runtime_log.New(c.Config.StateDir).Read(filter)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, event := range result.Events {
		encoded, err := json.Marshal(event)
		if err != nil {
			return "", err
		}
		b.Write(encoded)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
