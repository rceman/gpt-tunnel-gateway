package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func CanonicalRequestJSON(request Request) ([]byte, error) {
	if err := ValidateRequest(request); err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical request: %w", err)
	}
	return data, nil
}

func RequestDigest(request Request) (string, error) {
	data, err := CanonicalRequestJSON(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (request Request) CanonicalJSON() ([]byte, error) {
	return CanonicalRequestJSON(request)
}

func (request Request) Digest() (string, error) {
	return RequestDigest(request)
}
