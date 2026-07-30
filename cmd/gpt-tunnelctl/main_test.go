package main

import "testing"

func TestUpgradeResultSelection(t *testing.T) {
	for _, test := range []struct {
		status string
		want   bool
	}{
		{status: "", want: false},
		{status: "UPGRADE_COMPLETE", want: false},
		{status: "UPGRADE_ROLLED_BACK", want: true},
		{status: "UPGRADE_ROLLBACK_FAILED", want: true},
	} {
		if got := upgradeResultShouldPrint(test.status); got != test.want {
			t.Fatalf("status %q: got %v want %v", test.status, got, test.want)
		}
	}
}
