package statusapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cisync.dev/cisync/github-connector/internal/store"
)

type fakeSource struct {
	statuses []store.InstallationStatus
	err      error
}

func (f *fakeSource) InstallationStatuses(context.Context, time.Duration, time.Time) ([]store.InstallationStatus, error) {
	return f.statuses, f.err
}

func TestStatusHandlerAuthMatrix(t *testing.T) {
	src := &fakeSource{statuses: []store.InstallationStatus{{
		InstallationID: 7,
		Account:        "acme",
		Suspended:      false,
		Permissions:    map[string]string{"checks": "write"},
		Repos: []store.RepoStatus{{
			Name:            "payments",
			WebhookState:    "receiving",
			LastDeliverySeq: 3,
			LastEventAt:     ptrTime(time.Now().Add(-time.Minute)),
		}},
	}}}
	h := NewHandler(src, "sekrit")
	srv := httptest.NewServer(h)
	defer srv.Close()

	cases := []struct {
		name   string
		token  string
		status int
	}{
		{"missing token", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"valid token", "Bearer sekrit", http.StatusOK},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/installations/status", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", tc.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s: status = %d want %d", tc.name, resp.StatusCode, tc.status)
		}
	}
}

// TestStatusHandlerFailsClosedWithoutToken: an unconfigured admin token means
// the endpoint never serves data.
func TestStatusHandlerFailsClosedWithoutToken(t *testing.T) {
	srv := httptest.NewServer(NewHandler(&fakeSource{}, ""))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/installations/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unconfigured token must 401, got %d", resp.StatusCode)
	}
}

func TestStatusHandlerShape(t *testing.T) {
	src := &fakeSource{statuses: []store.InstallationStatus{}}
	srv := httptest.NewServer(NewHandler(src, "t"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/installations/status", nil)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
