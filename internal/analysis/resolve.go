package analysis

import (
	"context"
	"regexp"
	"strings"
)

// userIDPattern matches a bare Slack user ID (optionally seen with a
// leading "@" in LLM output). Used to decide which user_name-like
// fields to resolve to display names — names the model wrote in
// prose are left untouched.
var userIDPattern = regexp.MustCompile(`^U[A-Z0-9]+$`)

// resolveReport rewrites the user-ID-bearing fields of a finished
// report to "display (ID)" form. It runs after the LLM (the model
// sees raw IDs; translation never touches these fields), so names
// never leak into prompts. A nil resolver leaves IDs unchanged.
func (r *Runner) resolveReport(ctx context.Context, rep *Report) {
	if r.resolver == nil {
		return
	}
	rep.Participants = r.resolver.ResolveAll(ctx, rep.Participants)
	for i := range rep.Activity.Participants {
		rep.Activity.Participants[i].UserName = r.resolveUserField(ctx, rep.Activity.Participants[i].UserName)
	}
	for i := range rep.Roles.Roles {
		rep.Roles.Roles[i].UserName = r.resolveUserField(ctx, rep.Roles.Roles[i].UserName)
	}
	for i := range rep.Roles.Relationships {
		rep.Roles.Relationships[i].From = Text(r.resolveUserField(ctx, string(rep.Roles.Relationships[i].From)))
		rep.Roles.Relationships[i].To = Text(r.resolveUserField(ctx, string(rep.Roles.Relationships[i].To)))
	}
}

// resolveUserField resolves s to a display name when it is a Slack
// user ID (with or without a leading "@"); otherwise it returns s
// unchanged (the model may have written a prose name).
func (r *Runner) resolveUserField(ctx context.Context, s string) string {
	id := strings.TrimPrefix(strings.TrimSpace(s), "@")
	if userIDPattern.MatchString(id) {
		return r.resolver.Resolve(ctx, id)
	}
	return s
}
