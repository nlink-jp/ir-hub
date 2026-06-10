package llm

import (
	"errors"
	"testing"

	"google.golang.org/genai"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"googleapi: Error 429: Resource exhausted", true},
		{"rpc error: code = Unavailable desc = 503", true},
		{"context deadline exceeded", true},
		{"read: connection reset by peer", true},
		{"googleapi: Error 403: permission denied", false},
		{"invalid argument: bad schema", false},
	}
	for _, tt := range tests {
		if got := isRetryable(errors.New(tt.err)); got != tt.want {
			t.Errorf("isRetryable(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestExtractResponse(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{
				{Text: "thinking...", Thought: true},
				{Text: "<think>leak</think>real answer"},
			}},
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     100,
			CandidatesTokenCount: 20,
		},
	}
	out := extractResponse(resp)
	if out.Text != "real answer" {
		t.Errorf("Text = %q, want thought parts and tags stripped", out.Text)
	}
	if out.InputTokens != 100 || out.OutputTokens != 20 {
		t.Errorf("tokens = %d/%d", out.InputTokens, out.OutputTokens)
	}
}

func TestExtractResponseEmpty(t *testing.T) {
	if out := extractResponse(nil); out.Text != "" {
		t.Errorf("nil response Text = %q", out.Text)
	}
	if out := extractResponse(&genai.GenerateContentResponse{}); out.Text != "" {
		t.Errorf("empty response Text = %q", out.Text)
	}
}
