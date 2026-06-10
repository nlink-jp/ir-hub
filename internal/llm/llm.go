// Package llm is the LLM boundary for ir-hub's analysis features:
// a minimal Client interface that the analysis pipeline consumes
// and tests fake, with a Vertex AI Gemini implementation in
// vertex.go (adapted from gem-summary's vertexai package).
package llm

import "context"

// Request is one generation call.
type Request struct {
	// System becomes the SystemInstruction. Defense preambles
	// (nonce tag, IoC safety) belong at its top.
	System string
	// User is the single user content. Untrusted data must already
	// be guard-tag wrapped by the caller.
	User string
	// JSON asks for application/json response MIME type. Output is
	// still repaired via nlk/jsonfix downstream — this just stops
	// fences and prose preambles.
	JSON bool
}

// Response is the cleaned model output.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// Client is what the analysis pipeline depends on.
type Client interface {
	Generate(ctx context.Context, req Request) (*Response, error)
}
