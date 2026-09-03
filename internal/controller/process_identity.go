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

func expectedCommandLine(name, executable, configPath string) string {
	if name == "gateway" && configPath != "" {
		return executable + " --config " + configPath
	}
	if name == "tunnel" {
		return executable + " run"
	}
	return executable
}

func (c Controller) process(name, expected string) ProcessStatus {
	p := ProcessStatus{
		Name:                name,
		ExpectedExecutable:  expected,
		ExpectedCommandLine: expectedCommandLine(name, expected, c.ConfigPath),
		ExpectedUID:         uint32(os.Getuid()),
	}
	if expected == "" {
		p.IdentityReason = "configured executable is unavailable"
		return p
	}
	record, err := readPIDRecord(c.pidPath(name))
	if err != nil {
		p.IdentityReason = "PID record unavailable"
		return p
	}
	p.PID = record.PID
	p.ExpectedStartTimeTicks = record.StartTimeTicks
	if !alive(record.PID) {
		_ = os.Remove(c.pidPath(name))
		p.IdentityReason = "process is not running"
		return p
	}
	p.Running = true
	p.ActualStartTimeTicks, _ = procStartTime(record.PID)
	p.StartTimeTicks = p.ActualStartTimeTicks
	uid, uidErr := procUID(record.PID)
	cmdline, cmdErr := procCmdline(record.PID)
	p.ActualUID = uid
	p.CommandLine = cmdline
	p.Executable, _ = procExe(record.PID)
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
	p.IdentityValid = true
	return p
}

// ProcessStatus exposes the configured process identity primitive to local
// recovery domains without exposing PID/path storage or lifecycle ownership.
func (c Controller) ProcessStatus(name string) ProcessStatus {
	var expected string
	switch name {
	case "gateway":
		expected = c.Config.Controller.GatewayBinary
	case "tunnel":
		expected = c.Config.Controller.TunnelClientBinary
	}
	return c.process(name, expected)
}
