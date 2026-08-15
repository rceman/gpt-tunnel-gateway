package config

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
)

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
	for agentID, binding := range c.AgentBindings {
		if !validAgentBindingMapKey(agentID) {
			return fmt.Errorf("invalid agent binding id %q", agentID)
		}
		if err := binding.Validate(); err != nil {
			return fmt.Errorf("invalid agent binding %q: %w", agentID, err)
		}
	}
	for projectID, bindings := range c.ProjectAgentBindings {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`).MatchString(projectID) {
			return fmt.Errorf("invalid project agent binding project id %q", projectID)
		}
		for agentID, binding := range bindings {
			if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(agentID) {
				return fmt.Errorf("invalid project agent binding id %q", agentID)
			}
			if err := binding.Validate(); err != nil {
				return fmt.Errorf("invalid project agent binding %q/%q: %w", projectID, agentID, err)
			}
		}
	}
	for id, p := range c.Projects {
		if !idre.MatchString(id) {
			return fmt.Errorf("invalid project id %q", id)
		}
		if p.Root == "" || p.Mirror == "" || p.Remote == "" || p.DefaultBranch == "" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(p.AirelaySessionKey) {
			return fmt.Errorf("invalid project config %q", id)
		}
		if p.ProjectCode != "" && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(p.ProjectCode) {
			return fmt.Errorf("invalid project code %q", id)
		}
		if err := p.Watcher.Validate(); err != nil {
			return fmt.Errorf("invalid project watcher config %q: %w", id, err)
		}
		if p.Watcher.AgentID != "" {
			if _, ok := c.AgentBindings[p.Watcher.AgentID]; !ok {
				return fmt.Errorf("project %q references unbound watcher agent %q", id, p.Watcher.AgentID)
			}
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

func validAgentBindingMapKey(value string) bool {
	if regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(value) {
		return true
	}
	for _, separator := range []string{"/", "::"} {
		parts := strings.Split(value, separator)
		if len(parts) == 2 && regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`).MatchString(parts[0]) && regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(parts[1]) {
			return true
		}
	}
	return false
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
