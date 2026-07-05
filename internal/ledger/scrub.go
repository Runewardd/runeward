package ledger

import (
	"regexp"
	"strings"
)

// redactMask replaces a detected (but undeclared) secret substring. Declared
// secrets are hashed via redactString instead, so they remain provably linkable
// to a known value; pattern matches are simply masked.
const redactMask = "[REDACTED]"

// secretPatterns matches common credential shapes so a secret pasted into a
// command argument, code snippet, or metadata value is masked in the ledger
// even when it was never declared as an [[env]] secret. Patterns are
// intentionally conservative to avoid mangling ordinary payloads; each match is
// replaced with redactMask (for key/value forms, only the value is replaced).
var secretPatterns = []*regexp.Regexp{
	// PEM private key blocks (any key type).
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
	// JSON Web Tokens.
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}`),
	// Provider-specific tokens.
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                 // AWS access key id
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),       // GitHub tokens
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),     // Slack tokens
	regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),           // Google API key
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),              // OpenAI-style keys
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{8,}`), // Authorization: Bearer <token>
}

// secretKeyValue masks the value in key=value / key: value pairs whose key names
// a credential (password, token, secret, api_key, access_key, ...). Submatch 1
// is the key-and-separator prefix to keep; submatch 2 is the value to mask.
var secretKeyValue = regexp.MustCompile(
	`(?i)((?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret|auth[_-]?token)\s*[=:]\s*)("[^"]+"|'[^']+'|[^\s"'&;]+)`,
)

// Scrub returns a copy of ev with secrets removed from its payload fields
// (Action, Args, Meta values). Declared sensitive values are replaced by their
// "sha256:<hex>" digest; additionally, credential-shaped substrings are masked
// with redactMask. Structural fields (Tool, Verdict, SessionID, ...) are always
// preserved. When nothing is changed, ev is returned unmodified (Redacted stays
// false) so clean events remain fully readable. PayloadHash records the hash of
// the original payload so the plaintext can later be proven to match.
func Scrub(ev Event, sensitive ...string) Event {
	original := ev
	changed := false

	set := make(map[string]struct{}, len(sensitive))
	for _, s := range sensitive {
		if s != "" {
			set[s] = struct{}{}
		}
	}

	scrub := func(s string) string {
		out := scrubValue(s, set)
		if out != s {
			changed = true
		}
		return out
	}

	action := scrub(ev.Action)

	var args []string
	if ev.Args != nil {
		args = make([]string, len(ev.Args))
		for i, a := range ev.Args {
			args[i] = scrub(a)
		}
	}

	var meta map[string]string
	if ev.Meta != nil {
		meta = make(map[string]string, len(ev.Meta))
		for k, v := range ev.Meta {
			meta[k] = scrub(v)
		}
	}

	if !changed {
		return original
	}

	ev.Action = action
	ev.Args = args
	ev.Meta = meta
	ev.Redacted = true
	ev.PayloadHash = hashPayload(original)
	return ev
}

// ScrubString masks declared secrets and pattern-detected credentials in a
// free-form string (e.g. tool stdout/stderr) so they aren't returned to
// clients in cleartext. Declared secrets are masked (not hashed) here, since
// the result is meant to stay human-readable.
func ScrubString(s string, sensitive ...string) string {
	if s == "" {
		return ""
	}
	for _, secret := range sensitive {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, redactMask)
		}
	}
	s = secretKeyValue.ReplaceAllString(s, "${1}"+redactMask)
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, redactMask)
	}
	return s
}

// scrubValue applies declared-secret hashing and pattern masking to one string.
func scrubValue(s string, sensitive map[string]struct{}) string {
	if s == "" {
		return ""
	}
	// Exact match on a declared secret: hash the whole value.
	if _, ok := sensitive[s]; ok {
		return redactString(s)
	}
	// Declared secret embedded in a larger string: mask the occurrence.
	for secret := range sensitive {
		if secret != "" && s != secret {
			s = strings.ReplaceAll(s, secret, redactMask)
		}
	}
	// Pattern-detected credentials.
	s = secretKeyValue.ReplaceAllString(s, "${1}"+redactMask)
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, redactMask)
	}
	return s
}
