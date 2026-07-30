package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	SchemaVersion          int                      `json:"schema_version"`
	GatewayID              string                   `json:"gateway_id"`
	ListenAddr             string                   `json:"listen_addr"`
	StateDir               string                   `json:"state_dir"`
	MaxReadBytes           int64                    `json:"max_read_bytes"`
	MaxDiffBytes           int64                    `json:"max_diff_bytes"`
	MaxListItems           int                      `json:"max_list_items"`
	DispatchTimeoutSeconds int                      `json:"dispatch_timeout_seconds"`
	RunTimeoutSeconds      int                      `json:"run_timeout_seconds"`
	AirelayCommand         string                   `json:"airelay_command"`
	Hub                    HubConfig                `json:"hub"`
	Controller             ControllerConfig         `json:"controller"`
	Projects               map[string]ProjectConfig `json:"projects"`
}

type HubConfig struct {
	Root                  string `json:"root"`
	Remote                string `json:"remote"`
	Branch                string `json:"branch"`
	ProtocolRoot          string `json:"protocol_root"`
	AllowParallelProtocol bool   `json:"allow_parallel_protocol,omitempty"`
	AuthorName            string `json:"author_name"`
	AuthorEmail           string `json:"author_email"`
}
type ProjectConfig struct {
	Root              string `json:"root"`
	Mirror            string `json:"mirror"`
	Remote            string `json:"remote"`
	DefaultBranch     string `json:"default_branch"`
	AirelaySessionKey string `json:"airelay_session_key"`
}
type ControllerConfig struct {
	GatewayBinary      string `json:"gateway_binary"`
	TunnelClientBinary string `json:"tunnel_client_binary"`
	TunnelEnvFile      string `json:"tunnel_env_file"`
	PIDDir             string `json:"pid_dir"`
	LogDir             string `json:"log_dir"`
	TunnelReadyURL     string `json:"tunnel_ready_url"`
}

func DefaultPath() string {
	if p := os.Getenv("GPT_TUNNEL_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gpt-tunnel-gateway", "config.json")
}
func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("parse config: trailing JSON content")
	}
	c.expand()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	if root, err := canonicalDir(c.Hub.Root); err == nil {
		c.Hub.Root = root
	}
	for id, p := range c.Projects {
		if root, err := canonicalDir(p.Root); err == nil {
			p.Root = root
		}
		c.Projects[id] = p
	}
	return c, nil
}
func (c *Config) expand() {
	c.StateDir = expand(c.StateDir)
	c.Hub.Root = expand(c.Hub.Root)
	c.Controller.GatewayBinary = expand(c.Controller.GatewayBinary)
	c.Controller.TunnelClientBinary = expand(c.Controller.TunnelClientBinary)
	c.Controller.TunnelEnvFile = expand(c.Controller.TunnelEnvFile)
	c.Controller.PIDDir = expand(c.Controller.PIDDir)
	c.Controller.LogDir = expand(c.Controller.LogDir)
	for id, p := range c.Projects {
		p.Root = expand(p.Root)
		p.Mirror = expand(p.Mirror)
		c.Projects[id] = p
	}
}
func expand(s string) string {
	if s == "" {
		return s
	}
	home, _ := os.UserHomeDir()
	if s == "~" {
		return home
	}
	if strings.HasPrefix(s, "~/") {
		return filepath.Join(home, s[2:])
	}
	return s
}
func canonicalDir(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %s", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", resolved)
	}
	return resolved, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("unsupported config schema_version")
	}
	idre := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	if !idre.MatchString(c.GatewayID) {
		return fmt.Errorf("invalid gateway_id")
	}
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen_addr")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen_addr must be loopback")
	}
	if c.StateDir == "" || c.MaxReadBytes < 1 || c.MaxReadBytes > 64<<20 || c.MaxDiffBytes < 1 || c.MaxDiffBytes > 64<<20 || c.MaxListItems < 1 || c.MaxListItems > 10000 {
		return fmt.Errorf("invalid limits")
	}
	if c.DispatchTimeoutSeconds < 1 || c.DispatchTimeoutSeconds > 300 || c.RunTimeoutSeconds < 60 || c.RunTimeoutSeconds > 86400 {
		return fmt.Errorf("invalid timeouts")
	}
	if c.AirelayCommand == "" || strings.ContainsRune(c.AirelayCommand, 0) {
		return fmt.Errorf("invalid airelay command")
	}
	if c.Hub.Root == "" || c.Hub.Remote == "" || c.Hub.Branch == "" || c.Hub.ProtocolRoot == "" {
		return fmt.Errorf("incomplete hub config")
	}
	if _, err := canonicalDir(c.Hub.Root); err != nil {
		return fmt.Errorf("invalid hub root: %w", err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(c.Hub.Remote) {
		return fmt.Errorf("invalid hub remote")
	}
	if filepath.IsAbs(c.Hub.ProtocolRoot) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(c.Hub.ProtocolRoot)), "../") {
		return fmt.Errorf("invalid protocol_root")
	}
	if c.Hub.AuthorName == "" || c.Hub.AuthorEmail == "" || strings.ContainsAny(c.Hub.AuthorName+c.Hub.AuthorEmail, "\r\n\x00") {
		return fmt.Errorf("invalid hub author identity")
	}
	for id, p := range c.Projects {
		if !idre.MatchString(id) {
			return fmt.Errorf("invalid project id %q", id)
		}
		if p.Root == "" || p.Mirror == "" || p.Remote == "" || p.DefaultBranch == "" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(p.AirelaySessionKey) {
			return fmt.Errorf("invalid project config %q", id)
		}
		if _, err := canonicalDir(p.Root); err != nil {
			return fmt.Errorf("invalid project root %q: %w", id, err)
		}
		if !filepath.IsAbs(p.Mirror) {
			return fmt.Errorf("project mirror must be absolute: %q", id)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(p.Remote) {
			return fmt.Errorf("invalid project remote: %q", id)
		}
	}
	return nil
}
