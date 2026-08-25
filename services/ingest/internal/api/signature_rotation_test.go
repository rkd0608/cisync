package api

import "testing"

// TestVerifyGitHubSignatureAnyRotationMatrix pins the EC-010 rotation
// semantics: any-match passes, each comparison constant-time, empty list
// fails closed, and malformed headers never verify.
func TestVerifyGitHubSignatureAnyRotationMatrix(t *testing.T) {
	newSecret := []byte("brand-new-secret")
	oldSecret := []byte("old-compromised-window")
	body := []byte(`{"action":"opened","zen":"keep it simple"}`)

	newSig := signBody(newSecret, body)
	oldSig := signBody(oldSecret, body)
	other := signBody([]byte("unrelated"), body)

	both := [][]byte{newSecret, oldSecret}
	cases := []struct {
		name    string
		secrets [][]byte
		header  string
		want    bool
	}{
		{"new secret valid during window", both, newSig, true},
		{"old secret valid during window", both, oldSig, true},
		{"singular fallback unchanged", [][]byte{newSecret}, newSig, true},
		{"unknown signature rejected", both, other, false},
		{"empty list fails closed", nil, newSig, false},
		{"garbage header rejected", both, "sha256=zzz", false},
		{"missing prefix rejected", both, "md5=deadbeef", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyGitHubSignatureAny(tc.secrets, body, tc.header); got != tc.want {
				t.Fatalf("VerifyGitHubSignatureAny = %v, want %v", got, tc.want)
			}
		})
	}
}
