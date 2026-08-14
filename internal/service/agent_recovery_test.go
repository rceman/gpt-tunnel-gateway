package service

import "testing"

func TestRecoveryBlockIsTerminalAndPhaseSpecific(t *testing.T) {
	result, err := recoveryBlockAt(AgentRecoveryResult{
		Outcome: "blocked",
		Phase:   "session_probe",
	}, "session_probe", "controller unavailable")
	if err == nil || result.Outcome != "blocked" || result.Phase != "session_probe" || result.RecoveryEvent != "recovery_blocked" || result.Reason != "controller unavailable" {
		t.Fatalf("unexpected recovery blocker result=%+v err=%v", result, err)
	}
}
