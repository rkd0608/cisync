// Command loadgen drives the storm scenario from INSIDE the compose network,
// bypassing host port-forwarding (macOS Docker Desktop vpnkit collapses under
// per-port concurrency — W3 finding). Stdlib-only by design.
package main

import (
	"flag"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

var adminToken = flag.String("token", "dev_admin_token_not_for_prod", "bearer token")

type config struct {
	target      string
	concurrency int
	units       int
	repos       int
	dupes       int
	seed        int64
	timeout     time.Duration
	timeoutSec  int
}

type opOutcome struct {
	kind  string
	ok    bool
	ms    float64
	errCl string
}

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 256,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
	},
}

func main() {
	cfg := &config{}
	flag.StringVar(&cfg.target, "target", "http://control-plane:8081", "control-plane base URL")
	flag.IntVar(&cfg.concurrency, "concurrency", 500, "in-flight units")
	flag.IntVar(&cfg.units, "units", 500, "total units (intent + dupes candidates)")
	flag.IntVar(&cfg.repos, "repos", 8, "distinct repositories")
	flag.IntVar(&cfg.dupes, "dupes", 4, "near-duplicate candidates per intent")
	flag.IntVar(&cfg.timeoutSec, "timeout-sec", 30, "per-request timeout seconds")
	flag.Parse()
	cfg.timeout = time.Duration(cfg.timeoutSec) * time.Second

	log.Printf("loadgen: target=%s concurrency=%d units=%d repos=%d dupes=%d",
		cfg.target, cfg.concurrency, cfg.units, cfg.repos, cfg.dupes)

	start := time.Now()
	outcomes := make([][]opOutcome, cfg.units)
	pool := make(chan int, cfg.units)
	for i := 0; i < cfg.units; i++ {
		pool <- i
	}
	close(pool)
	var wg sync.WaitGroup
	var mu sync.Mutex
	completed := 0
	for w := 0; w < cfg.concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx, ok := <-pool
				if !ok {
					return
				}
				rng := rand.New(rand.NewSource(cfg.seed + int64(idx)))
				outcomes[idx] = runUnit(cfg, rng, idx)
				mu.Lock()
				completed++
				if completed%50 == 0 || completed == cfg.units {
					log.Printf("progress %d/%d in %.1fs", completed, cfg.units, time.Since(start).Seconds())
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	report(cfg, outcomes, start)
}
