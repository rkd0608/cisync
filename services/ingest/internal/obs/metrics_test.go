package obs

import (
	"strings"
	"sync"
	"testing"
)

func TestRenderPrometheusText(t *testing.T) {
	m := New()
	m.CounterInc("ingest_webhook_accepted_total", "Webhook requests accepted")
	m.CounterInc("ingest_webhook_rejected_total", "Webhook requests rejected at the edge", "reason", "bad_signature")
	m.CounterInc("ingest_webhook_rejected_total", "Webhook requests rejected at the edge", "reason", "too_large")
	m.CounterInc("ingest_webhook_rejected_total", "Webhook requests rejected at the edge", "reason", "bad_signature")
	m.GaugeSet("ingest_deliveries_pending", "Deliveries awaiting forward", 3, "status", "unforwarded")

	out := m.Render()
	for _, want := range []string{
		"# TYPE ingest_webhook_accepted_total counter",
		`ingest_webhook_rejected_total{reason="bad_signature"} 2`,
		`ingest_webhook_rejected_total{reason="too_large"} 1`,
		`ingest_deliveries_pending{status="unforwarded"} 3`,
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

func TestMultiLabelOrder(t *testing.T) {
	m := New()
	m.GaugeSet("fleet_queue_depth", "depth", 5, "pool", "sim", "tier", "0")
	out := m.Render()
	if !strings.Contains(out, `fleet_queue_depth{pool="sim",tier="0"} 5`) {
		t.Fatalf("multi-label rendering wrong:\n%s", out)
	}
}
