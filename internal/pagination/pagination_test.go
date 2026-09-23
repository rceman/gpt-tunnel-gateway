package pagination

import (
	"strings"
	"testing"
)

func resetCompactHandles() {
	compactHandles.Lock()
	compactHandles.values = make(map[string]map[string]string)
	compactHandles.ambiguous = make(map[string]map[string]struct{})
	compactHandles.order = nil
	compactHandles.Unlock()
}

func TestPageUsesOpaqueDeterministicContinuation(t *testing.T) {
	resetCompactHandles()
	items := []string{"a", "b", "c"}
	page, info, err := Page("items", items, 2, "", func(item string) string { return item })
	if err != nil || len(page) != 2 || page[0] != "a" || page[1] != "b" || !info.HasMore || info.NextCursor == "" {
		t.Fatalf("unexpected first page: %#v %#v %v", page, info, err)
	}
	if !ValidServerCursor(info.NextCursor) {
		t.Fatalf("cursor is not a compact server-owned handle: %q", info.NextCursor)
	}
	if info.NextCursor != EncodeServerCursor("items", "b") {
		t.Fatalf("cursor handle was not deterministic: %q", info.NextCursor)
	}
	page, info, err = Page("items", items, 2, info.NextCursor, func(item string) string { return item })
	if err != nil || len(page) != 1 || page[0] != "c" || info.HasMore || info.NextCursor != "" {
		t.Fatalf("unexpected continuation page: %#v %#v %v", page, info, err)
	}
	if _, _, err := Page("other", items, 2, Encode("items", "b"), func(item string) string { return item }); err == nil {
		t.Fatal("expected cursor kind mismatch")
	}
}

func TestPageRejectsLegacyUnknownAndStaleCursors(t *testing.T) {
	resetCompactHandles()
	legacy := strings.Repeat("A", 40)
	if _, _, err := Page("items", []string{"a", "b", "c"}, 2, legacy, func(item string) string { return item }); err == nil {
		t.Fatal("legacy self-contained cursor was accepted")
	}
	unknown := "ABCDEFGH"
	if _, ok := ResolveServerCursor(unknown, "items"); ok {
		t.Fatal("unknown handle resolved")
	}
	cursor := EncodeServerCursor("items", "b")
	if _, _, err := Page("items", []string{"a", "c"}, 2, cursor, func(item string) string { return item }); err == nil {
		t.Fatal("stale compact cursor accepted")
	}
}

func TestOpaqueKeysetsResolveOnlyWithinExactScope(t *testing.T) {
	resetCompactHandles()
	kind := "code-diff|gpt-tunnel-gateway|WT-TRN63-abcdef12|" + strings.Repeat("a", 40) + "|" + strings.Repeat("b", 40) + "|query"
	key := strings.Repeat("nested/path/", 40)
	cursor := EncodeOpaqueKeyset(kind, key)
	if !ValidServerCursor(cursor) || strings.Contains(cursor, key) {
		t.Fatalf("opaque cursor disclosed or exceeded its public contract: %q", cursor)
	}
	if resolved, err := DecodeOpaqueKeyset(cursor, kind); err != nil || resolved != key {
		t.Fatalf("server handle failed exact resolution: %d bytes, %v", len(resolved), err)
	}
	if _, err := DecodeOpaqueKeyset(cursor, kind+"-changed-head"); err == nil {
		t.Fatal("cursor accepted an incompatible scope")
	}
	if _, err := DecodeOpaqueKeyset("ABCDEFGH", kind); err == nil {
		t.Fatal("unknown compact handle resolved")
	}
}

func TestAmbiguousAndCapacityExhaustedServerHandlesFailClosed(t *testing.T) {
	resetCompactHandles()
	handle := EncodeServerCursor("ambiguous", "first")
	compactHandles.Lock()
	compactHandles.values["ambiguous"][handle] = "different"
	compactHandles.Unlock()
	if got := EncodeServerCursor("ambiguous", "first"); got != "" {
		t.Fatalf("ambiguous handle was reissued: %q", got)
	}
	if _, ok := ResolveServerCursor(handle, "ambiguous"); ok {
		t.Fatal("ambiguous handle resolved")
	}

	resetCompactHandles()
	stale := EncodeServerCursor("stale", "first")
	compactHandles.Lock()
	compactHandles.order = make([]string, compactHandleCapacity)
	compactHandles.Unlock()
	if key, ok := ResolveServerCursor(stale, "stale"); !ok || key != "first" {
		t.Fatal("capacity pressure invalidated an issued cursor")
	}
	if got := EncodeServerCursor("full", "new"); got != "" {
		t.Fatalf("issued a cursor after fail-closed capacity limit: %q", got)
	}
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
