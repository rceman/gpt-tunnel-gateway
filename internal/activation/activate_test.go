package activation

import (
	"strings"
	"testing"
)

func TestBoundedOutputIsDeterministicAndLimited(t *testing.T) {
	if got := BoundedOutput([]byte("  activation ok  \n")); got != "activation ok" {
		t.Fatalf("unexpected bounded output %q", got)
	}
	if got := BoundedOutput([]byte(strings.Repeat("x", OutputLimit+10))); len(got) != OutputLimit {
		t.Fatalf("output was not bounded: %d", len(got))
	}
}
