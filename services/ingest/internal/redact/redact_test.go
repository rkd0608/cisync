package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPayloadRedactsSensitiveKeys(t *testing.T) {
	raw := []byte(`{
		"action": "opened",
		"installation": {"id": 7},
		"pull_request": {
			"title": "fix",
			"head": {"ref": "agent-branch", "access_token": "ghs_supersecretvalue1"},
			"body": "uses password=abc"
		},
		"api_key": "AKIAIOSFODNN7EXAMPLE",
		"nested": {"deep": [{"secret_value": "x", "keep": 1}]}
	}`)
	out, err := Payload(raw)
	if err != nil {
		t.Fatalf("Payload returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("redacted payload invalid json: %v", err)
	}
	if !strings.Contains(string(out), `"action":"opened"`) {
		t.Fatalf("benign key lost: %s", out)
	}
	if strings.Contains(string(out), "ghs_supersecretvalue1") {
		t.Fatalf("token leaked: %s", out)
	}
	if strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("aws key leaked: %s", out)
	}
	pr := doc["pull_request"].(map[string]any)
	head := pr["head"].(map[string]any)
	if head["access_token"] != redactedPlaceholder {
		t.Fatalf("access_token not redacted: %v", head["access_token"])
	}
	if doc["api_key"] != redactedPlaceholder {
		t.Fatalf("api_key not redacted: %v", doc["api_key"])
	}
	nested := doc["nested"].(map[string]any)
	first := nested["deep"].([]any)[0].(map[string]any)
	if first["secret_value"] != redactedPlaceholder {
		t.Fatalf("nested secret not redacted: %v", first)
	}
	if first["keep"] != float64(1) {
		t.Fatalf("benign nested value lost: %v", first)
	}
}

func TestStringScrubsValuePatterns(t *testing.T) {
	cases := map[string]string{
		"auth bearer abcdefgh1234":                            "bearer",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.SflKxwRJSM": "eyJ",
		"pat ghp_0123456789abcdefghij":                        "ghp_",
		"aws AKIAIOSFODNN7EXAMPLE":                            "AKIA",
		"conn postgres://u:secretpw@h/db":                     "secretpw@h",
		"gcp AIzaSyD-1234567890abcdefghijklmnopqrstuv":        "AIza",
	}
	for input, mustNotContain := range cases {
		got := String(input)
		if strings.Contains(got, mustNotContain) {
			t.Fatalf("pattern leaked for %q: %q", input, got)
		}
		if !strings.Contains(got, redactedPlaceholder) {
			t.Fatalf("no placeholder for %q: %q", input, got)
		}
	}
}

func TestPayloadFailsClosedOnInvalidJSON(t *testing.T) {
	out, err := Payload([]byte(`{"broken": `))
	if err == nil {
		t.Fatalf("expected error for malformed payload")
	}
	var doc map[string]any
	if json.Unmarshal(out, &doc) != nil || doc["redacted"] != true {
		t.Fatalf("fail-closed tombstone expected, got: %s", out)
	}
}

func TestKeyIsSensitive(t *testing.T) {
	sensitive := []string{"token", "access_token", "private-key", "Authorization", "client_secret", "password"}
	for _, k := range sensitive {
		if !KeyIsSensitive(k) {
			t.Fatalf("expected sensitive key: %s", k)
		}
	}
	benign := []string{"repository", "action", "sha", "title"}
	for _, k := range benign {
		if KeyIsSensitive(k) {
			t.Fatalf("expected benign key: %s", k)
		}
	}
}

func TestWriterRedactsLogLines(t *testing.T) {
	var buf strings.Builder
	w := &Writer{Next: &buf}
	_, err := w.Write([]byte("user login password=hunter2 token=ghp_0123456789abcdefghij ok\n"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "ghp_") {
		t.Fatalf("log secret leaked: %s", out)
	}
}

func TestWalkPreservesTypes(t *testing.T) {
	in := map[string]any{"n": json.Number("42"), "f": 3.5, "b": true, "nil": nil, "arr": []any{json.Number("1")}}
	out := walk(in)
	if !reflect.DeepEqual(in, out.(map[string]any)) {
		t.Fatalf("walk mutated benign document: %#v vs %#v", in, out)
	}
}
