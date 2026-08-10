package tailcursor

import (
	"strings"
	"testing"
)

func TestCursorPagesMultipleNewLinesAndEmptyDelta(t *testing.T) {
	first, err := Initial("project:demo", "demo_master", []string{"one", "two"}, 1, 0)
	if err != nil || first.Text != "two\n" || first.NextCursor == "" || first.HasMore {
		t.Fatalf("initial page=%#v err=%v", first, err)
	}
	second, err := Continue("project:demo", "demo_master", first.NextCursor, []string{"one", "two", "three", "four"}, 1)
	if err != nil || second.Text != "three\n" || !second.HasMore {
		t.Fatalf("first catch-up page=%#v err=%v", second, err)
	}
	third, err := Continue("project:demo", "demo_master", second.NextCursor, []string{"one", "two", "three", "four"}, 1)
	if err != nil || third.Text != "four\n" || third.HasMore {
		t.Fatalf("second catch-up page=%#v err=%v", third, err)
	}
	empty, err := Continue("project:demo", "demo_master", third.NextCursor, []string{"one", "two", "three", "four"}, 1)
	if err != nil || empty.Text != "" || empty.NextCursor == "" || empty.HasMore {
		t.Fatalf("empty page=%#v err=%v", empty, err)
	}
}

func TestCursorRejectsInvalidScopeSessionAndTruncation(t *testing.T) {
	page, err := Initial("project:demo", "demo_master", []string{"one", "two"}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		scope   string
		session string
	}{
		{name: "scope", scope: "project:other", session: "demo_master"},
		{name: "session", scope: "project:demo", session: "replacement_master"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Continue(test.scope, test.session, page.NextCursor, []string{"one", "two"}, 1)
			if err == nil || !strings.Contains(err.Error(), "invalid tail cursor") {
				t.Fatalf("cursor accepted: %v", err)
			}
		})
	}
	_, err = Continue("project:demo", "demo_master", page.NextCursor, []string{"replacement"}, 1)
	if err == nil || !strings.Contains(err.Error(), "stale tail cursor") {
		t.Fatalf("truncated snapshot accepted: %v", err)
	}
}

func TestCursorNeverEmbedsSessionOrSnapshotText(t *testing.T) {
	page, err := Initial("project:demo", "secret_session", []string{"secret output"}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.NextCursor, "secret_session") || strings.Contains(page.NextCursor, "secret output") {
		t.Fatalf("cursor leaked caller/session data: %q", page.NextCursor)
	}
}
