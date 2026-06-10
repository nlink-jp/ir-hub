// Package analysis runs ir-hub's LLM analyses: the five-stage
// postmortem (summary, activity, roles, tactics in parallel, then a
// process review over their structured outputs) and the on-demand
// status summary. Theory ported from ai-ir2; analysis is
// English-canonical with a separate translation pass for
// channel-facing output.
package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nlink-jp/nlk/jsonfix"

	"github.com/nlink-jp/ir-hub/internal/defang"
	"github.com/nlink-jp/ir-hub/internal/knowledge"
	"github.com/nlink-jp/ir-hub/internal/llm"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// Config carries the runner settings.
type Config struct {
	// Language is the UI language for channel-facing output ("en" |
	// "ja"). Canonical analysis stays English.
	Language string
	// BotUserID is ir-hub's own Slack user ID; its posts are
	// excluded from analysis input.
	BotUserID string
	// MaxInputTokens bounds the conversation loaded into one prompt.
	MaxInputTokens int
}

// Runner executes analyses.
type Runner struct {
	llm   llm.Client
	store *store.Store
	cfg   Config
	logf  func(format string, v ...any)
	now   func() time.Time
}

// Option configures a Runner.
type Option func(*Runner)

// WithLogger overrides the log function.
func WithLogger(logf func(format string, v ...any)) Option {
	return func(r *Runner) { r.logf = logf }
}

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option {
	return func(r *Runner) { r.now = now }
}

// NewRunner creates a Runner.
func NewRunner(c llm.Client, st *store.Store, cfg Config, opts ...Option) *Runner {
	r := &Runner{llm: c, store: st, cfg: cfg, logf: log.Printf, now: time.Now}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// runStage executes one structured-output stage: generate →
// re-defang the raw response (the model may refang IoCs) → repair
// and decode JSON → normalize.
func runStage[T any](ctx context.Context, c llm.Client, sys, user string, normalize func(*T) []string) (*T, error) {
	resp, err := c.Generate(ctx, llm.Request{System: sys, User: user, JSON: true})
	if err != nil {
		return nil, err
	}
	text, _ := defang.Text(resp.Text)
	var out T
	if err := jsonfix.ExtractTo(text, &out); err != nil {
		return nil, fmt.Errorf("decode stage output: %w (raw: %.200s)", err, text)
	}
	if normalize != nil {
		for range normalize(&out) {
			// warnings are returned for the caller's logging; the
			// pipeline logs them with stage context below.
		}
	}
	return &out, nil
}

// RunPostmortem executes the full five-stage postmortem for a case.
// Any stage failure fails the whole run (no partial reports);
// re-run with /ir-hub pm.
func (r *Runner) RunPostmortem(ctx context.Context, c *store.Case) (*Report, error) {
	in, err := r.buildInput(c)
	if err != nil {
		return nil, err
	}
	if in.Analyzed == 0 {
		return nil, fmt.Errorf("no analyzable messages in case #%d", c.ID)
	}

	var (
		wg       sync.WaitGroup
		summary  *Summary
		activity *ActivityAnalysis
		roles    *RoleAnalysis
		tactics  *tacticsResponse
		errs     = make([]error, 4)
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		summary, errs[0] = r.stageSummary(ctx, in)
	}()
	go func() {
		defer wg.Done()
		activity, errs[1] = r.stageActivity(ctx, in)
	}()
	go func() {
		defer wg.Done()
		roles, errs[2] = r.stageRoles(ctx, in)
	}()
	go func() {
		defer wg.Done()
		tactics, errs[3] = r.stageTactics(ctx, in)
	}()
	wg.Wait()
	for i, name := range []string{"summary", "activity", "roles", "tactics"} {
		if errs[i] != nil {
			return nil, fmt.Errorf("postmortem stage %s: %w", name, errs[i])
		}
	}

	review, err := r.stageReview(ctx, c, summary, activity, roles, tactics)
	if err != nil {
		return nil, fmt.Errorf("postmortem stage review: %w", err)
	}

	return &Report{
		CaseID:           c.ID,
		Channel:          "#" + c.ChannelName,
		Summary:          *summary,
		Activity:         *activity,
		Roles:            *roles,
		Tactics:          normalizeTactics(tactics, r.logf),
		Review:           *review,
		Participants:     in.Participants,
		TotalMessages:    in.Total,
		AnalyzedMessages: in.Analyzed,
		Truncated:        in.Truncated,
		GeneratedAt:      r.now().UTC().Format(time.RFC3339),
	}, nil
}

func (r *Runner) stageSummary(ctx context.Context, in *Input) (*Summary, error) {
	return runStage(ctx, r.llm,
		systemPrompt(in.Tag, summaryBody),
		userPrompt(in, "Generate a comprehensive incident summary."),
		func(s *Summary) []string {
			var warns []string
			switch s.Severity {
			case "critical", "high", "medium", "low", "unknown":
			default:
				warns = append(warns, fmt.Sprintf("severity %q normalized to unknown", s.Severity))
				s.Severity = "unknown"
			}
			if s.Title == "" {
				s.Title = "Untitled incident"
			}
			for _, w := range warns {
				r.logf("analysis: summary: %s", w)
			}
			return warns
		})
}

func (r *Runner) stageActivity(ctx context.Context, in *Input) (*ActivityAnalysis, error) {
	return runStage[ActivityAnalysis](ctx, r.llm,
		systemPrompt(in.Tag, activityBody),
		userPrompt(in, "Identify each participant's specific actions, methods, and findings."),
		nil)
}

func (r *Runner) stageRoles(ctx context.Context, in *Input) (*RoleAnalysis, error) {
	return runStage(ctx, r.llm,
		systemPrompt(in.Tag, rolesBody),
		userPrompt(in, "Infer the role of each participant and identify key relationships."),
		func(ra *RoleAnalysis) []string {
			var warns []string
			for i := range ra.Roles {
				switch ra.Roles[i].Confidence {
				case "high", "medium", "low":
				default:
					warns = append(warns, fmt.Sprintf("role confidence %q normalized to low", ra.Roles[i].Confidence))
					ra.Roles[i].Confidence = "low"
				}
			}
			for _, w := range warns {
				r.logf("analysis: roles: %s", w)
			}
			return warns
		})
}

func (r *Runner) stageTactics(ctx context.Context, in *Input) (*tacticsResponse, error) {
	return runStage[tacticsResponse](ctx, r.llm,
		systemPrompt(in.Tag, tacticsBody),
		userPrompt(in, "Extract all reusable investigation tactics demonstrated in this conversation.\n"+
			"Focus on specific methods, commands, and approaches that could help in future incidents."),
		nil)
}

// stageReview consumes ONLY the structured outputs of the prior
// stages — raw conversation text never reaches this prompt.
func (r *Runner) stageReview(ctx context.Context, c *store.Case,
	summary *Summary, activity *ActivityAnalysis, roles *RoleAnalysis, tactics *tacticsResponse) (*Review, error) {

	payload, err := json.MarshalIndent(map[string]any{
		"channel":  "#" + c.ChannelName,
		"summary":  summary,
		"activity": activity,
		"roles":    roles,
		"tactics":  tactics.Tactics,
	}, "", "  ")
	if err != nil {
		return nil, err
	}

	user := fmt.Sprintf("Evaluate the incident response process quality for channel #%s:\n\n%s\n\n"+
		"Provide a structured process quality review.", c.ChannelName, payload)

	return runStage(ctx, r.llm, reviewBody, user, func(rv *Review) []string {
		var warns []string
		if rv.OverallScore < 1 || rv.OverallScore > 10 {
			warns = append(warns, fmt.Sprintf("overall_score %d clamped", rv.OverallScore))
			if rv.OverallScore < 1 {
				rv.OverallScore = 1
			} else {
				rv.OverallScore = 10
			}
		}
		for _, w := range warns {
			r.logf("analysis: review: %s", w)
		}
		return warns
	})
}

// normalizeTactics converts raw LLM tactics into knowledge.Tactic
// with ai-ir2's fallbacks (confidence→inferred, category→other).
func normalizeTactics(resp *tacticsResponse, logf func(string, ...any)) []knowledge.Tactic {
	out := make([]knowledge.Tactic, 0, len(resp.Tactics))
	for _, raw := range resp.Tactics {
		conf := raw.Confidence
		switch conf {
		case "confirmed", "inferred", "suggested":
		default:
			logf("analysis: tactics: confidence %q normalized to inferred", conf)
			conf = "inferred"
		}
		title := raw.Title
		if title == "" {
			title = "Untitled Tactic"
		}
		category := raw.Category
		if category == "" {
			category = "other"
		}
		out = append(out, knowledge.Tactic{
			Title:        title,
			Purpose:      string(raw.Purpose),
			Category:     category,
			Tools:        raw.Tools,
			Procedure:    string(raw.Procedure),
			Observations: string(raw.Observations),
			Tags:         raw.Tags,
			Confidence:   conf,
			Evidence:     string(raw.Evidence),
		})
	}
	return out
}

// StatusSummary generates the on-demand situation report directly
// in the configured UI language (non-canonical output).
func (r *Runner) StatusSummary(ctx context.Context, c *store.Case) (string, error) {
	in, err := r.buildInput(c)
	if err != nil {
		return "", err
	}
	if in.Analyzed == 0 {
		return "", fmt.Errorf("no analyzable messages in case #%d", c.ID)
	}
	resp, err := r.llm.Generate(ctx, llm.Request{
		System: systemPrompt(in.Tag, statusBody(r.cfg.Language)),
		User:   userPrompt(in, "Produce the current situation report."),
	})
	if err != nil {
		return "", err
	}
	text, _ := defang.Text(resp.Text)
	return text, nil
}
