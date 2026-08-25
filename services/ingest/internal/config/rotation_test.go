package config

import "testing"

func TestParseWebhookSecretsOrderedList(t *testing.T) {
	got := ParseWebhookSecrets(" new , old ,, ")
	if len(got) != 2 || got[0] != "new" || got[1] != "old" {
		t.Fatalf("ordered list parse wrong: %q", got)
	}
}

func TestParseWebhookSecretsEmpty(t *testing.T) {
	if got := ParseWebhookSecrets(""); len(got) != 0 {
		t.Fatalf("empty list must yield no secrets: %q", got)
	}
}

// TestFromEnvRotationListOverridesVerifySet pins EC-010 semantics: the
// ordered list replaces the verification set while WebhookSecret stays the
// PRIMARY (first) secret used for the ctrl signing hop.
func TestFromEnvRotationListOverridesVerifySet(t *testing.T) {
	t.Setenv("SAURON_INGEST_WEBHOOK_SECRET", "primary")
	t.Setenv("SAURON_INGEST_WEBHOOK_SECRETS", "new,old")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("rotation config must parse: %v", err)
	}
	if cfg.WebhookSecret != "primary" {
		t.Fatalf("primary secret must be untouched, got %q", cfg.WebhookSecret)
	}
	if len(cfg.WebhookSecrets) != 2 || cfg.WebhookSecrets[0] != "new" || cfg.WebhookSecrets[1] != "old" {
		t.Fatalf("rotation list wrong: %q", cfg.WebhookSecrets)
	}
}

// TestFromEnvSingularFallbackUnchanged: without the list var behavior is
// byte-identical to pre-rotation deployments.
func TestFromEnvSingularFallbackUnchanged(t *testing.T) {
	t.Setenv("SAURON_INGEST_WEBHOOK_SECRET", "only")
	t.Setenv("SAURON_INGEST_WEBHOOK_SECRETS", "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("singular config must parse: %v", err)
	}
	if len(cfg.WebhookSecrets) != 1 || cfg.WebhookSecrets[0] != "only" {
		t.Fatalf("singular fallback must be exactly [secret], got %q", cfg.WebhookSecrets)
	}
}
