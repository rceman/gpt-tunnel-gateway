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

func TestParseUpgradeArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{name: "run", want: "run", ok: true},
		{name: "inspect", args: []string{"inspect"}, want: "inspect", ok: true},
		{name: "status", args: []string{"status"}, want: "status", ok: true},
		{name: "unknown", args: []string{"bogus"}},
		{name: "extra", args: []string{"status", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUpgradeArgs(tt.args)
			if tt.ok && (err != nil || got != tt.want) {
				t.Fatalf("parseUpgradeArgs(%q) = %q, %v; want %q", tt.args, got, err, tt.want)
			}
			if !tt.ok && err == nil {
				t.Fatalf("parseUpgradeArgs(%q) unexpectedly succeeded", tt.args)
			}
		})
	}
}

func TestDispatchUpgradeStatusDoesNotRunUpgrade(t *testing.T) {
	var configLoads, runnerRuns, inspectCalls, statusCalls int
	status := func() {
		configLoads++
		statusCalls++
	}
	err := dispatchUpgrade([]string{"status"}, func() { runnerRuns++ }, func() { inspectCalls++ }, status)
	if err != nil || configLoads != 1 || runnerRuns != 0 || inspectCalls != 0 || statusCalls != 1 {
		t.Fatalf("dispatch status: err=%v config=%d runs=%d inspect=%d status=%d", err, configLoads, runnerRuns, inspectCalls, statusCalls)
	}
}

func TestDispatchUpgradeInvalidArgsHasNoSideEffects(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"status", "extra"}, {"inspect", "extra"}} {
		var configLoads, runnerRuns, inspectCalls, statusCalls int
		err := dispatchUpgrade(args, func() { runnerRuns++ }, func() { inspectCalls++ }, func() { configLoads++; statusCalls++ })
		if err == nil || configLoads != 0 || runnerRuns != 0 || inspectCalls != 0 || statusCalls != 0 {
			t.Fatalf("dispatch %q: err=%v config=%d runs=%d inspect=%d status=%d", args, err, configLoads, runnerRuns, inspectCalls, statusCalls)
		}
	}
}
