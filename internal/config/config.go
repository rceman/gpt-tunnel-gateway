package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	SchemaVersion          int                                `json:"schema_version"`
	GatewayID              string                             `json:"gateway_id"`
	ListenAddr             string                             `json:"listen_addr"`
	StateDir               string                             `json:"state_dir"`
	MaxReadBytes           int64                              `json:"max_read_bytes"`
	MaxDiffBytes           int64                              `json:"max_diff_bytes"`
	MaxListItems           int                                `json:"max_list_items"`
	DispatchTimeoutSeconds int                                `json:"dispatch_timeout_seconds"`
	RunTimeoutSeconds      int                                `json:"run_timeout_seconds"`
	AirelayCommand         string                             `json:"airelay_command"`
	Hub                    HubConfig                          `json:"hub"`
	Controller             ControllerConfig                   `json:"controller"`
	AgentBindings          map[string]AgentBinding            `json:"agent_bindings,omitempty"`
	ProjectAgentBindings   map[string]map[string]AgentBinding `json:"project_agent_bindings,omitempty"`
	Projects               map[string]ProjectConfig           `json:"projects"`
}

type HubConfig struct {
	RepositoryURL string `json:"repository_url"`
	Branch        string `json:"branch"`
	AuthorName    string `json:"author_name"`
	AuthorEmail   string `json:"author_email"`
}
type ProjectConfig struct {
	Root              string          `json:"root"`
	Mirror            string          `json:"mirror"`
	Remote            string          `json:"remote"`
	DefaultBranch     string          `json:"default_branch"`
	AirelaySessionKey string          `json:"airelay_session_key"`
	Watcher           WatcherSettings `json:"watcher,omitempty"`
}

// AgentBinding is host-local resolution for a portable Agent identity. The
// project and watcher declarations carry only agent_id; provider/session
// details stay in this generic binding map so a future Agent Registry can
// replace the map without changing project watcher contracts.
type AgentBinding struct {
	SessionKey string `json:"session_key"`
	Profile    string `json:"profile,omitempty"`
}

// ProjectAgentBindingKey is the canonical key for the legacy flat map when a
// host config is being migrated to project-scoped bindings.
func ProjectAgentBindingKey(projectID, agentID string) string {
	return projectID + "/" + agentID
}

// ResolveAgentBinding prefers the explicit project-scoped map and then the
// canonical composite key. The final flat-key fallback is retained only for
// existing host configs that have not adopted project-scoped bindings yet.
func (c Config) ResolveAgentBinding(projectID, agentID string) (AgentBinding, bool) {
	if byProject, ok := c.ProjectAgentBindings[projectID]; ok {
		binding, found := byProject[agentID]
		return binding, found
	}
	if binding, ok := c.AgentBindings[ProjectAgentBindingKey(projectID, agentID)]; ok {
		return binding, true
	}
	if binding, ok := c.AgentBindings[projectID+"::"+agentID]; ok {
		return binding, true
	}
	binding, ok := c.AgentBindings[agentID]
	return binding, ok
}

func (b AgentBinding) Validate() error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(b.SessionKey) {
		return fmt.Errorf("invalid agent binding session_key")
	}
	if b.Profile != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(b.Profile) {
		return fmt.Errorf("invalid agent binding profile")
	}
	return nil
}

// WatcherSettings contains technical supervision settings only. Behavioral
// policy is stored in the single revisioned Hub watcher guide.
type WatcherSettings struct {
	AgentID        string `json:"agent_id,omitempty"`
	Mode           string `json:"mode,omitempty"`
	CadenceSeconds int    `json:"cadence_seconds,omitempty"`
	TailLines      int    `json:"tail_lines,omitempty"`
	SeenRetention  int    `json:"seen_retention,omitempty"`
	NudgeEnabled   bool   `json:"nudge_enabled,omitempty"`
	RestartEnabled bool   `json:"restart_enabled,omitempty"`
}

func (w WatcherSettings) Effective() WatcherSettings {
	if w.Mode == "" {
		w.Mode = "disabled"
	}
	if w.CadenceSeconds == 0 {
		w.CadenceSeconds = 30
	}
	if w.TailLines == 0 {
		w.TailLines = 100
	}
	if w.SeenRetention == 0 {
		w.SeenRetention = 256
	}
	return w
}

func (w WatcherSettings) Validate() error {
	if w.AgentID != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(w.AgentID) {
		return fmt.Errorf("invalid watcher agent_id")
	}
	if w.Mode != "" && w.Mode != "disabled" && w.Mode != "observe" && w.Mode != "require" {
		return fmt.Errorf("invalid watcher mode")
	}
	effective := w.Effective()
	if effective.CadenceSeconds < 1 || effective.CadenceSeconds > 3600 || effective.TailLines < 1 || effective.TailLines > 200 || effective.SeenRetention < 1 || effective.SeenRetention > 256 {
		return fmt.Errorf("invalid watcher technical bounds")
	}
	return nil
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
