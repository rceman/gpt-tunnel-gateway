package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

const codeOutputTokenCeiling = 3000

var codeOutputCounter = tokenizer.NewCounter()

func enforceCodeOutputTokenBudget(action string, result map[string]any) error {
	if !strings.HasPrefix(action, "code/") {
		return nil
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("serialize code output: %w", err)
	}
	tokens, err := codeOutputCounter.CountText(payload)
	if err != nil {
		return fmt.Errorf("count code output tokens: %w", err)
	}
	if tokens > codeOutputTokenCeiling {
		return fmt.Errorf("code output exceeds %d tokenizer tokens: %d", codeOutputTokenCeiling, tokens)
	}
	return nil
}
