package servers

import "regexp"

// Redact masks values that look like credentials before log lines or diagnostic output leave the
// server: private keys, bearer tokens, URLs with passwords, and KEY=value pairs whose key suggests
// a secret. It errs on the side of masking; a hidden value is better than a leaked one.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}

var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`), "[private key redacted]"},
	{regexp.MustCompile(`(?i)\b(bearer|basic|token)\s+[A-Za-z0-9._~+/=-]{12,}`), "$1 ••••••"},
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/\s@]+:)[^@\s]+@`), "${1}••••••@"},
	{regexp.MustCompile(`(?i)\b([A-Z0-9_.-]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIALS?|AUTH)[A-Z0-9_.-]*)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`), "$1$2••••••"},
	{regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`), "••••••"},
	{regexp.MustCompile(`\b(ghp|gho|ghs|ghu|github_pat|glpat|xox[baprs])[-_][A-Za-z0-9_-]{16,}`), "••••••"},
}
