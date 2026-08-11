package pagination

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestPageUsesOpaqueDeterministicContinuation(t *testing.T) {
	items := []string{"a", "b", "c"}
	page, info, err := Page("items", items, 2, "", func(item string) string { return item })
	if err != nil || len(page) != 2 || page[0] != "a" || page[1] != "b" || !info.HasMore || info.NextCursor == "" {
		t.Fatalf("unexpected first page: %#v %#v %v", page, info, err)
	}
	if len(info.NextCursor) != CompactCursorLength || strings.ContainsAny(info.NextCursor, "Il1O0+/=") {
		t.Fatalf("cursor is not compact and agent-safe: %q", info.NextCursor)
	}
	page, info, err = Page("items", items, 2, info.NextCursor, func(item string) string { return item })
	if err != nil || len(page) != 1 || page[0] != "c" || info.HasMore || info.NextCursor != "" {
		t.Fatalf("unexpected continuation page: %#v %#v %v", page, info, err)
	}
	if _, _, err := Page("other", items, 2, Encode("items", "b"), func(item string) string { return item }); err == nil {
		t.Fatal("expected cursor kind mismatch")
	}
}

func TestPageAcceptsLegacyCursorAndRejectsStaleCompactCursor(t *testing.T) {
	legacy, err := jsonLegacyCursor("items", "b")
	if err != nil {
		t.Fatal(err)
	}
	page, _, err := Page("items", []string{"a", "b", "c"}, 2, legacy, func(item string) string { return item })
	if err != nil || len(page) != 1 || page[0] != "c" {
		t.Fatalf("legacy cursor continuation=%#v err=%v", page, err)
	}
	if _, _, err := Page("items", []string{"a", "c"}, 2, Encode("items", "b"), func(item string) string { return item }); err == nil {
		t.Fatal("stale compact cursor accepted")
	}
}

func jsonLegacyCursor(kind, key string) (string, error) {
	data, err := json.Marshal(cursor{Kind: kind, Key: key})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func TestLimitHasDefaultAndHardMaximum(t *testing.T) {
	if got, err := Limit(0, 1000); err != nil || got != DefaultLimit {
		t.Fatalf("default limit = %d, %v", got, err)
	}
	if _, err := Limit(MaxLimit+1, 1000); err == nil {
		t.Fatal("expected hard maximum rejection")
	}
	if got, err := Limit(0, 3); err != nil || got != 3 {
		t.Fatalf("configured cap = %d, %v", got, err)
	}
}
