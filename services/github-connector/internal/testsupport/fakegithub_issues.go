// FakeGitHub Issues-comment surface: list/create/edit for the sticky report
// poster. Comments persist in a keyed store so upsert semantics can be
// asserted end-to-end (create once, patch in place, never thread).
package testsupport

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// IssueCommentCall captures one Issues comment write as seen on the wire.
type IssueCommentCall struct {
	Method string // "list" | "create" | "edit"
	Issue  int
	ID     int64 // create response / edit target
	Body   string
}

// StoredIssueComment is a live fake-side comment (edits mutate Body).
type StoredIssueComment struct {
	ID     int64
	Issue  int
	Body   string
	Author string
	Type   string // "Bot" for ours, "User" for seeded noise
}

func (f *FakeGitHub) registerIssueRoutes(mux *http.ServeMux) {
	// Exact go-github path shapes; wildcard routes beat the generic
	// "POST|PATCH /repos/" check-run subtrees by specificity.
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}/comments", f.handleIssueList)
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{number}/comments", f.handleIssueCreate)
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/issues/comments/{id}", f.handleIssueEdit)
}

func (f *FakeGitHub) handleIssueList(w http.ResponseWriter, r *http.Request) {
	number := issueNumberFromPath(r.URL.Path)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0)
	for _, c := range f.comments {
		if c.Issue == number {
			out = append(out, map[string]any{
				"id":                 c.ID,
				"body":               c.Body,
				"user":               map[string]any{"login": c.Author, "type": c.Type},
				"author_association": "NONE",
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	raw, _ := json.Marshal(out)
	_, _ = w.Write(raw)
}

func (f *FakeGitHub) handleIssueCreate(w http.ResponseWriter, r *http.Request) {
	body := decodeCommentBody(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := f.nextID
	stored := StoredIssueComment{ID: id, Issue: issueNumberFromPath(r.URL.Path),
		Body: body, Author: "cisync-app[bot]", Type: "Bot"}
	f.comments[id] = stored
	f.issueCalls = append(f.issueCalls, IssueCommentCall{Method: "create",
		Issue: stored.Issue, ID: id, Body: body})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	raw, _ := json.Marshal(map[string]any{"id": id, "body": body})
	_, _ = w.Write(raw)
}

func (f *FakeGitHub) handleIssueEdit(w http.ResponseWriter, r *http.Request) {
	tail := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	id, _ := strconv.ParseInt(tail, 10, 64)
	body := decodeCommentBody(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.comments[id]; !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	stored := f.comments[id]
	stored.Body = body
	f.comments[id] = stored
	f.issueCalls = append(f.issueCalls, IssueCommentCall{Method: "edit",
		Issue: stored.Issue, ID: id, Body: body})
	w.Header().Set("Content-Type", "application/json")
	raw, _ := json.Marshal(map[string]any{"id": id, "body": body})
	_, _ = w.Write(raw)
}

// issueNumberFromPath parses "/repos/{o}/{r}/issues/{n}[/comments...]"; a
// non-numeric tail yields 0 so callers observe the miss instead of guessing.
func issueNumberFromPath(path string) int {
	segments := strings.Split(strings.TrimPrefix(path, "/repos/"), "/")
	for _, s := range segments {
		n, err := strconv.Atoi(s)
		if err == nil {
			return n
		}
	}
	return 0
}
