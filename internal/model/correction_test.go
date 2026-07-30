package model

import "testing"

func TestADRIdentifierRejectsPathTraversal(t *testing.T) {
	for _, id := range []string{"../../escape", "ADR/0001", "ADR-../x", ".git"} {
		if ValidateADRIdentifier(id) == nil {
			t.Fatalf("unsafe ADR id accepted: %q", id)
		}
	}
	if err := ValidateADRIdentifier("ADR-0001"); err != nil {
		t.Fatal(err)
	}
}

func TestRelativePathRejectsGitAndBackslash(t *testing.T) {
	for _, path := range []string{".git/config", ".GIT/config", `dir\file`} {
		if ValidateRelativePath(path) == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
}
