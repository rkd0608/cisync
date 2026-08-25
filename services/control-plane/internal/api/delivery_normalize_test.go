package api

import (
	"encoding/json"
	"fmt"
	"testing"
)

func mustPayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("fixture payload: %v", err)
	}
	return out
}

// TestNormalizeDeliveryMatrix pins the §3.1 mapping table as executable
// expectations (plan §7.1) over sanitized GitHub-shaped payloads.
func TestNormalizeDeliveryMatrix(t *testing.T) {
	tracked := []string{"main", "master"}
	prBase := `{"action":"%s","pull_request":{"number":7,"head":{"sha":"` + revA + `"},"base":{"sha":"` + revB + `","ref":"main"},"diff_url":"https://diff","title":"t"},"sender":{"login":"octocat"}}`
	cases := []struct {
		name      string
		eventKind string
		payload   string
		want      NormalizedKind // "" = unknown park
		rawLabel  string
	}{
		{"pr opened", "pull_request.opened", fmt.Sprintf(prBase, "opened"), KindPROpened, ""},
		{"pr reopened reuses mapping", "pull_request.reopened", fmt.Sprintf(prBase, "reopened"), KindPROpened, ""},
		{"pr synchronize", "pull_request.synchronize", `{"action":"synchronize","pull_request":{"number":7}}`, KindPRSynchronize, ""},
		{"pr closed", "pull_request.closed", `{"action":"closed","pull_request":{"number":7,"merged":false}}`, KindPRClosed, ""},
		{"push tracked base", "push", `{"ref":"refs/heads/main","before":"` + revB + `","after":"` + revC + `"}`, KindPushBaseAdv, ""},
		{"push master tracked", "push", `{"ref":"refs/heads/master","before":"` + revB + `","after":"` + revC + `"}`, KindPushBaseAdv, ""},
		{"push agent branch record-only", "push", `{"ref":"refs/heads/agent/int_1/x","before":"` + revB + `","after":"` + revC + `"}`, KindPushBranch, ""},
		{"branch deletion never advances base", "push", `{"ref":"refs/heads/main","before":"` + revB + `","after":"0000000000000000000000000000000000000000"}`, KindPushBranch, ""},
		{"installation deleted", "installation.deleted", `{"action":"deleted","installation":{"account":{"login":"acme"}},"repositories":[{"full_name":"acme/payments"}]}`, KindInstallDeleted, ""},
		{"installation created", "installation.created", `{"action":"created"}`, KindInstallCreated, ""},
		{"permissions accepted", "installation.new_permissions_accepted", `{"action":"new_permissions_accepted"}`, KindInstallPerms, ""},
		{"check_run rerequested", "check_run.rerequested", `{"action":"rerequested"}`, KindCheckRereq, ""},
		{"bare event recovers action from payload", "pull_request", fmt.Sprintf(prBase, "opened"), KindPROpened, ""},
		{"unknown gollum parks", "gollum.edited", `{"action":"edited"}`, "", "unknown.gollum.edited"},
		{"unknown bare parks", "fork", `{}`, "", "unknown.fork"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := normalizeDelivery(tc.eventKind, "acme/payments", mustPayload(t, tc.payload), tracked)
			got := normalizedLabel(view)
			var want string
			switch {
			case tc.want != "":
				want = string(tc.want)
			default:
				want = tc.rawLabel
			}
			if got != want {
				t.Fatalf("normalized = %q want %q", got, want)
			}
		})
	}
}

const (
	revA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	revB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	revC = "cccccccccccccccccccccccccccccccccccccccc"
)

// TestPrViewExtraction guards every payload field path the effects read.
func TestPrViewExtraction(t *testing.T) {
	view := normalizeDelivery("pull_request.opened", "acme/payments", mustPayload(t,
		`{"action":"opened","pull_request":{"number":42,"head":{"sha":"`+revA+`"},
		  "base":{"sha":"`+revB+`","ref":"main"},"diff_url":"https://github.com/acme/payments/7.diff",
		  "title":"Add checkout"},"sender":{"login":"octo"}}`),
		[]string{"main"})
	if view.PR.Number != 42 || view.PR.HeadSHA != revA || view.PR.BaseSHA != revB ||
		view.PR.BaseRef != "main" || view.PR.DiffURL == "" || view.PR.Title != "Add checkout" ||
		view.PR.Sender != "octo" {
		t.Fatalf("pr view extraction broken: %+v", view.PR)
	}
}
