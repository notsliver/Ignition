package servers

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"DATABASE_PASSWORD=hunter2 next":                                       "hunter2",
		`api_key: "abc123def"`:                                                 "abc123def",
		"postgres://app:s3cret@db:5432/app":                                    "s3cret",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig":               "eyJhbGciOiJIUzI1NiJ9",
		"key AKIAABCDEFGHIJKLMNOP here":                                        "AKIAABCDEFGHIJKLMNOP",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----": "MIIE",
		"token ghp_abcdefghijklmnopqrstuvwx":                                   "ghp_abcdefghijklmnopqrstuvwx",
	}
	for in, secret := range cases {
		out := Redact(in)
		if strings.Contains(out, secret) {
			t.Errorf("Redact(%q) = %q still contains %q", in, out, secret)
		}
	}
	for _, keep := range []string{"GET /health 200 3ms", "listening on :8080", "user=alice logged in"} {
		if Redact(keep) != keep {
			t.Errorf("Redact changed harmless line %q to %q", keep, Redact(keep))
		}
	}
}
