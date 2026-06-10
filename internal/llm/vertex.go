package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nlink-jp/nlk/backoff"
	"github.com/nlink-jp/nlk/strip"
	"google.golang.org/genai"
)

// maxRetries caps retry attempts on transient failures (429 / 5xx /
// transport); anything else surfaces immediately.
const maxRetries = 5

// Vertex implements Client on Vertex AI Gemini (ADC auth).
type Vertex struct {
	inner   *genai.Client
	model   string
	timeout time.Duration
}

var _ Client = (*Vertex)(nil)

// NewVertex creates the Vertex AI client. timeoutSec caps each
// GenerateContent call; 0 keeps the SDK default.
func NewVertex(ctx context.Context, project, location, model string, timeoutSec int) (*Vertex, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: location,
	})
	if err != nil {
		return nil, fmt.Errorf("vertex AI client: %w", err)
	}
	v := &Vertex{inner: client, model: model}
	if timeoutSec > 0 {
		v.timeout = time.Duration(timeoutSec) * time.Second
	}
	return v, nil
}

// Model returns the configured model name (startup logging).
func (v *Vertex) Model() string { return v.model }

// Generate sends one prompt with retry on transient failures.
func (v *Vertex) Generate(ctx context.Context, req Request) (*Response, error) {
	if v.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, v.timeout)
		defer cancel()
	}

	contents := []*genai.Content{genai.NewContentFromText(req.User, "user")}
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(req.System, "system"),
		// Gemini 2.5 Flash sometimes leaks a THOUGHT preamble in a
		// non-Thought part despite this flag; extractResponse runs
		// strip.ThinkTags as the defence-in-depth pass.
		ThinkingConfig: &genai.ThinkingConfig{IncludeThoughts: false},
	}
	if req.JSON {
		cfg.ResponseMIMEType = "application/json"
	}

	bo := backoff.New(
		backoff.WithBase(2*time.Second),
		backoff.WithMax(30*time.Second),
	)
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := v.inner.Models.GenerateContent(ctx, v.model, contents, cfg)
		if err == nil {
			return extractResponse(resp), nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxRetries {
			return nil, fmt.Errorf("vertex AI generate: %w", err)
		}
		wait := bo.Duration(attempt)
		log.Printf("llm: vertex AI call failed (attempt %d/%d), retrying in %v: %v",
			attempt+1, maxRetries+1, wait.Round(time.Second), err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, fmt.Errorf("vertex AI generate cancelled: %w", ctx.Err())
		}
	}
	return nil, fmt.Errorf("vertex AI generate failed after %d retries: %w", maxRetries, lastErr)
}

// extractResponse filters Thought parts structurally and strips
// leaked think-tags from the joined text.
func extractResponse(resp *genai.GenerateContentResponse) *Response {
	out := &Response{}
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return out
	}
	var parts []string
	for _, p := range resp.Candidates[0].Content.Parts {
		if p.Thought {
			continue
		}
		if p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	out.Text = strip.ThinkTags(strings.Join(parts, ""))
	if resp.UsageMetadata != nil {
		out.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
		out.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}
	return out
}

// isRetryable is conservative: only well-known transient failure
// strings retry; auth/schema errors surface immediately.
func isRetryable(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"429", "503", "500", "502",
		"unavailable", "deadline", "timeout",
		"connection refused", "eof", "reset by peer",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
