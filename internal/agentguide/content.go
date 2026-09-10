package agentguide

// Content is the canonical zero-state supervision guide shared by CLI and MCP.
type Content struct {
	Architecture    string `json:"architecture"`
	Tail            string `json:"tail"`
	StatusAwait     string `json:"status_await"`
	PromptInterrupt string `json:"prompt_interrupt"`
	Authority       string `json:"authority"`
}

func Canonical() Content {
	return Content{
		Architecture:    "The Planner role may have multiple durable Planner sessions; the project has exactly one attached enabled coding Agent. Train and watcher state are not Agent supervision authority.",
		Tail:            "agent/tail accepts {session?,lines?}. Omitted session selects the unique active durable Agent session; zero is an error and more than one requires explicit SA-*. Examples: {} or {lines:30}; explicit: {session:\"SA-GTW-AB12\",lines:30}. The selected durable session resolves internally to its stored Airelay ref.",
		StatusAwait:     "agent/status and agent/await accept an optional logical Agent selector; when omitted, they use the server-selected attached coding Agent. They do not accept a durable SA-* selector.",
		PromptInterrupt: "agent/prompt and agent/interrupt accept an optional logical Agent selector; when omitted, they use the server-selected attached coding Agent. Prompt sends a bounded message; interrupt cancels the current turn and may submit a bounded replacement. Neither accepts a durable SA-* selector.",
		Authority:       "Gateway schemas and handlers are the operational contract. No repo guide file, Train record, watcher, project Airelay fallback, or duplicate policy source is authoritative.",
	}
}
