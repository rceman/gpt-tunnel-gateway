package model

import "testing"

func TestProjectCallbackValidation(t *testing.T) {
	valid := ProjectCallback{Callback: "notify", Event: ProjectCallbackWorkFinishedEvent, URL: &ProjectCallbackURL{Method: "POST", URL: "https://example.invalid/hook", Body: "{}"}}
	if err := ValidateProjectCallback(valid); err != nil {
		t.Fatal(err)
	}
	valid.Script = &ProjectCallbackScript{Path: "scripts/work-finished", Args: []string{"--bounded"}}
	if err := ValidateProjectCallback(valid); err != nil {
		t.Fatal(err)
	}
	for name, callback := range map[string]ProjectCallback{
		"no target":       {Callback: "notify", Event: ProjectCallbackWorkFinishedEvent},
		"bad event":       {Callback: "notify", Event: "agent.idle", URL: valid.URL},
		"bad URL":         {Callback: "notify", Event: ProjectCallbackWorkFinishedEvent, URL: &ProjectCallbackURL{Method: "POST", URL: "file:///tmp/hook"}},
		"escaping script": {Callback: "notify", Event: ProjectCallbackWorkFinishedEvent, Script: &ProjectCallbackScript{Path: "../hook"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateProjectCallback(callback); err == nil {
				t.Fatal("invalid callback was accepted")
			}
		})
	}
}
