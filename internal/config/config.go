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
	RepositoryURL string `json:"repository_url"`
	Branch        string `json:"branch"`
	AuthorName    string `json:"author_name"`
	AuthorEmail   string `json:"author_email"`
}
type ProjectConfig struct {
	Root              string `json:"root"`
	Mirror            string `json:"mirror"`
	Remote            string `json:"remote"`
	DefaultBranch     string `json:"default_branch"`
	AirelaySessionKey string `json:"airelay_session_key"`
}
type ControllerConfig struct {
	GatewayBinary          string `json:"gateway_binary"`
	TunnelClientBinary     string `json:"tunnel_client_binary"`
	TunnelEnvFile          string `json:"tunnel_env_file"`
	PIDDir                 string `json:"pid_dir"`
	LogDir                 string `json:"log_dir"`
	TunnelHealthListenAddr string `json:"tunnel_health_listen_addr"`
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
	c.StateDir = filepath.Clean(c.StateDir)
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
	if err := validateLoopbackAddress("listen_addr", c.ListenAddr); err != nil {
		return err
	}
	if c.StateDir == "" || !filepath.IsAbs(c.StateDir) || c.MaxReadBytes < 1 || c.MaxReadBytes > 64<<20 || c.MaxDiffBytes < 1 || c.MaxDiffBytes > 64<<20 || c.MaxListItems < 1 || c.MaxListItems > 10000 {
		return fmt.Errorf("invalid limits or state_dir")
	}
	if c.DispatchTimeoutSeconds < 1 || c.DispatchTimeoutSeconds > 300 || c.RunTimeoutSeconds < 60 || c.RunTimeoutSeconds > 86400 {
		return fmt.Errorf("invalid timeouts")
	}
	if c.AirelayCommand == "" || strings.ContainsRune(c.AirelayCommand, 0) {
		return fmt.Errorf("invalid airelay command")
	}
	if err := validateRepositoryURL(c.Hub.RepositoryURL); err != nil {
		return err
	}
	if err := validateBranch(c.Hub.Branch); err != nil {
		return fmt.Errorf("invalid hub branch: %w", err)
	}
	if c.Hub.AuthorName == "" || c.Hub.AuthorEmail == "" || strings.ContainsAny(c.Hub.AuthorName+c.Hub.AuthorEmail, "\r\n\x00") {
		return fmt.Errorf("invalid hub author identity")
	}
	if err := validateLoopbackAddress("controller.tunnel_health_listen_addr", c.Controller.TunnelHealthListenAddr); err != nil {
		return err
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

func validateRepositoryURL(value string) error {
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("invalid hub repository_url")
	}
	if !filepath.IsAbs(value) && !strings.Contains(value, ":") {
		return fmt.Errorf("hub repository_url must be an absolute path or Git URL")
	}
	return nil
}

func validateBranch(value string) error {
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "\x00\r\n ~^:?*[\\") || strings.HasPrefix(value, "-") || strings.Contains(value, "..") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("invalid branch")
	}
	return nil
}

func validateLoopbackAddress(name, value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("invalid %s", name)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%s must be loopback", name)
	}
	n, err := net.LookupPort("tcp", port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid %s port", name)
	}
	return nil
}
