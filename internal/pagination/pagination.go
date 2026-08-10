package pagination

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

type PageInfo struct {
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type cursor struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

func Limit(requested, configured int) (int, error) {
	hardMax := MaxLimit
	if configured > 0 && configured < hardMax {
		hardMax = configured
	}
	if requested == 0 {
		requested = DefaultLimit
		if requested > hardMax {
			requested = hardMax
		}
	}
	if requested < 1 || requested > hardMax {
		return 0, fmt.Errorf("list limit must be between 1 and %d", hardMax)
	}
	return requested, nil
}

func Encode(kind, key string) string {
	data, _ := json.Marshal(cursor{
		Kind: kind,
		Key:  key,
	})
	return base64.RawURLEncoding.EncodeToString(data)
}

func Decode(value, kind string) (string, error) {
	if value == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("invalid continuation cursor")
	}
	var c cursor
	if err := json.Unmarshal(data, &c); err != nil || c.Kind != kind || c.Key == "" {
		return "", fmt.Errorf("invalid continuation cursor")
	}
	return c.Key, nil
}

func Page[T any](kind string, items []T, limit int, rawCursor string, key func(T) string) ([]T, PageInfo, error) {
	after, err := Decode(rawCursor, kind)
	if err != nil {
		return nil, PageInfo{}, err
	}
	start := 0
	if after != "" {
		for i, item := range items {
			if key(item) == after {
				start = i + 1
				break
			}
		}
		if start == 0 {
			return nil, PageInfo{}, fmt.Errorf("continuation cursor is no longer valid")
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	result := items[start:end]
	info := PageInfo{}
	if end < len(items) && len(result) > 0 {
		info.HasMore = true
		info.NextCursor = Encode(kind, key(result[len(result)-1]))
	}
	return result, info, nil
}
