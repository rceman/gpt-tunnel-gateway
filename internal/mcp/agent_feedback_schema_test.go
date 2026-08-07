package mcp

import "testing"

func TestRunReportOutputSchemaIncludesOptionalClosedAgentFeedback(t *testing.T) {
	schema := reportOutputSchema()
	properties := schema["properties"].(map[string]any)
	feedback, ok := properties["agent_feedback"].(map[string]any)
	if !ok {
		t.Fatal("run_report output schema omits agent_feedback")
	}
	if feedback["additionalProperties"] != false {
		t.Fatal("agent_feedback output schema is not closed")
	}
	required := feedback["required"].([]string)
	if len(required) != 4 {
		t.Fatalf("agent_feedback required fields = %#v", required)
	}
	for _, item := range required {
		if item == "summary" {
			t.Fatal("agent_feedback summary remains required")
		}
	}
	feedbackProperties := feedback["properties"].(map[string]any)
	candidates := feedbackProperties["tool_candidates"].(map[string]any)
	items := candidates["items"].(map[string]any)
	if items["additionalProperties"] != false {
		t.Fatal("tool candidate output schema is not closed")
	}
	for _, item := range schema["required"].([]string) {
		if item == "agent_feedback" {
			t.Fatal("agent_feedback must remain optional on run_report")
		}
	}
}
