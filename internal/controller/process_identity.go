package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func (c Controller) pidPath(name string) string {
	return filepath.Join(c.Config.Controller.PIDDir, name+".pid")
}
func (c Controller) logPath(name string) string {
	return filepath.Join(c.Config.Controller.LogDir, name+".log")
}
func readPID(path string) (int, error) {
	record, err := readPIDRecord(path)
	if err != nil {
		return 0, err
	}
	return record.PID, nil
}
func readPIDRecord(path string) (pidRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pidRecord{}, err
	}
	var record pidRecord
	if len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '{' {
		if err := json.Unmarshal(data, &record); err != nil {
			return pidRecord{}, err
		}
		if record.PID < 1 {
			return pidRecord{}, fmt.Errorf("invalid PID record")
		}
		return record, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		return pidRecord{}, fmt.Errorf("invalid PID file")
	}
	return pidRecord{PID: pid}, nil
}
func procExe(pid int) (string, error) {
	return filepath.EvalSymlinks(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}
func procCmdline(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return "", err
	}
	return strings.Join(strings.FieldsFunc(string(data), func(r rune) bool { return r == 0 }), " "), nil
}
func procStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return 0, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data[closeParen+2:]))
	if len(fields) < 20 {
		return 0, fmt.Errorf("invalid process stat fields")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}
func procUID(pid int) (uint32, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 32)
		return uint32(value), parseErr
	}
	return 0, fmt.Errorf("process UID unavailable")
}
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
func (c Controller) process(name, expected string) ProcessStatus {
	p := ProcessStatus{
		Name:               name,
		ExpectedExecutable: expected,
	}
	if expected == "" {
		p.IdentityReason = "configured executable is unavailable"
		return p
	}
	record, err := readPIDRecord(c.pidPath(name))
	if err != nil {
		return p
	}
	p.PID = record.PID
	if !alive(record.PID) {
		_ = os.Remove(c.pidPath(name))
		return p
	}
	p.StartTimeTicks, _ = procStartTime(record.PID)
	uid, uidErr := procUID(record.PID)
	cmdline, cmdErr := procCmdline(record.PID)
	if uidErr != nil || cmdErr != nil || uid != uint32(os.Getuid()) {
		p.IdentityReason = "process UID does not match controller owner"
		return p
	}
	if record.StartTimeTicks != 0 && record.StartTimeTicks != p.StartTimeTicks {
		p.IdentityReason = "PID was reused after controller record"
		return p
	}
	if !strings.Contains(cmdline, expected) {
		p.IdentityReason = "configured executable is absent from process command line"
		return p
	}
	if name == "gateway" && c.ConfigPath != "" && !strings.Contains(cmdline, c.ConfigPath) {
		p.IdentityReason = "configured gateway config is absent from process command line"
		return p
	}
	if name == "tunnel" && !strings.Contains(cmdline, " run") && !strings.HasSuffix(cmdline, " run") {
		p.IdentityReason = "managed tunnel command is not run"
		return p
	}
	exe, _ := procExe(record.PID)
	p.Executable = exe
	p.Running = true
	p.IdentityValid = true
	return p
}
