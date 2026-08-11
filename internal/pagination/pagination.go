package pagination

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const CompactCursorLength = 8

const compactAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

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
	return compactEncode(kind, key)
}

func compactEncode(kind, key string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + key))
	value := uint64(0)
	for _, b := range digest[:8] {
		value = value<<8 | uint64(b)
	}
	encoded := make([]byte, CompactCursorLength)
	for i := len(encoded) - 1; i >= 0; i-- {
		encoded[i] = compactAlphabet[value%uint64(len(compactAlphabet))]
		value /= uint64(len(compactAlphabet))
	}
	return string(encoded)
}

func Resolve(value, kind string, keys []string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) == CompactCursorLength && isCompact(value) {
		match := ""
		for _, key := range keys {
			if compactEncode(kind, key) != value {
				continue
			}
			if match != "" {
				return "", fmt.Errorf("invalid continuation cursor")
			}
			match = key
		}
		if match == "" {
			return "", fmt.Errorf("continuation cursor is no longer valid")
		}
		return match, nil
	}
	return Decode(value, kind)
}

func isCompact(value string) bool {
	if len(value) != CompactCursorLength {
		return false
	}
	for _, char := range value {
		if !containsByte(compactAlphabet, byte(char)) {
			return false
		}
	}
	return true
}

func containsByte(value string, wanted byte) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == wanted {
			return true
		}
	}
	return false
}

func decodeLegacy(value, kind string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("invalid continuation cursor")
	}
	var c cursor
	if err := json.Unmarshal(decoded, &c); err != nil || c.Kind != kind || c.Key == "" {
		return "", fmt.Errorf("invalid continuation cursor")
	}
	return c.Key, nil
}

func Decode(value, kind string) (string, error) {
	if value == "" {
		return "", nil
	}
	if isCompact(value) {
		return "", fmt.Errorf("compact cursor requires a scoped page")
	}
	return decodeLegacy(value, kind)
}

func Page[T any](kind string, items []T, limit int, rawCursor string, key func(T) string) ([]T, PageInfo, error) {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, key(item))
	}
	after, err := Resolve(rawCursor, kind, keys)
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
