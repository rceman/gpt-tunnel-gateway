package service

import (
	"context"
	"strings"
	"testing"
)

func TestQueryRunMessageFailsClosedWithoutHubFallback(t *testing.T) {
	s, _, _ := testService(t)
	_, err := s.QueryRun(context.Background(), QueryRunInput{
		ProjectID: "example",
		DSL:       "message.list().limit(1)",
	})
	if err == nil || !strings.Contains(err.Error(), "Shared Message authority is unavailable") {
		t.Fatalf("message query did not fail closed: %v", err)
	}
}
