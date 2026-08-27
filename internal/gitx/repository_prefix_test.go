package gitx

import (
	"strings"
	"testing"
)

func TestValidateCommitPrefixMatchesFailsClosedOnSamePrefix(t *testing.T) {
	prefix := "deadbeef"
	first := prefix + strings.Repeat("0", 32)
	second := prefix + strings.Repeat("1", 32)
	if err := validateCommitPrefixMatches(prefix, []string{first, second}); err == nil {
		t.Fatal("ambiguous commit prefix was accepted")
	}
}
