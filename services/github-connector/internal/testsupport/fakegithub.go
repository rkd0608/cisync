// Package testsupport provides the fake-GitHub httptest server used across
// connector tests (plan §7.5): installation-token minting plus Checks API
// create/update capture with injectable failure steps.
package testsupport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// CheckCall captures one Checks API write as seen on the wire.
type CheckCall struct {
	Method      string // "create" | "update"
	Owner       string
	Repo        string
	CheckRunID  int64 // update only
	Name        string
	HeadSHA     string
	Status      string
	Conclusion  string
	ExternalID  string
	Summary     string
	Title       string
	Annotations []WireAnnotation
}

// WireAnnotation mirrors the annotation fields we assert.
type WireAnnotation struct {
	Path      string `json:"path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Message   string `json:"message"`
	Title     string `json:"title,omitempty"`
}

// TokenMint captures one access-token exchange.
type TokenMint struct {
	InstallationID int64
	Body           string
}

// FailureStep makes the next check-run call answer with an injected error.
type FailureStep struct {
	Code       int
	RetryAfter string // seconds header value
	RateLimit  bool   // also send X-RateLimit-Remaining: 0 (plain rate limit)
}

// FakeGitHub is an in-process stand-in for api.github.com.
type FakeGitHub struct {
	mu         sync.Mutex
	server     *httptest.Server
	BaseURL    string
	nextID     int64
	tokens     []TokenMint
	checks     []CheckCall
	failures   []FailureStep
	comments   map[int64]StoredIssueComment
	issueCalls []IssueCommentCall
	requests   int // every check-write hit, including injected failures
}

// Requests reports total check-write HTTP hits (successes AND failures).
func (f *FakeGitHub) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// NewFakeGitHub starts the fake server and registers cleanup.
func NewFakeGitHub(t *testing.T) *FakeGitHub {
	t.Helper()
	f := &FakeGitHub{nextID: 1000, comments: make(map[int64]StoredIssueComment)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/", f.handleToken)
	mux.HandleFunc("POST /repos/", f.handleCreate)
	mux.HandleFunc("PATCH /repos/", f.handleUpdate)
	f.registerIssueRoutes(mux)
	f.server = httptest.NewServer(mux)
	f.BaseURL = f.server.URL
	t.Cleanup(f.server.Close)
	return f
}

// QueueFailure injects the next check-call failure response.
func (f *FakeGitHub) QueueFailure(step FailureStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, step)
}

// Tokens returns captured token exchanges.
func (f *FakeGitHub) Tokens() []TokenMint {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TokenMint(nil), f.tokens...)
}

// Calls returns captured check writes.
func (f *FakeGitHub) Calls() []CheckCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]CheckCall(nil), f.checks...)
}

// IssueComments returns the live fake-side comment store keyed by id.
func (f *FakeGitHub) IssueComments() map[int64]StoredIssueComment {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[int64]StoredIssueComment, len(f.comments))
	for k, v := range f.comments {
		out[k] = v
	}
	return out
}

// IssueCalls returns captured Issues comment HTTP calls, oldest first.
func (f *FakeGitHub) IssueCalls() []IssueCommentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]IssueCommentCall(nil), f.issueCalls...)
}

// SeedForeignIssueComment adds a NON-CISync comment (marker mid-body only)
// so tests can prove the poster never touches third-party comments.
func (f *FakeGitHub) SeedForeignIssueComment(issue int, body string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := f.nextID
	f.comments[id] = StoredIssueComment{ID: id, Issue: issue, Body: body,
		Author: "somehuman", Type: "User"}
	return id
}

func (f *FakeGitHub) handleToken(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/app/installations/")
	idStr, _, _ := strings.Cut(rest, "/")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.tokens = append(f.tokens, TokenMint{InstallationID: id, Body: string(raw)})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"token":"fake_token_` + idStr + `","expires_at":"` +
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `"}`))
}

func (f *FakeGitHub) popFailure() (FailureStep, bool) {
	if len(f.failures) == 0 {
		return FailureStep{}, false
	}
	step := f.failures[0]
	f.failures = f.failures[1:]
	return step, true
}

func (f *FakeGitHub) writeFailure(w http.ResponseWriter, step FailureStep) {
	if step.RetryAfter != "" {
		w.Header().Set("Retry-After", step.RetryAfter)
	}
	if step.RateLimit {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(step.Code)
	_, _ = w.Write([]byte(`{"message":"injected failure"}`))
}

// splitRepoPath parses /repos/{owner}/{rest} → owner, rest.
func splitRepoPath(path string) (string, string) {
	rest := strings.TrimPrefix(path, "/repos/")
	owner, tail, _ := strings.Cut(rest, "/")
	return owner, tail
}

func (f *FakeGitHub) handleCreate(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	if step, ok := f.popFailure(); ok {
		f.writeFailure(w, step)
		return
	}
	owner, tail := splitRepoPath(r.URL.Path) // tail: {name}/check-runs
	name := strings.TrimSuffix(tail, "/check-runs")
	call := CheckCall{Method: "create", Owner: owner, Repo: name}
	f.decodeBody(r, &call)
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	f.checks = append(f.checks, call)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id":` + strconv.FormatInt(id, 10) + `,"name":` +
		strconv.Quote(call.Name) + `,"status":"queued"}`))
}

func (f *FakeGitHub) handleUpdate(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	if step, ok := f.popFailure(); ok {
		f.writeFailure(w, step)
		return
	}
	owner, tail := splitRepoPath(r.URL.Path) // tail: {name}/check-runs/{id}
	parts := strings.Split(tail, "/")
	name := parts[0]
	id, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	call := CheckCall{Method: "update", Owner: owner, Repo: name, CheckRunID: id}
	f.decodeBody(r, &call)
	f.mu.Lock()
	f.checks = append(f.checks, call)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":` + strconv.FormatInt(id, 10) + `}`))
}
