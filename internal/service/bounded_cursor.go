package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// boundedIndexCursor is the opaque, integrity-checked envelope shared by
// bounded read projections. The query, filter, root and page size are bound
// into the cursor so callers cannot reuse a page token for another query.
type boundedIndexCursor struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	ProjectID string `json:"project_id"`
	Query     string `json:"query"`
	Filter    string `json:"filter"`
	Root      string `json:"root"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
	Checksum  string `json:"checksum"`
}

func boundedCursorChecksum(cursor boundedIndexCursor) string {
	cursor.Checksum = ""
	encoded, _ := json.Marshal(cursor)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func encodeBoundedIndexCursor(cursor boundedIndexCursor) (string, error) {
	cursor.Checksum = boundedCursorChecksum(cursor)
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeBoundedIndexCursor(raw string) (boundedIndexCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return boundedIndexCursor{}, fmt.Errorf("cursor is empty")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return boundedIndexCursor{}, fmt.Errorf("invalid cursor")
	}
	var cursor boundedIndexCursor
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return boundedIndexCursor{}, fmt.Errorf("invalid cursor")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || cursor.Version != 1 || cursor.Kind == "" || cursor.ProjectID == "" || cursor.Root == "" || cursor.Limit < 1 || cursor.Offset < 1 || cursor.Checksum == "" || cursor.Checksum != boundedCursorChecksum(cursor) {
		return boundedIndexCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}
