package controller

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The TSK670 activation incident proved that a stale managed Gateway daemon
// can keep serving the configured endpoint while the controller PID record
// points at a dead process. Stop/start bookkeeping alone cannot see that
// owner, so endpoint ownership is resolved directly from procfs: the LISTEN
// socket inode is mapped back to the owning process. Only a process that is
// provably this controller's managed Gateway class may be retired; anything
// else fails closed and is never signaled.

// endpointOwnerPIDs returns the PIDs of local processes holding a LISTEN
// socket on the given host:port. An empty list means the endpoint is free.
func endpointOwnerPIDs(addr string) ([]int, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil
	}
	inodes, err := listenSocketInodes(addr)
	if err != nil {
		return nil, err
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var owners []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid < 1 {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err != nil {
				continue
			}
			if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode, err := strconv.ParseUint(target[len("socket:["):len(target)-1], 10, 64)
			if err == nil && inodes[inode] {
				owners = append(owners, pid)
				break
			}
		}
	}
	return owners, nil
}

// listenSocketInodes returns the inode set of LISTEN sockets bound to the
// configured endpoint port (and address, when the host is a specific IPv4).
func listenSocketInodes(addr string) (map[uint64]bool, error) {
	host, portValue, err := net.SplitHostPort(strings.Trim(addr, "[]"))
	if err != nil {
		return nil, fmt.Errorf("invalid gateway listen address %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portValue, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway listen port %q", portValue)
	}
	if port == 0 {
		// Ephemeral binding cannot be shadowed by a fixed-port listener.
		return nil, nil
	}
	wantAddr := ""
	if parsed := net.ParseIP(strings.Trim(host, "[]")); parsed != nil && !parsed.IsUnspecified() {
		if v4 := parsed.To4(); v4 != nil {
			wantAddr = fmt.Sprintf("%02X%02X%02X%02X", v4[3], v4[2], v4[1], v4[0])
		}
	}
	inodes := map[uint64]bool{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(table)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			local := strings.SplitN(fields[1], ":", 2)
			if len(local) != 2 {
				continue
			}
			listenPort, err := strconv.ParseUint(local[1], 16, 16)
			if err != nil || listenPort != port {
				continue
			}
			if wantAddr != "" && !strings.HasSuffix(table, "tcp6") && local[0] != wantAddr {
				continue
			}
			inode, err := strconv.ParseUint(fields[9], 10, 64)
			if err == nil && inode != 0 {
				inodes[inode] = true
			}
		}
		_ = file.Close()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return inodes, nil
}

// managedGatewayOrphanEvidence describes why an endpoint owner is or is not a
// provably managed stale Gateway daemon.
func managedGatewayOrphanEvidence(pid int, expected string, configPath string) error {
	uid, err := procUID(pid)
	if err != nil {
		return fmt.Errorf("endpoint owner pid %d UID is unreadable: %v", pid, err)
	}
	if uid != uint32(os.Getuid()) {
		return fmt.Errorf("endpoint owner pid %d runs under a different UID %d", pid, uid)
	}
	exe, err := procExe(pid)
	if err != nil {
		return fmt.Errorf("endpoint owner pid %d executable is unreadable: %v", pid, err)
	}
	if filepath.Base(strings.TrimSuffix(exe, " (deleted)")) != filepath.Base(expected) {
		return fmt.Errorf("endpoint owner pid %d executable %q is not the managed Gateway binary class", pid, exe)
	}
	cmdline, err := procCmdline(pid)
	if err != nil {
		return fmt.Errorf("endpoint owner pid %d command line is unreadable: %v", pid, err)
	}
	if configPath == "" || !strings.Contains(cmdline, "--config "+configPath) {
		return fmt.Errorf("endpoint owner pid %d does not serve the configured Gateway config", pid)
	}
	return nil
}

// retireStaleGatewayEndpointOwners finds any process still holding the
// configured endpoint outside the controller PID record and retires it only
// when managed ownership is proven. An unprovable owner fails closed and is
// never signaled.
func (c Controller) retireStaleGatewayEndpointOwners() error {
	owners, err := endpointOwnerPIDs(c.Config.ListenAddr)
	if err != nil {
		return fmt.Errorf("endpoint ownership scan failed: %w", err)
	}
	if len(owners) == 0 {
		return nil
	}
	expected, err := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	if err != nil {
		return fmt.Errorf("installed gateway binary is unavailable for endpoint ownership proof: %w", err)
	}
	record, recordErr := readPIDRecord(c.pidPath("gateway"))
	for _, pid := range owners {
		if recordErr == nil && pid == record.PID {
			// The tracked daemon still owns the endpoint; the canonical stop
			// path owns its lifecycle.
			continue
		}
		if err := managedGatewayOrphanEvidence(pid, expected, c.ConfigPath); err != nil {
			return fmt.Errorf("gateway endpoint %s is held by an unproven process: %w", c.Config.ListenAddr, err)
		}
		c.processEvent("gateway", expected, "warn", "stale_endpoint_owner", pid, "retiring provably managed stale gateway endpoint owner", nil)
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("signal stale gateway endpoint owner pid %d: %w", pid, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for alive(pid) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if alive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			for i := 0; i < 20 && alive(pid); i++ {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if alive(pid) {
			return fmt.Errorf("stale gateway endpoint owner pid %d did not exit", pid)
		}
		if recordErr == nil && pid == record.PID {
			_ = os.Remove(c.pidPath("gateway"))
		}
	}
	return nil
}

// verifyGatewayEndpointCoherence proves that the process actually serving the
// configured endpoint is the recorded, freshly started managed Gateway — not
// a stale or foreign owner that would let a false-positive readiness answer
// stand in for activation success.
func (c Controller) verifyGatewayEndpointCoherence() error {
	owners, err := endpointOwnerPIDs(c.Config.ListenAddr)
	if err != nil {
		return fmt.Errorf("endpoint ownership verification failed: %w", err)
	}
	if len(owners) == 0 {
		return nil
	}
	expected, err := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	if err != nil {
		return fmt.Errorf("installed gateway binary is unavailable for endpoint verification: %w", err)
	}
	for _, pid := range owners {
		exe, err := procExe(pid)
		if err != nil {
			return fmt.Errorf("endpoint owner pid %d executable is unreadable: %w", pid, err)
		}
		if exe != expected {
			return fmt.Errorf("gateway endpoint %s is served by pid %d executable %q, not installed binary %s", c.Config.ListenAddr, pid, exe, expected)
		}
	}
	return nil
}
