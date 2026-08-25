package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"sauron.dev/sauron/ingest/internal/forward"
)

func extractRepo(raw []byte) string {
	var probe struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Repository.FullName
}

// composeEventKind joins the X-GitHub-Event header with the payload's action
// ("pull_request" + "opened" → "pull_request.opened"): control-plane's
// normalizer keys on event[.action] per plan §3.1 and GitHub carries the
// action only inside the JSON body, never as a header. Unparsable bodies
// degrade to the bare event name (still persisted/forwarded).
func composeEventKind(eventHeader string, raw []byte) string {
	if eventHeader == "" {
		return ""
	}
	var probe struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.Action == "" {
		return eventHeader
	}
	return eventHeader + "." + probe.Action
}

func outcomeLabel(r forward.Result) string {
	switch r {
	case forward.ResultAccepted:
		return "accepted"
	case forward.ResultUnavailable:
		return "unavailable"
	default:
		return "rejected"
	}
}

func reject(w http.ResponseWriter, code int, errCode string, message string, cause error) {
	if cause != nil {
		slog.Warn("webhook request rejected",
			slog.String("code", errCode), slog.String("err", cause.Error()))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": errCode, "message": message},
	})
}

func readBody(w http.ResponseWriter, r io.ReadCloser, capBytes int64) ([]byte, error) {
	buf, err := io.ReadAll(http.MaxBytesReader(w, r, capBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, err
		}
		return nil, errors.New("api: body read failed")
	}
	return buf, nil
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
