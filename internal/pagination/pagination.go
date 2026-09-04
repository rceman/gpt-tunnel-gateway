package pagination

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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

// EncodeFull returns a bounded opaque cursor. The scope and key digests bind
// it to the complete server-owned query without putting that query in the
// caller-visible token.
func EncodeFull(kind, key string) string {
	scope := sha256.Sum256([]byte(kind))
	value := sha256.Sum256([]byte(kind + "\x00" + key))
	data := make([]byte, 0, 33)
	data = append(data, 1)
	data = append(data, scope[:16]...)
	data = append(data, value[:16]...)
	return base64.RawURLEncoding.EncodeToString(data)
}

// OpaqueCursorMatches resolves a streamed cursor by comparing the candidate
// key while the bounded source walk is in progress.
func OpaqueCursorMatches(raw, kind, key string) bool {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) != 33 || data[0] != 1 {
		return false
	}
	scope := sha256.Sum256([]byte(kind))
	value := sha256.Sum256([]byte(kind + "\x00" + key))
	return string(data[1:17]) == string(scope[:16]) && string(data[17:]) == string(value[:16])
}

func ValidateOpaqueCursor(raw, kind string) error {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) != 33 || data[0] != 1 {
		return fmt.Errorf("invalid continuation cursor")
	}
	scope := sha256.Sum256([]byte(kind))
	if string(data[1:17]) != string(scope[:16]) {
		return fmt.Errorf("continuation cursor scope does not match")
	}
	return nil
}

func EncodeOffset(kind string, offset int64) string {
	if offset < 0 {
		return ""
	}
	scope := sha256.Sum256([]byte(kind))
	var raw [41]byte
	raw[0] = 2
	copy(raw[1:17], scope[:16])
	binary.BigEndian.PutUint64(raw[17:25], uint64(offset))
	digest := sha256.Sum256(append([]byte(kind+"\x00"), raw[17:25]...))
	copy(raw[25:], digest[:16])
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func DecodeOffset(raw, kind string) (int64, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) != 41 || data[0] != 2 {
		return 0, fmt.Errorf("invalid continuation cursor")
	}
	scope := sha256.Sum256([]byte(kind))
	if string(data[1:17]) != string(scope[:16]) {
		return 0, fmt.Errorf("continuation cursor scope does not match")
	}
	digest := sha256.Sum256(append([]byte(kind+"\x00"), data[17:25]...))
	if string(data[25:]) != string(digest[:16]) {
		return 0, fmt.Errorf("invalid continuation cursor")
	}
	offset := binary.BigEndian.Uint64(data[17:25])
	if offset > uint64(^uint64(0)>>1) {
		return 0, fmt.Errorf("invalid continuation cursor")
	}
	return int64(offset), nil
}

// EncodeRangeCursor returns a bounded cursor for a line range. The scope is
// carried in the cursor so the caller can reject reuse for another request.
// The scope remains tied to the complete server-owned query while the cursor
// carries the next line and the requested range end for continuation.
func EncodeRangeCursor(kind string, offset, end int) string {
	if offset < 0 || end < offset {
		return ""
	}
	scope := sha256.Sum256([]byte(kind))
	var raw [49]byte
	raw[0] = 4
	copy(raw[1:17], scope[:16])
	binary.BigEndian.PutUint64(raw[17:25], uint64(offset))
	binary.BigEndian.PutUint64(raw[25:33], uint64(end))
	digest := sha256.Sum256(append([]byte(kind+"\x00"), raw[17:33]...))
	copy(raw[33:], digest[:16])
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// DecodeRangeCursor validates and decodes a cursor created by
// EncodeRangeCursor. It deliberately binds only to kind; the caller remains
// responsible for validating the decoded line positions against its object.
func DecodeRangeCursor(raw, kind string) (int, int, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) != 49 || data[0] != 4 {
		return 0, 0, fmt.Errorf("invalid continuation cursor")
	}
	scope := sha256.Sum256([]byte(kind))
	if string(data[1:17]) != string(scope[:16]) {
		return 0, 0, fmt.Errorf("continuation cursor scope does not match")
	}
	digest := sha256.Sum256(append([]byte(kind+"\x00"), data[17:33]...))
	if string(data[33:]) != string(digest[:16]) {
		return 0, 0, fmt.Errorf("invalid continuation cursor")
	}
	offset := binary.BigEndian.Uint64(data[17:25])
	end := binary.BigEndian.Uint64(data[25:33])
	maxInt := uint64(^uint(0) >> 1)
	if offset > maxInt || end > maxInt || end < offset {
		return 0, 0, fmt.Errorf("invalid continuation cursor")
	}
	return int(offset), int(end), nil
}

func EncodeSearchCursor(kind, path string, line int) string {
	if line < 0 {
		return ""
	}
	scope := sha256.Sum256([]byte(kind))
	pathDigest := sha256.Sum256([]byte(kind + "\x00" + path))
	var raw [37]byte
	raw[0] = 3
	copy(raw[1:17], scope[:16])
	copy(raw[17:33], pathDigest[:16])
	binary.BigEndian.PutUint32(raw[33:37], uint32(line))
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func ValidateSearchCursor(raw, kind string) error {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) != 37 || data[0] != 3 {
		return fmt.Errorf("invalid continuation cursor")
	}
	scope := sha256.Sum256([]byte(kind))
	if string(data[1:17]) != string(scope[:16]) {
		return fmt.Errorf("continuation cursor scope does not match")
	}
	return nil
}

func SearchCursorPathMatches(raw, kind, path string) bool {
	if ValidateSearchCursor(raw, kind) != nil {
		return false
	}
	data, _ := base64.RawURLEncoding.DecodeString(raw)
	pathDigest := sha256.Sum256([]byte(kind + "\x00" + path))
	return string(data[17:33]) == string(pathDigest[:16])
}

func DecodeSearchCursorLine(raw, kind, path string) (int, error) {
	if err := ValidateSearchCursor(raw, kind); err != nil {
		return 0, err
	}
	if !SearchCursorPathMatches(raw, kind, path) {
		return 0, fmt.Errorf("continuation cursor path does not match")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid continuation cursor")
	}
	return int(binary.BigEndian.Uint32(data[33:37])), nil
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
