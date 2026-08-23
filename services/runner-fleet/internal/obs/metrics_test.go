package obs

import (
	"strings"
	"sync"
	"testing"
)

func TestRenderPrometheusText(t *testing.T) {
	m := New()
	m.CounterInc("fleet_claims_total", "Jobs claimed by workers")
	m.CounterInc("fleet_completions_total", "Accepted job completions", "status", "succeeded")
	m.CounterInc("fleet_completions_total", "Accepted job completions", "status", "failed")
	m.CounterInc("fleet_completions_total", "Accepted job completions", "status", "succeeded")
	m.GaugeSet("fleet_queue_depth", "Queued jobs per pool and tier", 3, "pool", "sim", "tier", "1")

	out := m.Render()
	for _, want := range []string{
		"# TYPE fleet_claims_total counter",
		`fleet_completions_total{status="succeeded"} 2`,
		`fleet_completions_total{status="failed"} 1`,
		`fleet_queue_depth{pool="sim",tier="1"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.CounterInc("c_total", "c", "l", "v")
				m.GaugeSet("g", "g", float64(j), "l", "v")
				_ = m.Render()
			}
		}()
	}
	wg.Wait()
	if !strings.Contains(m.Render(), `c_total{l="v"} 800`) {
		t.Fatalf("counter lost increments under concurrency")
	}
}

func TestEscapesLabelValues(t *testing.T) {
	m := New()
	m.GaugeSet("weird", "weird gauge", 1, "path", "q\"uote\tline")
	out := m.Render()
	if !strings.Contains(out, `weird{path="q\"uote\tline"} 1`) {
		t.Fatalf("label value not escaped correctly:\n%s", out)
	}
}
