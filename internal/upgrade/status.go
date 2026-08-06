package upgrade

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

const maxStatusTransactionBytes = 1 << 20

var transactionFileRE = regexp.MustCompile(`^upgrade-[0-9]{8}T[0-9]{6}Z-[0-9]+\.json$`)

type StatusResult struct {
	Status            string               `json:"status"`
	TransactionID     string               `json:"transaction_id,omitempty"`
	SourceVersion     string               `json:"source_version,omitempty"`
	TargetVersion     string               `json:"target_version,omitempty"`
	CurrentPhase      string               `json:"current_phase,omitempty"`
	FinalStatus       string               `json:"final_status,omitempty"`
	StartedAt         *time.Time           `json:"started_at,omitempty"`
	FinishedAt        *time.Time           `json:"finished_at,omitempty"`
	GatewayPIDBefore  int                  `json:"gateway_pid_before,omitempty"`
	GatewayPIDAfter   int                  `json:"gateway_pid_after,omitempty"`
	TunnelPIDBefore   int                  `json:"tunnel_pid_before,omitempty"`
	TunnelPIDAfter    int                  `json:"tunnel_pid_after,omitempty"`
	RollbackAvailable bool                 `json:"rollback_available"`
	ErrorClass        string               `json:"error_class,omitempty"`
	TargetStartup     *StatusTargetStartup `json:"target_startup,omitempty"`
}

type StatusTargetStartup struct {
	Phase                string `json:"phase"`
	CaptureStatus        string `json:"capture_status"`
	TargetPID            int    `json:"target_pid,omitempty"`
	TargetProcessRunning bool   `json:"target_process_running"`
	TargetProcessExited  bool   `json:"target_process_exited"`
	AliveButUnready      bool   `json:"alive_but_unready"`
	ElapsedMilliseconds  int64  `json:"elapsed_ms"`
	ReadinessPassed      bool   `json:"readiness_passed"`
	ErrorClass           string `json:"error_class,omitempty"`
	LogDelta             string `json:"log_delta,omitempty"`
	LogDeltaTruncated    bool   `json:"log_delta_truncated"`
}

func Status(c config.Config) (StatusResult, error) {
	root := filepath.Join(c.StateDir, "upgrade-transactions")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return StatusResult{Status: "no_history"}, nil
		}
		return StatusResult{Status: "corrupt", ErrorClass: "history_unavailable"}, fmt.Errorf("upgrade transaction history is unavailable")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !transactionFileRE.MatchString(entry.Name()) {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(root, entry.Name()))
		if statErr != nil {
			return StatusResult{Status: "corrupt", ErrorClass: "history_unreadable"}, fmt.Errorf("latest upgrade transaction is unreadable")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return StatusResult{Status: "corrupt", ErrorClass: "history_invalid"}, fmt.Errorf("latest upgrade transaction is not a regular file")
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return StatusResult{Status: "no_history"}, nil
	}
	sort.Strings(names)
	name := names[len(names)-1]
	data, err := readStatusTransaction(filepath.Join(root, name))
	if err != nil {
		return StatusResult{Status: "corrupt", ErrorClass: "history_unreadable"}, fmt.Errorf("latest upgrade transaction is unreadable")
	}
	var tx UpgradeTransaction
	dec := json.Unmarshal(data, &tx)
	if dec != nil || tx.TransactionID == "" {
		return StatusResult{Status: "corrupt", ErrorClass: "history_invalid"}, fmt.Errorf("latest upgrade transaction is invalid")
	}
	errorClass := ""
	if tx.PrimaryError != "" {
		errorClass = "transaction_error"
	}
	startup := projectStatusTargetStartup(tx.TargetStartup)
	if startup != nil && startup.ErrorClass != "" {
		errorClass = startup.ErrorClass
	}
	return StatusResult{
		Status:            "available",
		TransactionID:     tx.TransactionID,
		SourceVersion:     tx.SourceVersion,
		TargetVersion:     tx.TargetVersion,
		CurrentPhase:      tx.CurrentPhase,
		FinalStatus:       tx.FinalStatus,
		StartedAt:         &tx.StartedAt,
		FinishedAt:        tx.FinishedAt,
		GatewayPIDBefore:  tx.GatewayPIDBefore,
		GatewayPIDAfter:   tx.GatewayPIDAfter,
		TunnelPIDBefore:   tx.TunnelPIDBefore,
		TunnelPIDAfter:    tx.TunnelPIDAfter,
		RollbackAvailable: tx.RollbackAvailable,
		ErrorClass:        errorClass,
		TargetStartup:     startup,
	}, nil
}

func projectStatusTargetStartup(in *TargetStartupDiagnostics) *StatusTargetStartup {
	if in == nil {
		return nil
	}
	out := &StatusTargetStartup{
		Phase:                in.Phase,
		CaptureStatus:        in.CaptureStatus,
		TargetPID:            in.TargetPID,
		TargetProcessRunning: in.TargetProcessRunning,
		TargetProcessExited:  in.TargetProcessExited,
		AliveButUnready:      in.AliveButUnready,
		ElapsedMilliseconds:  in.ElapsedMilliseconds,
		ReadinessPassed:      in.ReadinessPassed,
		LogDelta:             sanitizeStatusText(in.LogDelta),
		LogDeltaTruncated:    in.LogDeltaTruncated,
	}
	if in.Error != "" {
		out.ErrorClass = "target_startup_error"
	} else if in.DiagnosticCaptureError != "" {
		out.ErrorClass = "diagnostic_capture_error"
	}
	return out
}

var (
	statusSecretRE = regexp.MustCompile(`(?i)(api[_ -]?key|authorization|token|secret|password)[=:][^[:space:]]+`)
	statusURLRE    = regexp.MustCompile(`(?i)\b(?:https?|ssh|file)://[^[:space:]"'<>]+`)
	statusPathRE   = regexp.MustCompile(`/[^[:space:]"'<>;,)]*`)
)

func sanitizeStatusText(value string) string {
	value = statusSecretRE.ReplaceAllString(value, "[redacted]")
	value = statusURLRE.ReplaceAllString(value, "[redacted-url]")
	value = statusPathRE.ReplaceAllString(value, "[redacted-path]")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 16<<10 {
		value = value[len(value)-(16<<10):]
	}
	return value
}

func readStatusTransaction(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("open transaction")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("transaction is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStatusTransactionBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxStatusTransactionBytes {
		return nil, fmt.Errorf("transaction exceeds read limit")
	}
	return data, nil
}
