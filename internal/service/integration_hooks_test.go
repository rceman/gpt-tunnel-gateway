package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectConfigurationHasNoInlineIntegrationCommandAuthority(t *testing.T) {
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{`"pre"`, `"post"`, `"command"`, `"commands"`} {
		if strings.Contains(string(data), retired) {
			t.Fatalf("ProjectConfiguration retained inline integration authority %s: %s", retired, data)
		}
	}
}
