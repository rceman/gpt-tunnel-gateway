package controller

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

func readTunnelEnv(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("tunnel env file must be a regular file with mode 0600 or stricter")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	reserved := map[string]bool{
		"MCP_SERVER_URL": true, "MCP_COMMAND": true, "HEALTH_LISTEN_ADDR": true,
		"TUNNEL_CLIENT_CONFIG": true, "TUNNEL_CLIENT_PROFILE": true, "TUNNEL_CLIENT_PROFILE_FILE": true,
	}
	scan := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" || !validEnvName(k) {
			return nil, fmt.Errorf("invalid tunnel env line")
		}
		if reserved[k] {
			return nil, fmt.Errorf("tunnel env must not override controller-owned variable %s", k)
		}
		if _, exists := values[k]; exists {
			return nil, fmt.Errorf("duplicate tunnel env variable %s", k)
		}
		values[k] = v
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	for _, required := range []string{"CONTROL_PLANE_API_KEY", "CONTROL_PLANE_TUNNEL_ID"} {
		if values[required] == "" {
			return nil, fmt.Errorf("tunnel env is missing %s", required)
		}
	}
	if !regexp.MustCompile(`^tunnel_[0-9a-f]{32}$`).MatchString(values["CONTROL_PLANE_TUNNEL_ID"]) {
		return nil, fmt.Errorf("CONTROL_PLANE_TUNNEL_ID has invalid format")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out, nil
}

// ValidateTunnelEnv validates the controller-owned environment without exposing values.
func ValidateTunnelEnv(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("tunnel env must be an owner-only regular file")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("tunnel env owner mismatch")
	}
	_, err = readTunnelEnv(path)
	return err
}

func validEnvName(name string) bool {
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return name != ""
}

func processEnv(extra []string) []string {
	values := map[string]string{}
	values["LC_ALL"] = "C"
	for _, key := range []string{"HOME", "PATH", "USER", "LOGNAME", "TMPDIR", "SSH_AUTH_SOCK", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	for _, entry := range extra {
		key, value, ok := strings.Cut(entry, "=")
		if ok && validEnvName(key) {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
