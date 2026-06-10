// Package channelname builds Slack channel names for cases:
// <prefix><%04d sequence>[-<slug>], at most 80 characters (Slack's
// limit). The slug keeps Unicode letters and digits — Slack allows
// non-Latin channel names, and Japanese case titles would otherwise
// lose all meaning — while punctuation and whitespace collapse to
// single hyphens. Uniqueness is guaranteed by the sequence number,
// which comes from the case row id.
package channelname

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxLen is Slack's channel-name length limit in characters.
const maxLen = 80

// Build returns the channel name for a case. Titles with no
// letters or digits at all yield no slug, leaving <prefix><seq>.
func Build(prefix string, seq int64, title string) string {
	base := fmt.Sprintf("%s%04d", prefix, seq)
	slug := slugify(title, maxLen-utf8.RuneCountInString(base)-1) // -1 for the joining '-'
	if slug == "" {
		return base
	}
	return base + "-" + slug
}

// slugify lowercases the title, keeps Unicode letters and digits,
// replaces every other run with a single '-', trims leading and
// trailing '-', and truncates to max characters (runes) without
// leaving a trailing '-'. Returns "" when max <= 0 or nothing
// survives.
func slugify(title string, max int) string {
	if max <= 0 {
		return ""
	}
	var runes []rune
	prevDash := true // suppress leading dash
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			runes = append(runes, r)
			prevDash = false
		default:
			if !prevDash {
				runes = append(runes, '-')
				prevDash = true
			}
		}
	}
	runes = trimTrailingDash(runes)
	if len(runes) > max {
		runes = trimTrailingDash(runes[:max])
	}
	return string(runes)
}

func trimTrailingDash(runes []rune) []rune {
	for len(runes) > 0 && runes[len(runes)-1] == '-' {
		runes = runes[:len(runes)-1]
	}
	return runes
}
