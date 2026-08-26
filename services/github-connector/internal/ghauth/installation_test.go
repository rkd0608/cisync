package ghauth

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/testsupport"
)

// testKey is a throwaway RSA key generated once for the package (minting
// only signs; no PEM file needed in unit tests).
var testKey = mustTestKey()

func mustTestKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

func newTestSource(t *testing.T, fake *testsupport.FakeGitHub) *InstallationTokenSource {
	t.Helper()
	src, err := NewInstallationTokenSource("app_1", 42,
		WithBaseURL(fake.BaseURL), WithKey(testKey), WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	require.NoError(t, err)
	return src
}

func TestTokenMintBodyScopesToRepo(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	src := newTestSource(t, fake)

	token, err := src.Token("acme/payments")
	require.NoError(t, err)
	require.Equal(t, "fake_token_42", token)

	mints := fake.Tokens()
	require.Len(t, mints, 1)
	require.Equal(t, int64(42), mints[0].InstallationID)
	require.JSONEq(t,
		`{"repositories":["payments"],"permissions":{"checks":"write"}}`,
		mints[0].Body, "per-repo scoping body per plan §2.1")
}

func TestTokenCachedPerRepoUntilExpiryMargin(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	// WHY real-time base: the fake mints expires_at = wall-now+1h, so the
	// injected clock starts aligned and jumps past expiry−60s for the
	// refresh assertion.
	now := time.Now()
	src := newTestSource(t, fake)
	setNow(src, func() time.Time { return now })

	_, err := src.Token("acme/payments")
	require.NoError(t, err)
	_, err = src.Token("acme/other") // different repo ⇒ separate mint
	require.NoError(t, err)
	_, err = src.Token("acme/payments") // cached ⇒ no mint
	require.NoError(t, err)
	require.Len(t, fake.Tokens(), 2)

	// Advance past expiry−60s: next call re-mints.
	now = now.Add(2 * time.Hour)
	setNow(src, func() time.Time { return now })
	_, err = src.Token("acme/payments")
	require.NoError(t, err)
	require.Len(t, fake.Tokens(), 3)
}

func TestTokenSingleFlightUnderConcurrency(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	src := newTestSource(t, fake)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := src.Token("acme/payments")
			require.NoError(t, err)
			require.NotEmpty(t, token)
		}()
	}
	wg.Wait()
	require.Len(t, fake.Tokens(), 1, "concurrent Token calls collapse to ONE mint")
}

func TestTokenRejectsMalformedRepoBeforeNetwork(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	src := newTestSource(t, fake)
	_, err := src.Token("not-a-repo")
	require.Error(t, err)
	require.Empty(t, fake.Tokens(), "no network call on malformed repo")
}

func setNow(s *InstallationTokenSource, now func() time.Time) { s.now = now }
