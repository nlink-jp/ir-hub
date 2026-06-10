package analysis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nlink-jp/nlk/jsonfix"

	"github.com/nlink-jp/ir-hub/internal/llm"
)

// Translation theory ported from ai-ir2 translate/translator.py,
// with structural protection instead of prompt-only rules: only
// narrative fields are collected into the translation payload, so
// commands, IoCs, enums, category slugs, IDs, and dates can never
// be mangled — they are simply not sent.
//
// Failure semantics: any error (call failure, broken JSON) keeps
// the English original — channel posts degrade to English rather
// than blocking the postmortem.

const translateSystem = `You are a professional technical translator for incident-response material.
Translate the JSON string values below into %s.

Rules:
- Translate ONLY the string values. Keys must stay exactly as-is.
- Do NOT translate anything that looks like a shell command, code, an IP address,
  a domain, a URL, a file hash, a defanged indicator (hxxp://, evil[.]com), a
  user ID (U0123...), or a tool name — reproduce those substrings unchanged
  inside the translated sentence.
- Preserve all whitespace and newlines within values.
- Return a single valid JSON object with exactly the same keys as the input.`

func languageName(lang string) string {
	if lang == "ja" {
		return "Japanese"
	}
	return "English"
}

// Translate returns a copy of the report with narrative fields
// translated to the configured language. With Language "en" (or any
// failure) the canonical report is returned unchanged.
func (r *Runner) Translate(ctx context.Context, rep *Report) *Report {
	if r.cfg.Language == "en" || rep == nil {
		return rep
	}

	// Deep copy via JSON round-trip; the copy receives translations.
	buf, err := json.Marshal(rep)
	if err != nil {
		r.logf("analysis: translate: marshal: %v", err)
		return rep
	}
	var out Report
	if err := json.Unmarshal(buf, &out); err != nil {
		r.logf("analysis: translate: unmarshal: %v", err)
		return rep
	}

	payload := collectNarrative(&out)
	if len(payload) == 0 {
		return &out
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		r.logf("analysis: translate: payload: %v", err)
		return rep
	}

	resp, err := r.llm.Generate(ctx, llm.Request{
		System: fmt.Sprintf(translateSystem, languageName(r.cfg.Language)),
		User:   string(body),
		JSON:   true,
	})
	if err != nil {
		r.logf("analysis: translate failed, keeping English: %v", err)
		return rep
	}
	translated := map[string]string{}
	if err := jsonfix.ExtractTo(resp.Text, &translated); err != nil {
		r.logf("analysis: translate: bad response, keeping English: %v", err)
		return rep
	}

	// Field-level fallback: keys the model dropped stay English.
	applyNarrative(&out, translated)
	return &out
}

// collectNarrative gathers the translatable fields keyed by stable
// dotted paths. applyNarrative must mirror this exactly.
func collectNarrative(rep *Report) map[string]string {
	m := map[string]string{}
	put := func(key, val string) {
		if val != "" {
			m[key] = val
		}
	}

	put("summary.title", rep.Summary.Title)
	put("summary.root_cause", string(rep.Summary.RootCause))
	put("summary.resolution", string(rep.Summary.Resolution))
	put("summary.summary", string(rep.Summary.Summary))
	for i := range rep.Summary.Timeline {
		put(fmt.Sprintf("timeline.%d.event", i), rep.Summary.Timeline[i].Event)
	}
	for i, p := range rep.Activity.Participants {
		for j, a := range p.Actions {
			put(fmt.Sprintf("activity.%d.%d.purpose", i, j), string(a.Purpose))
			put(fmt.Sprintf("activity.%d.%d.findings", i, j), string(a.Findings))
		}
	}
	for i, role := range rep.Roles.Roles {
		put(fmt.Sprintf("roles.%d.role", i), role.InferredRole)
		for j, ev := range role.Evidence {
			put(fmt.Sprintf("roles.%d.evidence.%d", i, j), ev)
		}
	}
	put("review.communication", string(rep.Review.Communication))
	put("review.role_clarity", string(rep.Review.RoleClarity))
	put("review.tools", string(rep.Review.ToolAppropriateness))
	for i, ph := range rep.Review.Phases {
		put(fmt.Sprintf("review.phase.%d.name", i), ph.Name)
		put(fmt.Sprintf("review.phase.%d.assessment", i), string(ph.Assessment))
	}
	for i, s := range rep.Review.Strengths {
		put(fmt.Sprintf("review.strength.%d", i), s)
	}
	for i, s := range rep.Review.Improvements {
		put(fmt.Sprintf("review.improvement.%d", i), s)
	}
	for i, s := range rep.Review.Checklist {
		put(fmt.Sprintf("review.checklist.%d", i), s)
	}
	for i, t := range rep.Tactics {
		put(fmt.Sprintf("tactic.%d.title", i), t.Title)
		put(fmt.Sprintf("tactic.%d.purpose", i), t.Purpose)
	}
	return m
}

func applyNarrative(rep *Report, tr map[string]string) {
	get := func(key string, dst *string) {
		if v, ok := tr[key]; ok && v != "" {
			*dst = v
		}
	}
	getText := func(key string, dst *Text) {
		if v, ok := tr[key]; ok && v != "" {
			*dst = Text(v)
		}
	}

	get("summary.title", &rep.Summary.Title)
	getText("summary.root_cause", &rep.Summary.RootCause)
	getText("summary.resolution", &rep.Summary.Resolution)
	getText("summary.summary", &rep.Summary.Summary)
	for i := range rep.Summary.Timeline {
		get(fmt.Sprintf("timeline.%d.event", i), &rep.Summary.Timeline[i].Event)
	}
	for i := range rep.Activity.Participants {
		for j := range rep.Activity.Participants[i].Actions {
			a := &rep.Activity.Participants[i].Actions[j]
			getText(fmt.Sprintf("activity.%d.%d.purpose", i, j), &a.Purpose)
			getText(fmt.Sprintf("activity.%d.%d.findings", i, j), &a.Findings)
		}
	}
	for i := range rep.Roles.Roles {
		get(fmt.Sprintf("roles.%d.role", i), &rep.Roles.Roles[i].InferredRole)
		for j := range rep.Roles.Roles[i].Evidence {
			get(fmt.Sprintf("roles.%d.evidence.%d", i, j), &rep.Roles.Roles[i].Evidence[j])
		}
	}
	getText("review.communication", &rep.Review.Communication)
	getText("review.role_clarity", &rep.Review.RoleClarity)
	getText("review.tools", &rep.Review.ToolAppropriateness)
	for i := range rep.Review.Phases {
		get(fmt.Sprintf("review.phase.%d.name", i), &rep.Review.Phases[i].Name)
		getText(fmt.Sprintf("review.phase.%d.assessment", i), &rep.Review.Phases[i].Assessment)
	}
	for i := range rep.Review.Strengths {
		get(fmt.Sprintf("review.strength.%d", i), &rep.Review.Strengths[i])
	}
	for i := range rep.Review.Improvements {
		get(fmt.Sprintf("review.improvement.%d", i), &rep.Review.Improvements[i])
	}
	for i := range rep.Review.Checklist {
		get(fmt.Sprintf("review.checklist.%d", i), &rep.Review.Checklist[i])
	}
	for i := range rep.Tactics {
		get(fmt.Sprintf("tactic.%d.title", i), &rep.Tactics[i].Title)
		get(fmt.Sprintf("tactic.%d.purpose", i), &rep.Tactics[i].Purpose)
	}
}
