package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectConfigurationReplacesCallbackSectionWithHooks(t *testing.T) {
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	configuration.Hooks[model.HookPostAgentWorkFinished] = "notify_work_finished"
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"callbacks"`) || !strings.Contains(string(data), `"hooks"`) {
		t.Fatalf("ProjectConfiguration did not use the canonical Hook section: %s", data)
	}
}
