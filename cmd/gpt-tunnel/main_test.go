package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestReviewSnapshotCLISuccessRenderingPath(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	output(model.ReviewSnapshot{SchemaVersion: 1, ReviewState: "active", NextAction: "wait_for_terminal"})
	_ = w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"review_state": "active"`) {
		t.Fatalf("unexpected rendering: %s", data)
	}
}

func TestReviewSnapshotCLIErrorRenderingPathIsBounded(t *testing.T) {
	s := service.New(config.Config{})
	_, err := s.RunReviewSnapshot(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "clone managed hub repository") {
		t.Fatalf("unexpected CLI error: %v", err)
	}
}
