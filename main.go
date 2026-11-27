// Command agw-decision-guardrail is an agentgateway guardrail webhook that
// checks prompts and responses with the OpenAI Decisions API.
//
// OPENAI_API_KEY is optional when routing through agentgateway.
// Flags fall back to GUARDRAIL_<FLAG>, e.g. GUARDRAIL_REQUEST_WINDOW.
// Metrics: GET /metrics.
package main

import (
	"os"

	"github.com/thde/agw-decision-guardrail/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
