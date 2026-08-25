package ghauth

import (
	"context"
	"testing"

	"github.com/google/go-github/v66/github"
	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/testsupport"
)

func TestRegistryLazilyBuildsAndReusesSources(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	registry := NewRegistry("app_1", "", WithBaseURL(fake.BaseURL), WithKey(testKey))

	srcA, err := registry.Source(42)
	require.NoError(t, err)
	srcB, err := registry.Source(42)
	require.NoError(t, err)
	require.Same(t, srcA, srcB, "same installation reuses one source")
	srcC, err := registry.Source(43)
	require.NoError(t, err)
	require.NotSame(t, srcA, srcC, "different installations get distinct sources")

	token, err := srcA.Token("acme/payments")
	require.NoError(t, err)
	require.Equal(t, "fake_token_42", token, "source bound to its installation id")
}

func TestRegistrySeedsDefaultInstallation(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	registry := NewRegistry("app_1", "", WithBaseURL(fake.BaseURL), WithKey(testKey))
	require.NoError(t, registry.Seed(42))
	require.Len(t, fake.Tokens(), 0, "seed builds the source without minting")
	_, err := registry.Source(42)
	require.NoError(t, err)
}

func TestRegistryClientPerInstallationWithScopedTransport(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	registry := NewRegistry("app_1", "", WithBaseURL(fake.BaseURL), WithKey(testKey))

	clientA, err := registry.Client(42)
	require.NoError(t, err)
	clientB, err := registry.Client(42)
	require.NoError(t, err)
	require.Equal(t, clientA.BaseURL, clientB.BaseURL)

	// Drive a real Checks call through the scoped transport.
	_, _, err = clientA.Checks.CreateCheckRun(context.Background(), "acme", "payments", github.CreateCheckRunOptions{
		Name:    "Agent Verification Gate",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Status:  github.String("queued"),
	})
	require.NoError(t, err)
	calls := fake.Calls()
	require.Len(t, calls, 1)
	mints := fake.Tokens()
	require.Len(t, mints, 1)
	require.JSONEq(t, `{"repositories":["payments"],"permissions":{"checks":"write"}}`, mints[0].Body,
		"transport minted a payments-scoped token for the API call")
}

func TestRegistryWithoutKeyFailsClosedOnFirstUse(t *testing.T) {
	registry := NewRegistry("app_1", "/nonexistent/key.pem")
	_, err := registry.Source(42)
	require.Error(t, err)
}
