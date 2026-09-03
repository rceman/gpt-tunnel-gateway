package mcp

import (
	"fmt"
	"sort"
	"strings"
)

func (s *Server) applicableGatewayKeys() []string {
	if s.gatewayInventoryFn != nil {
		return normalizedGatewayKeys(s.gatewayInventoryFn())
	}
	if s.Service == nil || s.Service.Config.GatewayID == "" {
		return nil
	}
	return []string{s.Service.Config.GatewayID}
}

func normalizedGatewayKeys(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (s *Server) resolvePublicGateway(requested string) (string, error) {
	keys := s.applicableGatewayKeys()
	if requested != "" {
		for _, key := range keys {
			if key == requested {
				return requested, nil
			}
		}
		return "", fmt.Errorf("unknown gateway %q", requested)
	}
	switch len(keys) {
	case 0:
		return "", fmt.Errorf("gateway selection unavailable: no applicable gateway is registered")
	case 1:
		return keys[0], nil
	default:
		return "", fmt.Errorf("gateway selection required; available gateways: %s", strings.Join(keys, ", "))
	}
}

func (s *Server) validatePublicGateway(requested string) error {
	if requested == "" {
		return fmt.Errorf("unknown gateway %q", requested)
	}
	_, err := s.resolvePublicGateway(requested)
	return err
}
