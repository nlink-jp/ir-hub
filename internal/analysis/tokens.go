package analysis

import (
	"strings"
	"unicode"
)

// EstimateTokens approximates the Gemini token count of s. Ported
// from gem-summary: the max of a char-based (len/4) and word-based
// (words*1.3) estimate, plus one token per CJK rune — both
// components bias toward over-estimation, which is the safe
// direction for budget enforcement.
func EstimateTokens(s string) int {
	charBased := (len(s) + 3) / 4
	wordBased := len(strings.Fields(s)) * 13 / 10

	cjk := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if (r >= 0x3040 && r <= 0x9fff) || (r >= 0xac00 && r <= 0xd7af) {
			cjk++
		}
	}

	m := charBased
	if wordBased > m {
		m = wordBased
	}
	return m + cjk
}
