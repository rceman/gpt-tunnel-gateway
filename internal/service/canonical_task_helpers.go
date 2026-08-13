package service

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

func requireCanonicalTaskID(id string) error {
	return model.ValidateCanonicalTaskID(id)
}
