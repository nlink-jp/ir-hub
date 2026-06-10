// Package knowledge renders extracted tactics into the canonical
// knowledge-document pair: structured JSON for machines and
// Markdown for humans (RFP: JSON + Markdown, consumed by teams
// outside IR via the storage export in Phase 3).
//
// Document structure ported from ai-ir2's knowledge/formatter.py,
// with JSON replacing YAML per the ir-hub RFP. Documents are
// English-canonical.
package knowledge

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Tactic is one reusable investigation method extracted by the
// analysis pipeline. Confidence is one of confirmed | inferred |
// suggested (ai-ir2 calibration: confirmed = output shared in
// channel, inferred = stated but no output, suggested = recommended
// only).
type Tactic struct {
	Title        string   `json:"title"`
	Purpose      string   `json:"purpose"`
	Category     string   `json:"category"`
	Tools        []string `json:"tools"`
	Procedure    string   `json:"procedure"`
	Observations string   `json:"observations"`
	Tags         []string `json:"tags"`
	Confidence   string   `json:"confidence"`
	Evidence     string   `json:"evidence"`
}

// Source records where a tactic was observed.
type Source struct {
	Channel      string   `json:"channel"`
	Participants []string `json:"participants"`
}

// docJSON is the canonical JSON document shape.
type docJSON struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Confidence   string   `json:"confidence"`
	Evidence     string   `json:"evidence"`
	Purpose      string   `json:"purpose"`
	Category     string   `json:"category"`
	Tools        []string `json:"tools"`
	Procedure    string   `json:"procedure"`
	Observations string   `json:"observations"`
	Tags         []string `json:"tags"`
	Source       Source   `json:"source"`
	CreatedAt    string   `json:"created_at"`
}

// Doc is the rendered pair plus index metadata.
type Doc struct {
	TacticID string
	Tactic   Tactic
	Summary  string // index line: first sentence of purpose
	JSON     string
	Markdown string
}

// Build renders the JSON + Markdown pair for a tactic. tacticID is
// assigned by the store (tac-YYYYMMDD-NNN); channel/participants
// become the source block.
func Build(t Tactic, tacticID, channel string, participants []string, createdAt time.Time) (Doc, error) {
	if t.Tools == nil {
		t.Tools = []string{}
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	if participants == nil {
		participants = []string{}
	}
	j, err := json.MarshalIndent(docJSON{
		ID:           tacticID,
		Title:        t.Title,
		Confidence:   t.Confidence,
		Evidence:     t.Evidence,
		Purpose:      t.Purpose,
		Category:     t.Category,
		Tools:        t.Tools,
		Procedure:    t.Procedure,
		Observations: t.Observations,
		Tags:         t.Tags,
		Source:       Source{Channel: channel, Participants: participants},
		CreatedAt:    createdAt.UTC().Format("2006-01-02"),
	}, "", "  ")
	if err != nil {
		return Doc{}, fmt.Errorf("knowledge: marshal %s: %w", tacticID, err)
	}

	return Doc{
		TacticID: tacticID,
		Tactic:   t,
		Summary:  firstSentence(t.Purpose),
		JSON:     string(j),
		Markdown: markdown(t, tacticID, channel, participants, createdAt),
	}, nil
}

// markdown renders the human/RAG-friendly form: every section
// self-contained, tool names in backticks (ai-ir2 formatter rules).
func markdown(t Tactic, tacticID, channel string, participants []string, createdAt time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", t.Title)
	fmt.Fprintf(&b, "- **ID**: `%s`\n", tacticID)
	fmt.Fprintf(&b, "- **Category**: `%s`\n", t.Category)
	fmt.Fprintf(&b, "- **Confidence**: %s\n", t.Confidence)
	if len(t.Tags) > 0 {
		fmt.Fprintf(&b, "- **Tags**: %s\n", strings.Join(t.Tags, ", "))
	}
	fmt.Fprintf(&b, "- **Source**: %s", channel)
	if len(participants) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(participants, ", "))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- **Created**: %s\n", createdAt.UTC().Format("2006-01-02"))

	fmt.Fprintf(&b, "\n## Purpose\n\n%s\n", t.Purpose)
	if len(t.Tools) > 0 {
		b.WriteString("\n## Tools\n\n")
		for _, tool := range t.Tools {
			fmt.Fprintf(&b, "- `%s`\n", tool)
		}
	}
	if t.Procedure != "" {
		fmt.Fprintf(&b, "\n## Procedure\n\n%s\n", t.Procedure)
	}
	if t.Observations != "" {
		fmt.Fprintf(&b, "\n## Observations\n\n%s\n", t.Observations)
	}
	if t.Evidence != "" {
		fmt.Fprintf(&b, "\n## Evidence\n\n%s\n", t.Evidence)
	}
	return b.String()
}

// firstSentence extracts the index summary from a purpose text:
// everything up to and including the earliest sentence terminator
// (". " or "。"), or the whole text when none is found.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	end := -1
	if i := strings.Index(s, ". "); i >= 0 {
		end = i + 1 // keep the period, drop the space
	}
	if i := strings.Index(s, "。"); i >= 0 {
		if j := i + len("。"); end == -1 || j < end {
			end = j
		}
	}
	if end == -1 {
		return s
	}
	return s[:end]
}

// Slug derives the export-filename fragment from a title:
// lowercase, ASCII alphanumerics and hyphens only, runs collapsed,
// truncated to 30 chars without a trailing hyphen (ai-ir2 rule —
// filenames stay ASCII even though channel names may not).
func Slug(title string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(title) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 30 {
		s = strings.TrimRight(s[:30], "-")
	}
	return s
}
