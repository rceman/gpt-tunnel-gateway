package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCheckAndWriteModes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(path, []byte("package fixture\n\ntype S struct { A int; B int }\nvar _ = S{A: 1, B: 2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--check", path}, &strings.Builder{}, &strings.Builder{}); err == nil {
		t.Fatal("check mode accepted non-canonical source")
	}
	if err := run([]string{"--write", path}, &strings.Builder{}, &strings.Builder{}); err != nil {
		t.Fatalf("write mode: %v", err)
	}
	if err := run([]string{"--check", path}, &strings.Builder{}, &strings.Builder{}); err != nil {
		t.Fatalf("check mode after write: %v", err)
	}
}
