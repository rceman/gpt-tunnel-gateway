package session

import (
	"crypto/rand"
	"fmt"
)

func (s Store) nextID(role, projectCode string) (string, error) {
	if s.IDGenerator != nil {
		return s.IDGenerator()
	}
	if s.TypedIDGenerator != nil {
		return s.TypedIDGenerator(role)
	}
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var value uint64
	for _, b := range raw {
		value = value<<8 | uint64(b)
	}
	var encoded [8]byte
	for i := len(encoded) - 1; i >= 0; i-- {
		encoded[i] = alphabet[value&31]
		value >>= 5
	}
	prefix := map[string]string{RolePlanner: SessionIDPrefixPlanner, RoleAgent: SessionIDPrefixAgent}[role]
	if prefix == "" {
		return "", fmt.Errorf("%w: unsupported session role", ErrInvalidSession)
	}
	if projectCode != "" {
		if err := validateProjectCode(projectCode); err != nil {
			return "", err
		}
		return prefix + "-" + projectCode + "-" + string(encoded[4:]), nil
	}
	return prefix + "-" + string(encoded[:]), nil
}
