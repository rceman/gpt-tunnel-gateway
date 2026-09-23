package service

import (
	"strings"
	"testing"
)

func TestTaskExecutionHistoricalCommitFingerprintResolution(t *testing.T) {
	first := "abcdef01" + strings.Repeat("1", 32)
	second := "12345678" + strings.Repeat("2", 32)
	resolved, err := resolveTaskExecutionHistoricalCommitReference("abcdef01", []string{first, second})
	if err != nil || resolved != first {
		t.Fatalf("unique short fingerprint resolved to %q, %v", resolved, err)
	}
	if resolved, err := resolveTaskExecutionHistoricalCommitReference(first, []string{first, second}); err != nil || resolved != first {
		t.Fatalf("exact internal commit resolution=%q, %v", resolved, err)
	}
	if _, err := resolveTaskExecutionHistoricalCommitReference("abcdef01", []string{first, "abcdef01" + strings.Repeat("3", 32)}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous fingerprint result error=%v", err)
	}
	if _, err := resolveTaskExecutionHistoricalCommitReference("87654321", []string{first, second}); err == nil || !strings.Contains(err.Error(), "unknown or stale") {
		t.Fatalf("unknown fingerprint result error=%v", err)
	}
	if validTaskExecutionCommitReference("ABCDEF01") || validTaskExecutionCommitReference("abcdef012") || !validTaskExecutionCommitReference("abcdef01") {
		t.Fatal("public Git fingerprint validator accepted an invalid value or rejected an 8-character lowercase fingerprint")
	}
}
