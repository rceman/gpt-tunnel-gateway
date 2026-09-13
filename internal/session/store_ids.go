package session

import (
	"crypto/rand"
	"fmt"
)

const sessionIDAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

func (s Store) nextID(role, projectCode string) (string, error) {
	if s.IDGenerator != nil {
		return s.IDGenerator()
	}
	if s.TypedIDGenerator != nil {
		return s.TypedIDGenerator(role)
	}
	if err := validateGatewayKey(s.GatewayID); err != nil {
		return "", err
	}
	if err := validateProjectCode(projectCode); err != nil {
		return "", err
	}
	roleCode, ok := WorkflowRoleCode(role)
	if !ok {
		return "", fmt.Errorf("%w: unsupported session role", ErrInvalidSession)
	}
	var suffix [5]byte
	for i := range suffix {
		for {
			var raw [1]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return "", fmt.Errorf("generate session ID: %w", err)
			}
			if raw[0] >= 252 {
				continue
			}
			suffix[i] = sessionIDAlphabet[int(raw[0])%len(sessionIDAlphabet)]
			break
		}
	}
	return fmt.Sprintf("%s_%s_%s_%s", s.GatewayID, projectCode, roleCode, string(suffix[:])), nil
}
