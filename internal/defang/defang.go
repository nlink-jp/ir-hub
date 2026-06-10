// Package defang neutralizes Indicators of Compromise in text so
// they cannot be accidentally activated (clicked, resolved,
// auto-linked) anywhere downstream — in LLM prompts, model output,
// Slack posts, or stored knowledge documents.
//
// Ported from ai-ir2 (parser/defang.py). Processing order matters
// and is fixed: URLs → emails → IPv4 → standalone domains → hashes
// (hashes are recorded but not rewritten — they are not
// executable). Later matchers skip spans already claimed by earlier
// ones, so a domain inside an already-defanged URL is not touched
// twice.
package defang

import (
	"regexp"
	"sort"
	"strings"
)

// IoC is one extracted indicator.
type IoC struct {
	Original string
	Defanged string
	Type     string // url | email | ip | domain | hash
}

var (
	// Go regexp has no lookbehind/lookahead; candidates are matched
	// broadly and context-checked in code.
	urlRe    = regexp.MustCompile(`(?i)(?:https?|ftp|file)://[^\s<>"'` + "`" + `,;)(\[\]]+`)
	emailRe  = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	ipv4Re   = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)
	hashRe   = regexp.MustCompile(`\b(?:[0-9a-fA-F]{64}|[0-9a-fA-F]{40}|[0-9a-fA-F]{32})\b`)
	domainRe = regexp.MustCompile(`(?i)\b(?:[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+` +
		`(?:com|net|org|io|gov|edu|mil|int|info|biz|co|uk|de|fr|jp|ru|cn|au|ca|onion|local|internal|corp|lan)\b`)

	schemeHTTP  = regexp.MustCompile(`(?i)^http://`)
	schemeHTTPS = regexp.MustCompile(`(?i)^https://`)
	schemeFTP   = regexp.MustCompile(`(?i)^ftp://`)
	schemeFILE  = regexp.MustCompile(`(?i)^file://`)
)

// IP defangs an IPv4 address: 192.168.1.1 → 192[.]168[.]1[.]1.
func IP(ip string) string { return strings.ReplaceAll(ip, ".", "[.]") }

// Domain defangs a domain: evil.com → evil[.]com.
func Domain(d string) string { return strings.ReplaceAll(d, ".", "[.]") }

// Email defangs an address: user@evil.com → user[@]evil[.]com.
func Email(e string) string {
	at := strings.Index(e, "@")
	if at < 0 {
		return e
	}
	return e[:at] + "[@]" + strings.ReplaceAll(e[at+1:], ".", "[.]")
}

// URL defangs the scheme and hostname dots. file:// URLs get only
// the scheme rewritten — their paths are local, not hostnames.
func URL(u string) string {
	d := schemeHTTPS.ReplaceAllString(u, "hxxps://")
	d = schemeHTTP.ReplaceAllString(d, "hxxp://")
	d = schemeFTP.ReplaceAllString(d, "fxxp://")
	d = schemeFILE.ReplaceAllString(d, "fxxle://")
	if strings.HasPrefix(strings.ToLower(d), "fxxle://") {
		return d
	}

	scheme := ""
	for _, s := range []string{"hxxps://", "hxxp://", "fxxp://"} {
		if strings.HasPrefix(strings.ToLower(d), s) {
			scheme = d[:len(s)]
			d = d[len(s):]
			break
		}
	}
	if scheme == "" {
		return d
	}
	hostname, path := d, ""
	if i := strings.Index(d, "/"); i >= 0 {
		hostname, path = d[:i], d[i:]
	}
	host, port := hostname, ""
	if i := strings.Index(hostname, ":"); i >= 0 {
		host, port = hostname[:i], hostname[i:]
	}
	return scheme + strings.ReplaceAll(host, ".", "[.]") + port + path
}

type span struct {
	start, end int
	repl       string
	ioc        IoC
}

func overlaps(start, end int, spans []span) bool {
	for _, s := range spans {
		if start < s.end && end > s.start {
			return true
		}
	}
	return false
}

func validIPv4(s string) bool {
	for _, oct := range strings.Split(s, ".") {
		if len(oct) > 1 && oct[0] == '0' && oct != "0" {
			// Leading zeros accepted by ai-ir2; keep parity (parse value only).
			_ = oct
		}
		n := 0
		for _, c := range oct {
			n = n*10 + int(c-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

// Text defangs all IoCs in s and returns the rewritten text plus
// the extracted indicators (network IoCs in positional order,
// hashes appended).
func Text(s string) (string, []IoC) {
	var spans []span
	var hashes []IoC

	for _, m := range urlRe.FindAllStringIndex(s, -1) {
		orig := s[m[0]:m[1]]
		spans = append(spans, span{m[0], m[1], URL(orig), IoC{orig, URL(orig), "url"}})
	}
	for _, m := range emailRe.FindAllStringIndex(s, -1) {
		if overlaps(m[0], m[1], spans) {
			continue
		}
		orig := s[m[0]:m[1]]
		spans = append(spans, span{m[0], m[1], Email(orig), IoC{orig, Email(orig), "email"}})
	}
	for _, m := range ipv4Re.FindAllStringIndex(s, -1) {
		// Lookaround substitute: reject when adjacent to a digit or
		// dot (version strings, longer dotted sequences).
		if m[0] > 0 && (s[m[0]-1] == '.' || isDigit(s[m[0]-1])) {
			continue
		}
		if m[1] < len(s) && (s[m[1]] == '.' || isDigit(s[m[1]])) {
			continue
		}
		orig := s[m[0]:m[1]]
		if !validIPv4(orig) || overlaps(m[0], m[1], spans) {
			continue
		}
		spans = append(spans, span{m[0], m[1], IP(orig), IoC{orig, IP(orig), "ip"}})
	}
	for _, m := range domainRe.FindAllStringIndex(s, -1) {
		// Lookbehind substitute: skip when preceded by '/' or '@'
		// (URL path segments, email local parts already handled).
		if m[0] > 0 && (s[m[0]-1] == '/' || s[m[0]-1] == '@' || s[m[0]-1] == '.') {
			continue
		}
		if overlaps(m[0], m[1], spans) {
			continue
		}
		orig := s[m[0]:m[1]]
		spans = append(spans, span{m[0], m[1], Domain(orig), IoC{orig, Domain(orig), "domain"}})
	}
	for _, m := range hashRe.FindAllStringIndex(s, -1) {
		if overlaps(m[0], m[1], spans) {
			continue
		}
		orig := s[m[0]:m[1]]
		hashes = append(hashes, IoC{orig, orig, "hash"})
	}

	// Apply replacements right-to-left to keep offsets valid.
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	out := s
	for _, sp := range spans {
		out = out[:sp.start] + sp.repl + out[sp.end:]
	}

	// Positional order for network IoCs, hashes appended.
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	iocs := make([]IoC, 0, len(spans)+len(hashes))
	for _, sp := range spans {
		iocs = append(iocs, sp.ioc)
	}
	iocs = append(iocs, hashes...)
	return out, iocs
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
