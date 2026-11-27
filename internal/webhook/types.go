// Package webhook serves agentgateway's LLM guardrail webhook protocol.
//
// Wire types mirror crates/agentgateway/src/llm/policy/webhook.rs.
// Agentgateway publishes no Go types for them.
package webhook

// Message is a text message from a prompt or an LLM response.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type promptRequest struct {
	Body struct {
		Messages []Message `json:"messages"`
	} `json:"body"`
}

type responseRequest struct {
	Body struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	} `json:"body"`
}

type response struct {
	Action any `json:"action"`
}

// Agentgateway decodes the action as an untagged enum and only accepts
// {reason} as a pass, so the shapes stay distinct types.
type passAction struct {
	Reason string `json:"reason,omitempty"`
}

type rejectAction struct {
	Body       string `json:"body"`
	StatusCode int    `json:"status_code"`
	Reason     string `json:"reason,omitempty"`
}
