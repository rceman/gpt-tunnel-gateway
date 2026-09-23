package pagination

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const CompactCursorLength = 8

const compactAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

const (
	DefaultLimit            = 20
	MaxLimit                = 100
	compactHandleCapacity   = 65536
	maxServerCursorKeyBytes = 65535
)

var compactHandles = struct {
	sync.Mutex
	values    map[string]map[string]string
	ambiguous map[string]map[string]struct{}
	order     []string
}{
	values:    make(map[string]map[string]string),
	ambiguous: make(map[string]map[string]struct{}),
}

type PageInfo struct {
	NextCursor string `json:"-"`
	HasMore    bool   `json:"-"`
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
	return EncodeServerCursor(kind, key)
}

func EncodeServerCursor(kind, key string) string {
	if kind == "" || key == "" || len(key) > maxServerCursorKeyBytes {
		return ""
	}
	handle := compactEncode(kind, key)
	compactHandles.Lock()
	defer compactHandles.Unlock()
	if _, exists := compactHandles.ambiguous[kind][handle]; exists {
		return ""
	}
	if compactHandles.values[kind] == nil {
		compactHandles.values[kind] = make(map[string]string)
	}
	if previous, exists := compactHandles.values[kind][handle]; exists {
		if previous == key {
			return handle
		}
		delete(compactHandles.values[kind], handle)
		if compactHandles.ambiguous[kind] == nil {
			compactHandles.ambiguous[kind] = make(map[string]struct{})
		}
		compactHandles.ambiguous[kind][handle] = struct{}{}
		return ""
	}
	if len(compactHandles.order) >= compactHandleCapacity {
		return ""
	}
	compactHandles.values[kind][handle] = key
	compactHandles.order = append(compactHandles.order, kind+"\x00"+handle)
	return handle
}

func ResolveServerCursor(raw, kind string) (string, bool) {
	if kind == "" || !ValidServerCursor(raw) {
		return "", false
	}
	compactHandles.Lock()
	defer compactHandles.Unlock()
	if _, exists := compactHandles.ambiguous[kind][raw]; exists {
		return "", false
	}
	key, ok := compactHandles.values[kind][raw]
	return key, ok
}

func ValidServerCursor(raw string) bool {
	if len(raw) != CompactCursorLength {
		return false
	}
	for i := range raw {
		if !strings.ContainsRune(compactAlphabet, rune(raw[i])) {
			return false
		}
	}
	return true
}

func OpaqueCursorMatches(raw, kind, key string) bool {
	resolved, ok := ResolveServerCursor(raw, kind)
	return ok && resolved == key
}

func ValidateOpaqueCursor(raw, kind string) error {
	if _, ok := ResolveServerCursor(raw, kind); !ok {
		return fmt.Errorf("invalid or stale continuation cursor")
	}
	return nil
}

func EncodeOffset(kind string, offset int64) string {
	if offset < 0 {
		return ""
	}
	return EncodeServerCursor(kind, strconv.FormatInt(offset, 10))
}

func DecodeOffset(raw, kind string) (int64, error) {
	key, ok := ResolveServerCursor(raw, kind)
	if !ok {
		return 0, fmt.Errorf("invalid or stale continuation cursor")
	}
	offset, err := strconv.ParseInt(key, 10, 64)
	if err != nil || offset < 0 || strconv.FormatInt(offset, 10) != key {
		return 0, fmt.Errorf("invalid continuation cursor")
	}
	return offset, nil
}

func EncodeRangeCursor(kind string, offset, end int) string {
	if offset < 0 || end < offset {
		return ""
	}
	return EncodeServerCursor(kind, strconv.Itoa(offset)+":"+strconv.Itoa(end))
}

func DecodeRangeCursor(raw, kind string) (int, int, error) {
	key, ok := ResolveServerCursor(raw, kind)
	if !ok {
		return 0, 0, fmt.Errorf("invalid or stale continuation cursor")
	}
	separator := strings.IndexByte(key, ':')
	if separator < 1 || strings.IndexByte(key[separator+1:], ':') >= 0 {
		return 0, 0, fmt.Errorf("invalid continuation cursor")
	}
	offset, offsetErr := strconv.Atoi(key[:separator])
	end, endErr := strconv.Atoi(key[separator+1:])
	if offsetErr != nil || endErr != nil || offset < 0 || end < offset || strconv.Itoa(offset)+":"+strconv.Itoa(end) != key {
		return 0, 0, fmt.Errorf("invalid continuation cursor")
	}
	return offset, end, nil
}

func EncodeSearchCursor(kind, path string, line int) string {
	if path == "" || line < 0 {
		return ""
	}
	return EncodeServerCursor(kind, path+"|"+strconv.Itoa(line))
}

func ValidateSearchCursor(raw, kind string) error {
	key, ok := ResolveServerCursor(raw, kind)
	if !ok {
		return fmt.Errorf("invalid or stale continuation cursor")
	}
	if _, _, err := parseSearchKey(key); err != nil {
		return err
	}
	return nil
}

func SearchCursorPathMatches(raw, kind, path string) bool {
	key, ok := ResolveServerCursor(raw, kind)
	if !ok {
		return false
	}
	cursorPath, _, err := parseSearchKey(key)
	return err == nil && cursorPath == path
}

func DecodeSearchCursorLine(raw, kind, path string) (int, error) {
	key, ok := ResolveServerCursor(raw, kind)
	if !ok {
		return 0, fmt.Errorf("invalid or stale continuation cursor")
	}
	cursorPath, line, err := parseSearchKey(key)
	if err != nil {
		return 0, err
	}
	if cursorPath != path {
		return 0, fmt.Errorf("continuation cursor path does not match")
	}
	return line, nil
}

func parseSearchKey(key string) (string, int, error) {
	separator := strings.LastIndexByte(key, '|')
	if separator < 1 {
		return "", 0, fmt.Errorf("invalid continuation cursor")
	}
	line, err := strconv.Atoi(key[separator+1:])
	if err != nil || line < 0 || strconv.Itoa(line) != key[separator+1:] {
		return "", 0, fmt.Errorf("invalid continuation cursor")
	}
	return key[:separator], line, nil
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

func Decode(value, kind string) (string, error) {
	if value == "" {
		return "", nil
	}
	key, ok := ResolveServerCursor(value, kind)
	if !ok {
		return "", fmt.Errorf("invalid or stale continuation cursor")
	}
	return key, nil
}

func Resolve(value, kind string, keys []string) (string, error) {
	if value == "" {
		return "", nil
	}
	key, err := Decode(value, kind)
	if err != nil {
		return "", err
	}
	matches := 0
	for _, candidate := range keys {
		if candidate == key {
			matches++
		}
	}
	if matches == 0 {
		return "", fmt.Errorf("continuation cursor is no longer valid")
	}
	if matches > 1 {
		return "", fmt.Errorf("ambiguous continuation cursor")
	}
	return key, nil
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
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	result := items[start:end]
	info := PageInfo{}
	if end < len(items) && len(result) > 0 {
		info.HasMore = true
		info.NextCursor = EncodeServerCursor(kind, key(result[len(result)-1]))
		if info.NextCursor == "" {
			return nil, PageInfo{}, fmt.Errorf("could not issue an unambiguous continuation cursor")
		}
	}
	return result, info, nil
}
