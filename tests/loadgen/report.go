package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"
)

type reportFile struct {
	StartedAt  string         `json:"startedAt"`
	FinishedAt string         `json:"finishedAt"`
	Config     map[string]any `json:"config"`
	Totals     map[string]int `json:"totals"`
	LatencyMs  map[string]*histogramJSON
	ErrorClass map[string]int `json:"errorClasses"`
}

type histogramJSON struct {
	N     int     `json:"n"`
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

func report(cfg *config, outcomes [][]opOutcome, start time.Time) {
	totals := map[string]int{"intents_ok": 0, "candidates_ok": 0}
	errCl := map[string]int{}
	hists := map[string][]float64{"create_intent": {}, "submit_candidate": {}}
	for _, unit := range outcomes {
		for _, op := range unit {
			if op.ok {
				switch op.kind {
				case "create_intent":
					totals["intents_ok"]++
				case "submit_candidate":
					totals["candidates_ok"]++
				}
			} else if op.errCl != "" {
				errCl[op.errCl]++
			}
			if op.ms > 0 {
				hists[op.kind] = append(hists[op.kind], op.ms)
			}
		}
	}

	log.Printf("RESULT intents_ok=%d candidates_ok=%d errors=%v", totals["intents_ok"], totals["candidates_ok"], errCl)
	for kind, samples := range hists {
		if len(samples) == 0 {
			continue
		}
		log.Printf("LATENCY %s %s", kind, histJSON(samples).summary())
	}

	out := map[string]any{
		"startedAt":  start.UTC().Format(time.RFC3339),
		"finishedAt": time.Now().UTC().Format(time.RFC3339),
		"config": map[string]any{
			"target": cfg.target, "concurrency": cfg.concurrency, "units": cfg.units,
			"repos": cfg.repos, "dupes": cfg.dupes,
		},
		"totals":       totals,
		"errorClasses": errCl,
	}
	enc, _ := json.Marshal(out)
	fmt.Println(string(enc))
}

type histogramSummary = histogramJSON

func histJSON(samples []float64) *histogramJSON {
	sort.Float64s(samples)
	pick := func(p float64) float64 {
		idx := int(p * float64(len(samples)-1))
		return round1(samples[idx])
	}
	return &histogramJSON{
		N: len(samples), P50Ms: pick(0.50), P95Ms: pick(0.95),
		P99Ms: pick(0.99), MaxMs: round1(samples[len(samples)-1]),
	}
}

func (h *histogramJSON) summary() string {
	return fmt.Sprintf("n=%d p50=%.1fms p95=%.1fms p99=%.1fms max=%.1fms",
		h.N, h.P50Ms, h.P95Ms, h.P99Ms, h.MaxMs)
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func classify(res httpResult) string {
	if res.status == 0 {
		return "network_error:" + res.errMsg
	}
	return fmt.Sprintf("http_%d", res.status)
}

func jsonString(data []byte, key string) string {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return v
}
