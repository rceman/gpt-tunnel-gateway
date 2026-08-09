package upgrade

import (
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

func restartGatewayAfterUpgrade(ctl upgradeController) (controller.GatewayStartupDiagnostics, error) {
	if diagnosticCtl, ok := ctl.(startupDiagnosticController); ok {
		return diagnosticCtl.RestartGatewayAfterUpgradeDiagnostics()
	}
	err := ctl.RestartGatewayAfterUpgrade()
	return controller.GatewayStartupDiagnostics{ReadinessPassed: err == nil, Error: err}, err
}

func targetStartupDiagnostics(in controller.GatewayStartupDiagnostics) *TargetStartupDiagnostics {
	captureStatus := in.CaptureStatus
	if in.ProcessStateError != nil && captureStatus == "captured" {
		captureStatus = "partial"
	}
	d := &TargetStartupDiagnostics{
		Phase:                in.Phase,
		CaptureStatus:        captureStatus,
		TargetPID:            in.TargetPID,
		TargetProcessRunning: in.TargetProcessRunning,
		TargetProcessExited:  in.TargetProcessExited,
		ProcessStateError:    sanitizeError(in.ProcessStateError),
		AliveButUnready:      in.AliveButUnready,
		ElapsedMilliseconds:  in.Elapsed.Milliseconds(),
		ReadinessPassed:      in.ReadinessPassed,
		LogDelta:             sanitizeLogDelta(in.LogDelta),
		LogDeltaTruncated:    in.LogDeltaTruncated,
	}
	if in.Error != nil {
		d.Error = sanitizeError(in.Error)
	}
	if in.LogCaptureError != nil {
		d.DiagnosticCaptureError = sanitizeError(in.LogCaptureError)
	}
	return d
}

func sanitizeLogDelta(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = sanitizeStatusText(line)
	}
	value = strings.Join(lines, "\n")
	const maxLogDelta = 16 << 10
	if len(value) > maxLogDelta {
		value = value[len(value)-maxLogDelta:]
		if newline := strings.IndexByte(value, '\n'); newline >= 0 {
			value = value[newline+1:]
		} else {
			value = ""
		}
	}
	return value
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	s := sanitizeStatusText(err.Error())
	if len(s) > 240 {
		s = s[:240]
	}
	return s

}
