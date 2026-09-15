package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
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
	Debug                  DebugConfig                        `json:"debug"`
	ProjectAgentBindings   map[string]map[string]AgentBinding `json:"project_agent_bindings,omitempty"`
	Projects               map[string]ProjectConfig           `json:"projects"`
}

// DebugConfig is host-local break-glass configuration. Its zero value keeps
// the recovery domain disabled for existing configurations.
type DebugConfig struct {
	Enabled bool `json:"enabled"`
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
	ProjectCode       string `json:"project_code,omitempty"`
	AirelaySessionKey string `json:"airelay_session_key"`
}

// AgentBinding is host-local resolution for a portable Agent identity.
// Provider/session details stay in this generic binding map so a future Agent
// Registry can replace the map without changing project contracts.
const (
	GTWProjectID           = "gpt-tunnel-gateway"
	GTWWorkerAgentID       = "gtw-worker"
	LegacyGTWWorkerAgentID = "gpt-review-planner"
)

type AgentBinding struct {
	SessionKey string `json:"session_key"`
	Profile    string `json:"profile,omitempty"`
}

func (c Config) ResolveAgentBinding(projectID, agentID string) (AgentBinding, bool) {
	byProject, ok := c.ProjectAgentBindings[projectID]
	if !ok {
		return AgentBinding{}, false
	}
	binding, found := byProject[agentID]
	return binding, found
}

func (c *Config) migrateGTWWorkerBinding() error {
	bindings, ok := c.ProjectAgentBindings[GTWProjectID]
	if !ok {
		return nil
	}
	legacy, foundLegacy := bindings[LegacyGTWWorkerAgentID]
	if !foundLegacy {
		return nil
	}
	if _, foundCanonical := bindings[GTWWorkerAgentID]; foundCanonical {
		return fmt.Errorf("conflicting GTW Worker Agent bindings: %q and %q", LegacyGTWWorkerAgentID, GTWWorkerAgentID)
	}
	delete(bindings, LegacyGTWWorkerAgentID)
	bindings[GTWWorkerAgentID] = legacy
	return nil
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
	if err := c.migrateGTWWorkerBinding(); err != nil {
		return Config{}, err
	}
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

// UpdateProjectCode changes only one existing host project code and returns
// the original bytes for compensation if a later authority rejects the change.
func UpdateProjectCode(path, projectID, expectedCode, projectCode string) ([]byte, error) {
	if path == "" {
		path = DefaultPath()
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Load(path)
	if err != nil {
		return nil, err
	}
	project, ok := c.Projects[projectID]
	if !ok {
		return nil, fmt.Errorf("unknown local project %q", projectID)
	}
	if project.ProjectCode != expectedCode {
		return nil, fmt.Errorf("local project code changed: expected %q, found %q", expectedCode, project.ProjectCode)
	}
	if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(projectCode) {
		return nil, fmt.Errorf("project_code must be exactly three uppercase letters")
	}
	project.ProjectCode = projectCode
	c.Projects[projectID] = project
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := fsutil.WriteJSONAtomic(path, c, 0o600); err != nil {
		return nil, err
	}
	return original, nil
}

func Restore(path string, original []byte) error {
	if path == "" {
		path = DefaultPath()
	}
	return fsutil.WriteFileAtomic(path, original, 0o600)
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
