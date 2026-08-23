package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

type httpResult struct {
	status int
	body   []byte
	errMsg string
}

// runUnit creates one intent then its near-duplicate candidates, mirroring
// tests/scenarios/storm.ts semantics so both harnesses stay comparable.
func runUnit(cfg *config, rng *rand.Rand, unitIdx int) []opOutcome {
	outcomes := []opOutcome{}
	repo := fmt.Sprintf("acme/storm-%d", unitIdx%cfg.repos)
	intentKey := fmt.Sprintf("lg-%d-%d-%d", time.Now().UnixNano(), unitIdx, rng.Intn(1_000_000))

	body := fmt.Sprintf(`{"goal":"loadgen unit %d","repository":%q,"base":"main",
		"expected_surfaces":["services/checkout/**"],"acceptance_criteria":["lg-%d"],
		"risk":"%s"}`, unitIdx, repo, unitIdx, riskFor(rng))
	start := time.Now()
	res := postJSON(cfg, cfg.target+"/v1/change-intents", intentKey, body)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if res.status != 200 && res.status != 201 {
		outcomes = append(outcomes, opOutcome{kind: "create_intent", ok: false, ms: ms, errCl: classify(res)})
		return outcomes
	}
	outcomes = append(outcomes, opOutcome{kind: "create_intent", ok: true, ms: ms})
	intentID := jsonString(res.body, "intent_id")
	if intentID == "" {
		return append(outcomes, opOutcome{kind: "submit_candidate", ok: false, errCl: "missing_intent_id"})
	}

	for d := 0; d < cfg.dupes; d++ {
		candBody := fmt.Sprintf(`{"patch_ref":"bundle:lg-%d","head_sha":%q,"base_sha":%q,
			"changed_paths":["services/checkout/cart.go"]}`,
			unitIdx, sha40(rng), sha40(rng))
		start := time.Now()
		res := postJSON(cfg, cfg.target+"/v1/change-intents/"+intentID+"/candidates",
			fmt.Sprintf("%s-cand-%d", intentKey, d), candBody)
		ms := float64(time.Since(start).Microseconds()) / 1000
		ok := res.status == 200 || res.status == 201
		outcomes = append(outcomes, opOutcome{kind: "submit_candidate", ok: ok, ms: ms,
			errCl: ternary(ok, "", classify(res))})
	}
	return outcomes
}

func postJSON(cfg *config, url, idemKey, body string) httpResult {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return httpResult{errMsg: "newrequest:" + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+*adminToken)
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		msg := err.Error()
		if len(msg) > 80 {
			msg = msg[:80]
		}
		return httpResult{errMsg: msg}
	}
	defer func() { _ = resp.Body.Close() }()
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return httpResult{status: resp.StatusCode, body: buf}
}

func riskFor(rng *rand.Rand) string {
	if rng.Intn(2) == 0 {
		return "low"
	}
	return "medium"
}

func sha40(rng *rand.Rand) string {
	b := make([]byte, 40)
	for i := range b {
		b[i] = "0123456789abcdef"[rng.Intn(16)]
	}
	return string(b)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
