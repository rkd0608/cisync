package api

import (
	"sort"
	"strconv"
	"sync"
)

// Metrics is a minimal Prometheus text-format registry (counters + gauges).
type Metrics struct {
	mu      sync.Mutex
	samples map[string]float64
}

// NewMetrics constructs an empty registry.
func NewMetrics() *Metrics {
	return &Metrics{samples: map[string]float64{}}
}

// Inc bumps a counter sample by 1.
func (m *Metrics) Inc(name string, labelValues ...string) {
	m.Add(name, 1, labelValues...)
}

// Add bumps a counter by delta.
func (m *Metrics) Add(name string, delta float64, labelValues ...string) {
	key := name
	if len(labelValues) > 0 {
		key = name + "{" + joinLabels(labelValues) + "}"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples[key] += delta
}

// Set overwrites a gauge sample.
func (m *Metrics) Set(name string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples[name] = v
}

// Render produces the Prometheus text exposition of all samples.
func (m *Metrics) Render() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.samples))
	for k := range m.samples {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		out += k + " " + strconv.FormatFloat(m.samples[k], 'f', -1, 64) + "\n"
	}
	return out
}

func joinLabels(pairs []string) string {
	out := ""
	for i, p := range pairs {
		if i > 0 {
			out += ","
		}
		if i%2 == 0 {
			out += p + "=\""
		} else {
			out += p + "\""
		}
	}
	return out
}
