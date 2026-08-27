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
	compact, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("serialize code page: %w", err)
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, fmt.Errorf("pretty-serialize code page: %w", err)
	}
	// The public transport carries both structuredContent and its text
	// projection. Pack against that envelope so the actual MCP page, not only
	// the internal result object, remains within the public budget.
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
