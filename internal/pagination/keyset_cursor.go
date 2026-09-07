package pagination

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

// EncodeOpaqueKeyset stores a bounded, integrity-protected keyset token.
func EncodeOpaqueKeyset(kind, key string) string {
	if key == "" || len(key) > 65535 {
		return ""
	}
	scope := sha256.Sum256([]byte(kind))
	raw := make([]byte, 0, 1+16+2+len(key)+16)
	raw = append(raw, 5)
	raw = append(raw, scope[:16]...)
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(key)))
	raw = append(raw, size[:]...)
	raw = append(raw, key...)
	digest := sha256.Sum256(append([]byte(kind+"\x00"), []byte(key)...))
	raw = append(raw, digest[:16]...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeOpaqueKeyset(raw, kind string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) < 1+16+2+16 || data[0] != 5 {
		return "", fmt.Errorf("invalid continuation cursor")
	}
	scope := sha256.Sum256([]byte(kind))
	if string(data[1:17]) != string(scope[:16]) {
		return "", fmt.Errorf("continuation cursor scope does not match")
	}
	size := int(binary.BigEndian.Uint16(data[17:19]))
	if len(data) != 1+16+2+size+16 || size == 0 {
		return "", fmt.Errorf("invalid continuation cursor")
	}
	key := string(data[19 : 19+size])
	digest := sha256.Sum256(append([]byte(kind+"\x00"), []byte(key)...))
	if string(data[19+size:]) != string(digest[:16]) {
		return "", fmt.Errorf("invalid continuation cursor")
	}
	return key, nil
}
