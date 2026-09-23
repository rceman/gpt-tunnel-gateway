package actioncontract

import (
	"fmt"
	"io/fs"

	contractfiles "github.com/rceman/gpt-tunnel-gateway/contracts"
)

func LoadCanonical() (*CompiledSet, error) {
	shared, err := fs.ReadFile(contractfiles.Files, "shared-definitions.yaml")
	if err != nil {
		return nil, fmt.Errorf("read canonical shared definitions: %w", err)
	}
	actions, err := fs.ReadFile(contractfiles.Files, "actions.yaml")
	if err != nil {
		return nil, fmt.Errorf("read canonical action catalog: %w", err)
	}
	return Compile(shared, actions)
}
