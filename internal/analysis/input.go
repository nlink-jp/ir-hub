package analysis

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nlink-jp/nlk/guard"

	"github.com/nlink-jp/ir-hub/internal/defang"
	"github.com/nlink-jp/ir-hub/internal/sanitize"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// promptOverheadTokens is reserved for the system prompt, the user
// prompt framing, and per-message wrapping when deciding how many
// messages fit the configured token budget.
const promptOverheadTokens = 4000

// Input is the prepared conversation for one analysis turn.
type Input struct {
	Case         *store.Case
	Conversation string   // formatted, defanged, nonce-wrapped lines
	Participants []string // unique user IDs, order of appearance, bot excluded
	Total        int      // ingested messages before truncation
	Analyzed     int      // messages included after the budget cut
	Truncated    bool
	Tag          guard.Tag
}

// buildInput loads a case's messages, drops the bot's own posts,
// defangs IoCs, logs injection findings, wraps each message in this
// turn's nonce tag, and enforces the token budget by keeping the
// newest messages (incidents conclude at the end; the truncation is
// noted in the prompt and the report).
func (r *Runner) buildInput(c *store.Case) (*Input, error) {
	msgs, err := r.store.ListMessages(c.ID)
	if err != nil {
		return nil, err
	}

	tag := guard.NewTag()

	type line struct {
		text   string
		tokens int
		user   string
	}
	var lines []line
	for _, m := range msgs {
		if m.UserID != "" && m.UserID == r.cfg.BotUserID {
			continue // ir-hub's own posts are noise for the analysis
		}
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}

		defanged, _ := defang.Text(text)
		for _, w := range sanitize.Detect(defanged) {
			r.logf("analysis: [SECURITY] injection pattern in %s/%s: %s", m.ChannelID, m.TS, w)
		}
		wrapped, err := tag.Wrap(defanged)
		if err != nil {
			// Tag collision inside a message is a strong attack
			// signal: skip the message, keep the analysis going.
			r.logf("analysis: [SECURITY] guard tag collision in %s/%s, message skipped", m.ChannelID, m.TS)
			continue
		}

		author := "@" + m.UserID
		if m.UserID == "" {
			author = "[bot] " + m.BotID
		}
		formatted := fmt.Sprintf("[%s] %s:\n%s", formatTS(m.TS), author, wrapped)
		lines = append(lines, line{text: formatted, tokens: EstimateTokens(formatted), user: m.UserID})
	}

	total := len(lines)
	budget := r.cfg.MaxInputTokens - promptOverheadTokens

	// Keep the newest lines within budget.
	used, start := 0, len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if used+lines[i].tokens > budget && start < len(lines) {
			break
		}
		if used+lines[i].tokens > budget {
			// Even the newest single message exceeds the budget:
			// include it anyway — the LLM call may still succeed and
			// failing here would make huge single messages fatal.
			start = i
			used += lines[i].tokens
			break
		}
		used += lines[i].tokens
		start = i
	}
	kept := lines[start:]

	var sb strings.Builder
	seen := map[string]bool{}
	var participants []string
	for _, l := range kept {
		sb.WriteString(l.text)
		sb.WriteString("\n")
		if l.user != "" && !seen[l.user] {
			seen[l.user] = true
			participants = append(participants, l.user)
		}
	}

	return &Input{
		Case:         c,
		Conversation: sb.String(),
		Participants: participants,
		Total:        total,
		Analyzed:     len(kept),
		Truncated:    len(kept) < total,
		Tag:          tag,
	}, nil
}

// formatTS renders a Slack ts ("1718000000.000100") as a UTC
// timestamp; unparsable values pass through unchanged.
func formatTS(ts string) string {
	sec := ts
	if i := strings.Index(ts, "."); i >= 0 {
		sec = ts[:i]
	}
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return ts
	}
	return time.Unix(n, 0).UTC().Format("2006-01-02 15:04:05")
}
