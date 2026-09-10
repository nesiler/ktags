package mask

import (
	"regexp"
	"slices"
	"strings"
)

// Placeholder replaces every masked value.
const Placeholder = "[masked]"

// pattern masks the whole match, or only its non-empty capture groups when valueOnly is set.
type pattern struct {
	re        *regexp.Regexp
	valueOnly bool
}

// Building blocks of the token/password patterns. Every value alternative is one capture group.
const (
	// sensitiveKey is a key or flag name containing token or password, e.g. rke2_token, --password.
	sensitiveKey = `[A-Za-z0-9_.-]*(?:token|password)[A-Za-z0-9_.-]*`
	// quotedValue: a closed double- or single-quoted value ('' is YAML's escaped quote); a quote
	// that never closes (a truncated line) masks to the end of the line.
	quotedValue = `"((?:[^"\\]|\\.)*)"|'((?:[^']|'')*)'|"([^\n]*)|'([^\n]*)`
	// escapedValue: a value quoted with \" inside a JSON string, e.g. Ansible msg or stdout.
	escapedValue = `\\+"((?:[^"\\]|\\[^"\\])*)\\+"|\\+"([^\n]*)`
	// blockValue: a YAML block scalar; the value is every following indented line.
	blockValue = `[|>][-+1-9]{0,2}[ \t]*(?:#[^\n]*)?\n(?:[ \t]*\n)*[ \t]+([^\n]*(?:\n(?:[ \t]*\n)*[ \t]+[^\n]*)*)`
)

// patterns follow security.md §1. Unquoted values stop at white space or a quote: over-masking the
// rest of a word is harmless, stopping early would leak.
var patterns = []pattern{
	// PEM blocks, including a block cut off before its END line (one log line of many).
	{re: regexp.MustCompile(`-----BEGIN(?s:.*?)(?:-----END [A-Z0-9 ]*-----|\z)`)},
	// age identities.
	{re: regexp.MustCompile(`(?i)AGE-SECRET-KEY-[0-9A-Z]+`)},
	// Tailscale auth, API and OAuth client keys.
	{re: regexp.MustCompile(`tskey-[A-Za-z0-9_-]+`)},
	// RKE2 tokens: K10<ca hash>, optionally followed by ::<user>:<password>.
	{re: regexp.MustCompile(`K10[0-9a-fA-F]{20,}(?:::[^\s"']*)?`)},
	// HTTP bearer credentials; the scheme name is case-insensitive (RFC 9110 §11.1).
	{re: regexp.MustCompile(`(?i)bearer[ \t]+([^\s"']+)`), valueOnly: true},
	// token/password key-value pairs in YAML, JSON (also escaped inside a JSON string), INI or
	// --flag=value form; the key stays readable.
	{
		re: regexp.MustCompile(`(?i)` + sensitiveKey + `\\*["']?[ \t]*[:=][ \t]*(?:` +
			quotedValue + `|` + escapedValue + `|` + blockValue + `|([^\s"']+))`),
		valueOnly: true,
	},
	// Command lines: --token value, -password value. The flag starts a word and the value does
	// not look like the next flag.
	{
		re: regexp.MustCompile(`(?i)(?:^|[\s"'])--?` + sensitiveKey + `[ \t]+(?:` +
			quotedValue + `|([^\s"'-][^\s"']*))`),
		valueOnly: true,
	},
}

type span struct{ start, end int }

// Mask returns s with every secret value replaced by Placeholder. Text without secrets is
// returned unchanged. Mask never fails; any input is valid.
func Mask(s string) string {
	var spans []span
	for _, p := range patterns {
		if !p.valueOnly {
			for _, m := range p.re.FindAllStringIndex(s, -1) {
				spans = append(spans, span{m[0], m[1]})
			}
			continue
		}
		spans = appendGroupSpans(spans, p, s)
	}
	if len(spans) == 0 {
		return s
	}

	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for i := 0; i < len(spans); {
		start, end := spans[i].start, spans[i].end
		// Merge overlapping and touching ranges into one placeholder.
		for i++; i < len(spans) && spans[i].start <= end; i++ {
			end = max(end, spans[i].end)
		}
		b.WriteString(s[last:start])
		b.WriteString(Placeholder)
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// appendGroupSpans records the non-empty groups of every match. The next search starts at the
// masked value rather than after it, because a value can itself be the key of the next pair
// ("token: password: x"); non-overlapping matching would skip that inner secret. The position
// always advances by at least one byte, so the loop ends even for a group at the match start.
func appendGroupSpans(spans []span, p pattern, s string) []span {
	for pos := 0; pos < len(s); {
		m := p.re.FindStringSubmatchIndex(s[pos:])
		if m == nil {
			break
		}
		next := pos + m[1]
		for g := 1; g <= p.re.NumSubexp(); g++ {
			if m[2*g] < m[2*g+1] {
				spans = append(spans, span{pos + m[2*g], pos + m[2*g+1]})
				next = min(next, pos+m[2*g])
			}
		}
		pos = max(next, pos+1)
	}
	return spans
}
