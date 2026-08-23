package api

import (
	"net/http"
	"strconv"
	"strings"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// handleTailEvents implements GET /v1/events (ledger tail for agents/web).
func (s *Server) handleTailEvents(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	q := r.URL.Query()
	afterSeqStr := q.Get("after_seq")
	afterSeq, err := strconv.ParseInt(afterSeqStr, 10, 64)
	if err != nil || afterSeq < 0 {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		WriteError(w, http.StatusRequestedRangeNotSatisfiable, "validation_failed",
			"after_seq must be an integer ≥ 0", nil, nil, nil)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "416")
		return
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 || parsed > 500 {
			WriteError(w, http.StatusBadRequest, "validation_failed", "limit must be within [1,500]", nil, nil, nil)
			return
		}
		limit = parsed
	}
	var types []string
	if t := q.Get("types"); t != "" {
		for _, part := range strings.Split(t, ",") {
			if part = strings.TrimSpace(part); part != "" {
				types = append(types, part)
			}
		}
	}
	events, nextSeq, err := s.store.TailEvents(r.Context(), tenant, afterSeq, types, q.Get("aggregate"), limit)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	out := make([]domain.Event, 0, len(events))
	for _, ev := range events {
		ev.OccurredAt = ev.OccurredAt.UTC()
		out = append(out, *ev)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"events": out, "next_seq": nextSeq})
	s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
}
