package model

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	ProjectCallbackWorkFinishedEvent  = "agent.work_finished"
	MaxProjectCallbacks               = 32
	MaxProjectCallbackKeyBytes        = 128
	MaxProjectCallbackEventBytes      = 64
	MaxProjectCallbackURLBytes        = 2048
	MaxProjectCallbackBodyBytes       = 16 << 10
	MaxProjectCallbackScriptArgs      = 16
	MaxProjectCallbackArgBytes        = 256
	MaxProjectCallbackScriptPathBytes = 4096
)

// ProjectCallbackURL is an exact, server-owned HTTP delivery definition. The
// body is intentionally opaque and is sent without adding credentials or
// provider-specific metadata.
type ProjectCallbackURL struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

// ProjectCallbackScript is an argv-only delivery definition. Path resolution
// and execution are kept outside the model in the callback delivery adapter.
type ProjectCallbackScript struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

// ProjectCallback is a project-scoped callback registration. Callback keys
// are unique within one project configuration.
type ProjectCallback struct {
	Callback string                 `json:"callback"`
	Event    string                 `json:"event"`
	URL      *ProjectCallbackURL    `json:"url,omitempty"`
	Script   *ProjectCallbackScript `json:"script,omitempty"`
}

func ValidateProjectCallback(v ProjectCallback) error {
	if v.Callback == "" || len([]byte(v.Callback)) > MaxProjectCallbackKeyBytes || ValidateObjectIdentifier(v.Callback) != nil {
		return fmt.Errorf("invalid callback key")
	}
	if v.Event == "" || len([]byte(v.Event)) > MaxProjectCallbackEventBytes || v.Event != ProjectCallbackWorkFinishedEvent {
		return fmt.Errorf("unsupported callback event")
	}
	if v.URL == nil && v.Script == nil {
		return fmt.Errorf("callback must configure a URL or script")
	}
	if v.URL != nil {
		if err := validateProjectCallbackURL(*v.URL); err != nil {
			return err
		}
	}
	if v.Script != nil {
		if err := validateProjectCallbackScript(*v.Script); err != nil {
			return err
		}
	}
	return nil
}

func validateProjectCallbackURL(v ProjectCallbackURL) error {
	if v.Method != "POST" && v.Method != "PUT" {
		return fmt.Errorf("callback URL method must be POST or PUT")
	}
	if len([]byte(v.URL)) == 0 || len([]byte(v.URL)) > MaxProjectCallbackURLBytes || !utf8.ValidString(v.URL) || strings.ContainsRune(v.URL, '\x00') {
		return fmt.Errorf("invalid callback URL")
	}
	parsed, err := url.Parse(v.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("callback URL must be an absolute HTTP or HTTPS URL without credentials")
	}
	if len([]byte(v.Body)) > MaxProjectCallbackBodyBytes || !utf8.ValidString(v.Body) || strings.ContainsRune(v.Body, '\x00') {
		return fmt.Errorf("callback URL body exceeds bounds or is invalid UTF-8")
	}
	return nil
}

func validateProjectCallbackScript(v ProjectCallbackScript) error {
	if len([]byte(v.Path)) == 0 || len([]byte(v.Path)) > MaxProjectCallbackScriptPathBytes {
		return fmt.Errorf("invalid callback script path")
	}
	if err := ValidateRelativePath(v.Path); err != nil {
		return fmt.Errorf("invalid callback script path: %w", err)
	}
	if len(v.Args) > MaxProjectCallbackScriptArgs {
		return fmt.Errorf("callback script has too many arguments")
	}
	for _, arg := range v.Args {
		if len([]byte(arg)) > MaxProjectCallbackArgBytes || !utf8.ValidString(arg) || strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("invalid callback script argument")
		}
	}
	return nil
}

func ValidateProjectCallbacks(values []ProjectCallback) error {
	if len(values) > MaxProjectCallbacks {
		return fmt.Errorf("project callback registry exceeds %d entries", MaxProjectCallbacks)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := ValidateProjectCallback(value); err != nil {
			return err
		}
		if _, exists := seen[value.Callback]; exists {
			return fmt.Errorf("duplicate callback key %q", value.Callback)
		}
		seen[value.Callback] = struct{}{}
	}
	return nil
}
