package ghauth

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/testsupport"
)

func lastMintBody(t *testing.T, fake *testsupport.FakeGitHub) string {
	t.Helper()
	mints := fake.Tokens()
	require.NotEmpty(t, mints)
	return mints[len(mints)-1].Body
}

func newScopeTestSource(t *testing.T, fake *testsupport.FakeGitHub,
	opts ...Option) *InstallationTokenSource {
	t.Helper()
	src, err := NewInstallationTokenSource("app_1", 42,
		append([]Option{WithBaseURL(fake.BaseURL), WithKey(testKey),
			WithHTTPClient(&http.Client{Timeout: 5 * time.Second})}, opts...)...)
	require.NoError(t, err)
	return src
}

// TestMintBodyScoping pins the exact permission bodies requested at token
// exchange: checks-only unless comment posting was explicitly opted into
// (CISYNC_CONN_REPORT_COMMENTS ⇒ operator must enable Issues: Write first).
func TestMintBodyScoping(t *testing.T) {
	t.Run("default_checks_only", func(t *testing.T) {
		fake := testsupport.NewFakeGitHub(t)
		src := newTestSource(t, fake)
		token, err := src.Token("acme/payments")
		require.NoError(t, err)
		require.NotEmpty(t, token)
		require.JSONEq(t,
			`{"repositories":["payments"],"permissions":{"checks":"write"}}`,
			lastMintBody(t, fake), "default stays byte-compatible")
	})

	t.Run("with_issues_scope", func(t *testing.T) {
		fake := testsupport.NewFakeGitHub(t)
		src := newScopeTestSource(t, fake, WithIssuesWriteScope())
		_, err := src.Token("acme/payments")
		require.NoError(t, err)
		require.Contains(t, lastMintBody(t, fake),
			`"checks":"write","issues":"write"`,
			"comment posting needs Issues:Write on every scoped token")
	})
}

// TestRegistryPropagatesIssuesScope proves the registry forwards the flag to
// every lazily-built installation source.
func TestRegistryPropagatesIssuesScope(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	reg := NewRegistry("app_1", "", WithBaseURL(fake.BaseURL), WithKey(testKey),
		WithIssuesWriteScope())
	src, err := reg.Source(7)
	require.NoError(t, err)
	_, err = src.Token("acme/payments")
	require.NoError(t, err)
	require.Contains(t, lastMintBody(t, fake), `"issues":"write"`)
}
