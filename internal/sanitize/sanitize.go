// Package sanitize detects prompt-injection attempts in
// user-sourced text. Detection is advisory — analysis proceeds
// regardless, with warnings logged for the security audit trail —
// because the actual defense is structural: every message is
// wrapped in an unpredictable nonce tag (nlk/guard) and the system
// prompt instructs the model to treat tagged content as data only.
//
// Patterns ported from ai-ir2 (parser/sanitizer.py).
package sanitize

import (
	"fmt"
	"regexp"
)

type pattern struct {
	re   *regexp.Regexp
	desc string
}

var patterns = []pattern{
	{regexp.MustCompile(`(?i)ignore\s+(?:(?:previous|all|above|prior)\s+)*instructions?`), "Instruction override attempt"},
	{regexp.MustCompile(`(?i)forget\s+(everything|all|previous|prior)`), "Memory wipe attempt"},
	{regexp.MustCompile(`(?i)you\s+are\s+now\s+`), "Persona reassignment attempt"},
	{regexp.MustCompile(`(?i)new\s+instructions?\s*:`), "New instruction injection"},
	{regexp.MustCompile(`(?i)system\s*:\s*`), "System prompt injection marker"},
	{regexp.MustCompile(`(?i)<\s*/?system\s*>`), "XML system tag injection"},
	{regexp.MustCompile(`(?i)<\s*/?instructions?\s*>`), "XML instructions tag injection"},
	{regexp.MustCompile(`(?i)\[INST\]`), "Llama instruction marker"},
	{regexp.MustCompile(`(?i)###\s*instruction`), "Markdown instruction header injection"},
	{regexp.MustCompile(`(?i)act\s+as\s+`), "Role-play directive"},
	{regexp.MustCompile(`(?i)roleplay\s+as`), "Role-play directive"},
	{regexp.MustCompile(`(?i)pretend\s+(you\s+are|to\s+be)`), "Persona pretend directive"},
	{regexp.MustCompile(`(?i)disregard\s+(previous|all|above|prior)`), "Instruction disregard attempt"},
	{regexp.MustCompile(`(?i)override\s+(previous|system|all)\s+(prompt|instructions?)?`), "System override attempt"},
}

// Detect scans text for injection patterns and returns one warning
// per matched pattern. An empty slice means nothing matched.
func Detect(text string) []string {
	var warnings []string
	for _, p := range patterns {
		if loc := p.re.FindStringIndex(text); loc != nil {
			warnings = append(warnings,
				fmt.Sprintf("%s: matched %q at position %d", p.desc, text[loc[0]:loc[1]], loc[0]))
		}
	}
	return warnings
}
