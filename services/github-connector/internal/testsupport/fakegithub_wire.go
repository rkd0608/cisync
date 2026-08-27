package testsupport

import (
	"encoding/json"
	"io"
	"net/http"
)

// The wire structs below mirror only the fields connector payloads set;
// decode tolerates absence of everything else.

type wireBody struct {
	Name       *string     `json:"name"`
	HeadSHA    *string     `json:"head_sha"`
	Status     *string     `json:"status"`
	Conclusion *string     `json:"conclusion"`
	ExternalID *string     `json:"external_id"`
	Output     *wireOutput `json:"output"`
}

type wireOutput struct {
	Title       *string          `json:"title"`
	Summary     *string          `json:"summary"`
	Annotations []WireAnnotation `json:"annotations"`
}

type wireCommentBody struct {
	Body *string `json:"body"`
}

func (f *FakeGitHub) decodeBody(r *http.Request, call *CheckCall) {
	raw, _ := io.ReadAll(r.Body)
	var body wireBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return
	}
	call.Name = derefString(body.Name)
	call.HeadSHA = derefString(body.HeadSHA)
	call.Status = derefString(body.Status)
	call.Conclusion = derefString(body.Conclusion)
	call.ExternalID = derefString(body.ExternalID)
	if body.Output != nil {
		call.Summary = derefString(body.Output.Summary)
		call.Title = derefString(body.Output.Title)
		call.Annotations = body.Output.Annotations
	}
}

// decodeCommentBody reads the {"body": ...} shape shared by the Issues API
// create/edit comment endpoints.
func decodeCommentBody(r *http.Request) string {
	raw, _ := io.ReadAll(r.Body)
	var body wireCommentBody
	if json.Unmarshal(raw, &body) != nil || body.Body == nil {
		return ""
	}
	return *body.Body
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
