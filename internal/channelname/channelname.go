// Package channelname builds Slack channel names for cases:
// <prefix><%04d sequence>[-<slug>], lowercase, at most 80 characters
// (Slack's limit). Uniqueness is guaranteed by the sequence number,
// which comes from the case row id.
package channelname

import (
	"fmt"
	"strings"
)

// maxLen is Slack's channel-name length limit.
const maxLen = 80

// Build returns the channel name for a case. The slug is derived
// from the title; titles with no ASCII-alphanumeric content (e.g.
// Japanese-only) yield no slug, leaving <prefix><seq>.
func Build(prefix string, seq int64, title string) string {
	base := fmt.Sprintf("%s%04d", prefix, seq)
	slug := slugify(title, maxLen-len(base)-1) // -1 for the joining '-'
	if slug == "" {
		return base
	}
	return base + "-" + slug
}

// slugify lowercases the title, replaces every non-[a-z0-9] run with
// a single '-', trims leading/trailing '-', and truncates to max
// without leaving a trailing '-'. Returns "" when max <= 0 or
// nothing survives.
func slugify(title string, max int) string {
	if max <= 0 {
		return ""
	}
	var b strings.Builder
	prevDash := true // suppress leading dash
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
	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}
	return s
}
