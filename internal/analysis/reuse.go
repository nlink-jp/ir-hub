package analysis

import (
	"context"
	"fmt"
	"strings"

	"github.com/nlink-jp/nlk/guard"

	"github.com/nlink-jp/ir-hub/internal/defang"
	"github.com/nlink-jp/ir-hub/internal/llm"
	"github.com/nlink-jp/ir-hub/internal/sanitize"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// KnowledgeSummary is the lightweight projection the briefing
// consumes — it decouples Briefing from store.KnowledgeDoc.
type KnowledgeSummary struct {
	TacticID string
	Title    string
	Category string
	Summary  string
}

// NoBriefing is the sentinel the briefing model returns when no
// past knowledge is relevant; the caller suppresses the post.
const NoBriefing = "NONE"

// Answer responds to an @-mention question using the narrowed
// knowledge documents and, when c != nil, a budget-limited slice of
// the current case conversation. The question (user input) and the
// knowledge docs (LLM-generated from user content) are both
// nonce-wrapped; the defense preamble sits first. Output is
// defanged in case the model refangs an IoC.
func (r *Runner) Answer(ctx context.Context, c *store.Case, question string, docs []store.KnowledgeDoc) (string, error) {
	tag := guard.NewTag()

	q, _ := defang.Text(strings.TrimSpace(question))
	for _, w := range sanitize.Detect(q) {
		r.logf("analysis: [SECURITY] injection pattern in Q&A question: %s", w)
	}
	wrappedQ, err := tag.Wrap(q)
	if err != nil {
		return "", fmt.Errorf("wrap question: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("KNOWLEDGE documents:\n\n")
	if len(docs) == 0 {
		sb.WriteString("(no matching knowledge documents)\n")
	}
	budget := r.cfg.MaxInputTokens - promptOverheadTokens
	used := 0
	for _, d := range docs {
		doc, _ := defang.Text(d.DocMD)
		wrapped, werr := tag.Wrap(doc)
		if werr != nil {
			r.logf("analysis: [SECURITY] guard collision in knowledge %s, skipped", d.TacticID)
			continue
		}
		t := EstimateTokens(wrapped)
		if used+t > budget && used > 0 {
			break // keep within budget; earlier (lower tactic_id) docs win
		}
		used += t
		fmt.Fprintf(&sb, "Document %s:\n%s\n\n", d.TacticID, wrapped)
	}

	if c != nil {
		if in, ierr := r.buildInput(c); ierr == nil && in.Analyzed > 0 {
			sb.WriteString("CASE CONTEXT (current channel conversation):\n\n")
			// in.Conversation is already defanged + wrapped with its
			// own tag; the defense preamble covers any nonce tag.
			sb.WriteString(in.Conversation)
			sb.WriteString("\n")
		}
	}

	fmt.Fprintf(&sb, "QUESTION:\n%s\n", wrappedQ)

	resp, err := r.llm.Generate(ctx, llm.Request{
		System: systemPrompt(tag, answerBody(r.cfg.Language)),
		User:   sb.String(),
	})
	if err != nil {
		return "", err
	}
	text, _ := defang.Text(resp.Text)
	return text, nil
}

// Briefing selects relevant past knowledge for a newly opened case
// and produces a short channel-facing briefing, or returns
// NoBriefing when nothing is relevant. The caller is responsible
// for narrowing summaries to fit the budget before calling (see the
// bot's runBriefing); this method is a single LLM call.
func (r *Runner) Briefing(ctx context.Context, title, severity string, summaries []KnowledgeSummary) (string, error) {
	if len(summaries) == 0 {
		return NoBriefing, nil
	}
	tag := guard.NewTag()

	dTitle, _ := defang.Text(title)
	var sb strings.Builder
	sb.WriteString("KNOWLEDGE summaries (past tactics):\n\n")
	for _, s := range summaries {
		line := fmt.Sprintf("%s [%s] %s: %s", s.TacticID, s.Category, s.Title, s.Summary)
		ds, _ := defang.Text(line)
		wrapped, werr := tag.Wrap(ds)
		if werr != nil {
			continue
		}
		sb.WriteString(wrapped)
		sb.WriteString("\n")
	}

	resp, err := r.llm.Generate(ctx, llm.Request{
		System: systemPrompt(tag, briefingBody(r.cfg.Language, dTitle, severity)),
		User:   sb.String(),
	})
	if err != nil {
		return "", err
	}
	text, _ := defang.Text(strings.TrimSpace(resp.Text))
	return text, nil
}