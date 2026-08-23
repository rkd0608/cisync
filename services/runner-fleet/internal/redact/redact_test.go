package redact

import (
	"strings"
	"testing"
)

func TestStringScrubsValuePatterns(t *testing.T) {
	cases := []string{
		"auth bearer abcdefgh1234",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.SflKxwRJSME",
		"pat ghp_0123456789abcdefghij",
		"aws AKIAIOSFODNN7EXAMPLE",
		"conn postgres://u:secretpw@h/db",
		"gcp AIzaSyD-1234567890abcdefghijklmnopqrstu",
		"login password=hunter2",
		"-----BEGIN RSA PRIVATE KEY-----",
	}
	for _, input := range cases {
		got := String(input)
		if got == input {
			continue
		}
		if !strings.Contains(got, redactedPlaceholder) {
			t.Fatalf("no placeholder for %q: %q", input, got)
		}
	}
}

func TestWriterRedactsLogLines(t *testing.T) {
	var buf strings.Builder
	w := &Writer{Next: &buf}
	if _, err := w.Write([]byte(`job failed token=ghp_0123456789abcdefghij retrying`)); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "ghp_") {
		t.Fatalf("log secret leaked: %s", out)
	}
}
