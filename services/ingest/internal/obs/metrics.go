// Package obs exposes a minimal Prometheus text-format registry. The allowed
// dependency list has no metrics client, so exposition is hand-rolled.
package obs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Metrics is a concurrency-safe labeled counter/gauge registry rendering the
// Prometheus text exposition format. Label arguments are name,value pairs,
// e.g. CounterInc("jobs_total", "help text", "pool", "sim").
type Metrics struct {
	mu       sync.Mutex
	families map[string]*family
}

type family struct {
	name    string
	help    string
	kind    string
	labels  []string
	samples map[string]float64
}

// New returns an empty registry.
func New() *Metrics {
	return &Metrics{families: make(map[string]*family)}
}

// CounterInc declares (once) and increments a named counter. labelPairs is an
// even-length sequence of label name,value strings.
func (m *Metrics) CounterInc(name, help string, labelPairs ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.declare(name, help, "counter", labelPairs)
	f.samples[labelKey(labelPairs)]++
}

// GaugeSet declares (once) and sets a named gauge. labelPairs follows the same
// name,value pairing rule as CounterInc.
func (m *Metrics) GaugeSet(name, help string, value float64, labelPairs ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.declare(name, help, "gauge", labelPairs)
	f.samples[labelKey(labelPairs)] = value
}

func (m *Metrics) declare(name, help, kind string, labelPairs []string) *family {
	f, ok := m.families[name]
	if !ok {
		labels := labelNames(labelPairs)
		f = &family{name: name, help: help, kind: kind, labels: labels, samples: make(map[string]float64)}
		m.families[name] = f
	}
	key := labelKey(labelPairs)
	if _, ok := f.samples[key]; !ok {
		f.samples[key] = 0
	}
	return f
}

func labelNames(pairs []string) []string {
	names := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		names = append(names, pairs[i])
	}
	return names
}

func labelValues(pairs []string) []string {
	values := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		values = append(values, pairs[i+1])
	}
	return values
}

func labelKey(pairs []string) string {
	return strings.Join(labelValues(pairs), "\x00")
}

// Render produces the Prometheus text exposition of every registered series.
func (m *Metrics) Render() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.families))
	for n := range m.families {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		f := m.families[n]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", f.name, f.help, f.name, f.kind)
		keys := make([]string, 0, len(f.samples))
		for k := range f.samples {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s%s %s\n", f.name, renderLabels(f.labels, k), formatValue(f.samples[k]))
		}
	}
	return b.String()
}

func renderLabels(labels []string, joinedValues string) string {
	if len(labels) == 0 {
		return ""
	}
	values := strings.Split(joinedValues, "\x00")
	var b strings.Builder
	b.WriteByte('{')
	for i, name := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		var v string
		if i < len(values) {
			v = values[i]
		}
		fmt.Fprintf(&b, "%s=%q", name, v)
	}
	b.WriteByte('}')
	return b.String()
}

func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
