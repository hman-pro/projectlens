package storage

import (
	"strings"
	"testing"
)

func TestSanitizeErrText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{
			name: "openai key",
			in:   "openai: request failed: sk-abcDEF1234567890ZZZZ123",
			want: "openai: request failed: sk-[REDACTED]",
		},
		{
			name: "anthropic key",
			in:   "auth error: sk-ant-api03-aBcD_eF-1234567890",
			want: "auth error: sk-ant-[REDACTED]",
		},
		{
			name: "bearer header",
			in:   "401: Bearer eyJhbGciOi.JIUzI1NiJ9.signature",
			want: "401: Bearer [REDACTED]",
		},
		{
			name: "postgres url password",
			in:   "dial failed postgres://user:hunter2@db.internal:5432/repo",
			want: "dial failed postgres://user:[REDACTED]@db.internal:5432/repo",
		},
		{
			name: "authorization basic credential consumed fully",
			in:   "401: Authorization: Basic dXNlcjpwYXNz",
			want: "401: Authorization: [REDACTED]",
		},
		{
			name: "authorization stops at line boundary",
			in:   "rejected\nAuthorization: Bearer leaked-token\nnext-line",
			want: "rejected\nAuthorization: [REDACTED]\nnext-line",
		},
		{
			name: "no secrets passthrough",
			in:   "history: latest timestamp: context canceled",
			want: "history: latest timestamp: context canceled",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeErrText(c.in)
			if got != c.want {
				t.Fatalf("sanitizeErrText(%q):\n  got:  %q\n  want: %q", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeErrTextTruncates(t *testing.T) {
	in := strings.Repeat("x", maxErrorTextBytes+1000)
	got := sanitizeErrText(in)
	if len(got) != maxErrorTextBytes {
		t.Fatalf("len(got) = %d, want %d", len(got), maxErrorTextBytes)
	}
}
