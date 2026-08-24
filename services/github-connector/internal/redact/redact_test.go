package redact

import (
	"strings"
	"testing"
)

// P1-2 / T1 / B3: classification inputs and log output must be scrubbed
// before persistence.

func TestStringScrubsSecretShapes(t *testing.T) {
	input := "token=ghp_0123456789abcdef012345678901234567 bearer abcdefghijklmno password=hunter2x postgres://sauron:hunter2@db.example.com/sauron"
	out := String(input)
	for _, leak := range []string{"ghp_", "password=hunter2x", "hunter2@"} {
		if strings.Contains(out, leak) {
			t.Fatalf("secret shape %q survived scrubbing: %s", leak, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("redaction placeholder missing: %s", out)
	}
}

func TestWriterScrubsLogLines(t *testing.T) {
	var sb strings.Builder
	w := &Writer{Next: &sb}
	if _, err := w.Write([]byte("level=INFO msg=\"run failed\" err=\"bearer ABCDEFGHIJK123\"\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if strings.Contains(sb.String(), "ABCDEFGHIJK123") {
		t.Fatalf("bearer leaked into log sink: %s", sb.String())
	}
}
