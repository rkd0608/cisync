package api

import (
	"encoding/json"
	"net/http"
)

// errorBody keeps the v1 §4 error shape: {"error":{"code","message"}}.
func errorBody(code, message string) errorEnvelope {
	return errorEnvelope{Error: errDetail{Code: code, Message: message}}
}

type errorEnvelope struct {
	Error errDetail `json:"error"`
}

type errDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// jsonUnmarshalStrict decodes an envelope; unknown fields are ignored so the
// §4 contract can widen additively without breaking older connectors.
func jsonUnmarshalStrict(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}
