package pagination

import "fmt"

func EncodeOpaqueKeyset(kind, key string) string {
	return EncodeServerCursor(kind, key)
}

func DecodeOpaqueKeyset(raw, kind string) (string, error) {
	key, ok := ResolveServerCursor(raw, kind)
	if !ok {
		return "", fmt.Errorf("invalid or stale continuation cursor")
	}
	return key, nil
}
