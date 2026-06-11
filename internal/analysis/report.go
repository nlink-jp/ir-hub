package analysis

import (
	"fmt"
	"strings"

	"github.com/nlink-jp/ir-hub/internal/msg"
)

// RenderMarkdown renders a (possibly translated) report as the full
// Markdown document posted as a snippet. Section headers come from
// the message catalog so the document language matches the report's.
func RenderMarkdown(rep *Report, cat *msg.Catalog) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w(cat.F(cat.RptTitle, rep.CaseID, rep.Summary.Title))
	w("")
	w("- %s / `%s` / %s: %d", rep.Channel, rep.Summary.Severity, "score", rep.Review.OverallScore)
	w("- %s", rep.GeneratedAt)
	if rep.Truncated {
		w("")
		w(cat.F(cat.RptTruncated, rep.AnalyzedMessages, rep.TotalMessages))
	}

	w("")
	w(cat.RptSummary)
	w("")
	w("%s", rep.Summary.Summary)
	if len(rep.Summary.AffectedSystems) > 0 {
		w("")
		for _, s := range rep.Summary.AffectedSystems {
			w("- `%s`", s)
		}
	}

	if len(rep.Summary.Timeline) > 0 {
		w("")
		w(cat.RptTimeline)
		w("")
		for _, e := range rep.Summary.Timeline {
			if e.Time != "" {
				w("- **%s** — %s", e.Time, e.Event)
			} else {
				w("- %s", e.Event)
			}
		}
	}

	w("")
	w(cat.RptRootCause)
	w("")
	w("%s", rep.Summary.RootCause)
	w("")
	w(cat.RptResolution)
	w("")
	w("%s", rep.Summary.Resolution)

	w("")
	w(cat.RptReview)
	w("")
	w("**%s**", cat.F(cat.RptScore, rep.Review.OverallScore))
	if len(rep.Review.Phases) > 0 {
		w("")
		w("### %s", cat.RptPhases)
		w("")
		for _, p := range rep.Review.Phases {
			w("- **%s** (%s): %s", p.Name, p.Duration, p.Assessment)
		}
	}
	w("")
	w("### %s", cat.RptCommunication)
	w("")
	w("%s", rep.Review.Communication)
	w("")
	w("### %s", cat.RptRoleClarity)
	w("")
	w("%s", rep.Review.RoleClarity)
	w("")
	w("### %s", cat.RptTools)
	w("")
	w("%s", rep.Review.ToolAppropriateness)
	writeList(&b, "### "+cat.RptStrengths, rep.Review.Strengths)
	writeList(&b, "### "+cat.RptImprovements, rep.Review.Improvements)
	writeList(&b, "### "+cat.RptChecklist, rep.Review.Checklist)

	if len(rep.Activity.Participants) > 0 {
		w("")
		w(cat.RptActivity)
		for _, p := range rep.Activity.Participants {
			w("")
			w("### %s", p.UserName)
			for _, a := range p.Actions {
				w("- [%s] %s — %s", a.Timestamp, a.Purpose, a.Findings)
				if a.Method != "" {
					w("  - `%s`", a.Method)
				}
			}
		}
	}

	if len(rep.Roles.Roles) > 0 {
		w("")
		w(cat.RptRoles)
		w("")
		for _, role := range rep.Roles.Roles {
			w("- %s — %s (%s)", role.UserName, role.InferredRole, role.Confidence)
		}
	}

	if len(rep.Tactics) > 0 {
		w("")
		w(cat.RptTactics)
		for _, t := range rep.Tactics {
			w("")
			w("### %s", t.Title)
			w("")
			w("- `%s` / %s", t.Category, t.Confidence)
			w("- %s", t.Purpose)
			if len(t.Tools) > 0 {
				w("- `%s`", strings.Join(t.Tools, "` `"))
			}
		}
	}

	return b.String()
}

func writeList(b *strings.Builder, header string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s\n\n", header)
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", it)
	}
}
