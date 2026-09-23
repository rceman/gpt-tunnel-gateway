package publicprojection

import (
	"reflect"
	"strings"
	"testing"
)

func TestProjectCompactsGitFingerprintsAndOmitsNonGitDigests(t *testing.T) {
	input := map[string]any{
		"key": "GTW-TSK573", "title": "Preserved title", "path": "internal/main.go", "created_at": "2026-09-23T11:00:00Z",
		"head":               "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		"tree_id":            "1234567890abcdef1234567890abcdef12345678",
		"parents":            []any{"abcdef0123456789abcdef0123456789abcdef01"},
		"old_hub_sha":        "deadbeef1234567890abcdef1234567890abcdef",
		"canonical_head":     "cafebabe1234567890abcdef1234567890abcdef",
		"task_sha256":        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"source_fingerprint": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"contract_digest":    "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"digest":             "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"command_digests":    map[string]any{"test": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
		"effective":          map[string]any{"digest": "a1b2c3d4", "rules": map[string]any{"format": "strict"}},
		"file_hash":          "0123abcd",
		"large_file_hash":    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"operation_id":       "gateway-debug-activation-0123456789abcdef0123456789abcdef01234567",
	}
	projectedValue, err := Project(input)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectedValue.(map[string]any)
	if projected["head"] != "abcdef01" || projected["tree_id"] != "12345678" || projected["old_hub_sha"] != "deadbeef" || projected["canonical_head"] != "cafebabe" || !reflect.DeepEqual(projected["parents"], []any{"abcdef01"}) {
		t.Fatalf("Git fingerprints were not compacted: %#v", projected)
	}
	for _, field := range []string{"task_sha256", "source_fingerprint", "contract_digest", "digest", "command_digests", "large_file_hash"} {
		if _, exists := projected[field]; exists {
			t.Fatalf("non-Git full identifier %q was retained: %#v", field, projected)
		}
	}
	if projected["key"] != input["key"] || projected["title"] != input["title"] || projected["path"] != input["path"] || projected["created_at"] != input["created_at"] || projected["file_hash"] != input["file_hash"] || projected["operation_id"] != "gateway-debug-activation-01234567" {
		t.Fatalf("semantic values or compliant code hash changed: %#v", projected)
	}
	effective := projected["effective"].(map[string]any)
	if effective["digest"] != "a1b2c3d4" {
		t.Fatalf("already-compact Rule digest changed: %#v", effective)
	}
}

func TestResolveGitFingerprintRequiresScopedExactAuthority(t *testing.T) {
	scope := "publicprojection-test-unique"
	full := "0123abcd" + strings.Repeat("1", 32)
	projected, err := ProjectMapForScope(map[string]any{"head": full}, scope)
	if err != nil || projected["head"] != "0123abcd" {
		t.Fatalf("Git fingerprint projection=%#v err=%v", projected, err)
	}
	if resolved, err := ResolveGitFingerprint(scope, "0123abcd", []string{full}); err != nil || resolved != full {
		t.Fatalf("exact authoritative resolution=%q err=%v", resolved, err)
	}
	if _, err := ResolveGitFingerprint(scope, "0123abcd", []string{"fedcba98" + strings.Repeat("2", 32)}); err == nil {
		t.Fatal("stale fingerprint resolved against unrelated authoritative state")
	}
	if _, err := ResolveGitFingerprint("other-scope", "0123abcd", []string{full}); err == nil {
		t.Fatal("fingerprint resolved outside its registered project scope")
	}
}

func TestProjectRejectsAmbiguousPublicGitFingerprint(t *testing.T) {
	scope := "publicprojection-ambiguous-test"
	first := "abcdef01" + strings.Repeat("1", 32)
	second := "abcdef01" + strings.Repeat("2", 32)
	if _, err := ProjectMapForScope(map[string]any{"head": first}, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectMapForScope(map[string]any{"head": second}, scope); err == nil {
		t.Fatal("ambiguous public Git fingerprint was projected")
	}
	if _, err := ResolveGitFingerprint(scope, "abcdef01", []string{first}); err == nil {
		t.Fatal("ambiguous public Git fingerprint resolved")
	}
}

func TestGitTextProjectionIsLimitedToGitActions(t *testing.T) {
	full := "abcdef01" + strings.Repeat("1", 32)
	text := "commit " + full + "\n\nmessage " + full + "\n"
	projected, err := Project(map[string]any{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	if projected.(map[string]any)["text"] != text {
		t.Fatal("ordinary text field was rewritten as Git metadata")
	}
	projectedAction, err := ProjectMapForAction(map[string]any{"text": text}, "git-project", "git_show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projectedAction["text"].(string), "commit abcdef01\n") || !strings.Contains(projectedAction["text"].(string), "message "+full) {
		t.Fatalf("git show metadata projection mismatch: %#v", projectedAction)
	}
}

func TestCompactGitTextProjectsOnlyGitMetadata(t *testing.T) {
	scope := "publicprojection-git-text-test"
	commit := "abcdef01" + strings.Repeat("1", 32)
	parent := "12345678" + strings.Repeat("2", 32)
	show := "commit " + commit + "\nParent: ignored\nparent " + parent + "\n\nmessage " + commit + "\n"
	projectedShow, err := CompactGitText(scope, show)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projectedShow, "commit abcdef01\n") || !strings.Contains(projectedShow, "parent 12345678\n") || !strings.Contains(projectedShow, "message "+commit) {
		t.Fatalf("git show metadata projection changed unexpected content: %q", projectedShow)
	}
	diff := "diff --git a/file b/file\nindex " + commit + ".." + parent + " 100644\n--- a/file\n+++ b/file\n@@ -0,0 +1 @@\n+commit " + commit + "\n"
	projectedDiff, err := CompactGitText(scope, diff)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projectedDiff, "index abcdef01..12345678 100644\n") || !strings.Contains(projectedDiff, "+commit "+commit) {
		t.Fatalf("git diff index fingerprints were not compacted: %q", projectedDiff)
	}
}

func TestProjectPreservesNonGitDigestOnlyWhenAlreadyCompact(t *testing.T) {
	projectedValue, err := Project(map[string]any{"digest": "a1b2c3d4", "gate_profile": "a1b2c3d4"})
	if err != nil {
		t.Fatal(err)
	}
	projected := projectedValue.(map[string]any)
	if projected["digest"] != "a1b2c3d4" {
		t.Fatalf("compliant digest was changed: %#v", projected)
	}
	if _, exists := projected["gate_profile"]; exists {
		t.Fatalf("non-Git compacted authority alias remained public: %#v", projected)
	}
}
