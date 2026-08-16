package activation

import "bytes"

const DiagnosticOutputLimit = 16 << 10

// BoundedDiagnosticOutput keeps failure evidence deterministic and bounded.
// The prefix is retained because command phase headers and first errors are
// normally emitted before verbose subprocess output.
func BoundedDiagnosticOutput(data []byte) (string, bool) {
	if len(data) <= DiagnosticOutputLimit {
		return string(data), false
	}
	return string(bytes.TrimSpace(data[:DiagnosticOutputLimit])), true
}
