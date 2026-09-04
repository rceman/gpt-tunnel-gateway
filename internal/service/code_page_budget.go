package service

import (
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

const CodePageTokenBudget = 3000

const codePageTransportReserve = 128

var codePageTokenCounter = tokenizer.NewCounter()

func largestCodePageSize(max int, fits func(int) (bool, error)) (int, error) {
	best := 0
	for low, high := 1, max; low <= high; {
		middle := low + (high-low)/2
		ok, err := fits(middle)
		if err != nil {
			return 0, err
		}
		if ok {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	return best, nil
}

func codePageFits(value any) (bool, error) {
	public := map[string]any{
		"ok":      true,
		"result":  value,
		"metrics": map[string]any{"time": 0, "tokens": 0},
	}
	if result, ok := value.(map[string]any); ok {
		if pagination, ok := result["_pagination"].(map[string]any); ok {
			if cursor, ok := pagination["next_cursor"].(string); ok && cursor != "" {
				public["pagination"] = map[string]any{"next_cursor": cursor}
			}
		}
	}
	compact, err := json.Marshal(public)
	if err != nil {
		return false, fmt.Errorf("serialize code page: %w", err)
	}
	pretty, err := json.MarshalIndent(public, "", "  ")
	if err != nil {
		return false, fmt.Errorf("pretty-serialize code page: %w", err)
	}
	// The public transport carries both structuredContent and its text
	// projection. Pack against the same public call envelope produced by
	// genericCallPublic so all code actions share one budget calculation.
	probe := map[string]any{
		"result": map[string]any{
			"content":           []map[string]any{{"type": "text", "text": string(pretty)}},
			"isError":           false,
			"structuredContent": json.RawMessage(compact),
		},
	}
	payload, err := json.Marshal(probe)
	if err != nil {
		return false, fmt.Errorf("serialize code page envelope: %w", err)
	}
	tokens, err := codePageTokenCounter.CountText(payload)
	if err != nil {
		return false, fmt.Errorf("count code page tokens: %w", err)
	}
	return tokens <= CodePageTokenBudget-codePageTransportReserve, nil
}
