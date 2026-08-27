package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func baseReportEnv(t *testing.T) func() {
	t.Helper()
	required := map[string]string{
		"CISYNC_CONN_WEBHOOK_SECRET": "s",
	}
	for k, v := range required {
		t.Setenv(k, v)
	}
	return func() {}
}

// TestReportCommentsFlagParses pins CISYNC_CONN_REPORT_COMMENTS semantics:
// default OFF (byte-compatible posture); truthy values enable; anything else
// fails loudly rather than silently guessing operator intent.
func TestReportCommentsFlagParses(t *testing.T) {
	t.Run("default_off", func(t *testing.T) {
		baseReportEnv(t)
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.ReportComments)
	})

	t.Run("truthy_values", func(t *testing.T) {
		baseReportEnv(t)
		for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
			t.Setenv("CISYNC_CONN_REPORT_COMMENTS", v)
			cfg, err := Load()
			require.NoError(t, err, "value %q", v)
			require.True(t, cfg.ReportComments, "value %q", v)
		}
	})

	t.Run("invalid_value_fails", func(t *testing.T) {
		baseReportEnv(t)
		t.Setenv("CISYNC_CONN_REPORT_COMMENTS", "maybe")
		_, err := Load()
		require.ErrorContains(t, err, "CISYNC_CONN_REPORT_COMMENTS")
	})
}
