// Package metrics keeps the API's counters, gauges and histograms and serves
// them in the Prometheus text format (version 0.0.4) on GET /metrics.
//
// It is a small hand-written registry rather than client_golang: the API
// exposes a handful of metrics, and none of them needs labels beyond one.
package metrics

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Registry holds the metrics, in registration order. Safe for concurrent
// use; registering the same name twice returns the metric already there
// (and panics if it is of another kind).
type Registry struct {
	mu      sync.Mutex
	metrics []metric
	byName  map[string]metric
}

type metric interface {
	name() string
	write(w *bytes.Buffer)
}

func NewRegistry() *Registry { return &Registry{byName: map[string]metric{}} }

func register[M metric](r *Registry, m M) M {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.byName[m.name()]; ok {
		same, ok := old.(M)
		if !ok {
			panic(fmt.Sprintf("metrics: %s is already registered as another kind", m.name()))
		}
		return same
	}
	r.byName[m.name()] = m
	r.metrics = append(r.metrics, m)
	return m
}

// WriteText writes every metric in the Prometheus text format.
func (r *Registry) WriteText(w io.Writer) error {
	r.mu.Lock()
	ms := slices.Clone(r.metrics)
	r.mu.Unlock()
	var b bytes.Buffer
	for _, m := range ms {
		m.write(&b)
	}
	_, err := b.WriteTo(w)
	return err
}

// Handler serves the metrics.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = r.WriteText(w) // the scraper went away
	})
}

func header(w *bytes.Buffer, name, help, typ string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// escapeLabel escapes a label value as the text format requires.
var escapeLabel = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace

// --- counter ---

// Counter only goes up.
type Counter struct {
	n, help string
	v       atomic.Uint64
}

func (r *Registry) Counter(name, help string) *Counter {
	return register(r, &Counter{n: name, help: help})
}

func (c *Counter) Inc()          { c.v.Add(1) }
func (c *Counter) Add(n uint64)  { c.v.Add(n) }
func (c *Counter) Value() uint64 { return c.v.Load() }
func (c *Counter) name() string  { return c.n }
func (c *Counter) write(w *bytes.Buffer) {
	header(w, c.n, c.help, "counter")
	fmt.Fprintf(w, "%s %d\n", c.n, c.v.Load())
}

// CounterVec is a counter per value of one label.
type CounterVec struct {
	n, help, label string
	mu             sync.Mutex
	values         map[string]*atomic.Uint64
}

func (r *Registry) CounterVec(name, help, label string) *CounterVec {
	return register(r, &CounterVec{n: name, help: help, label: label, values: map[string]*atomic.Uint64{}})
}

func (c *CounterVec) Add(value string, n uint64) {
	c.mu.Lock()
	v := c.values[value]
	if v == nil {
		v = new(atomic.Uint64)
		c.values[value] = v
	}
	c.mu.Unlock()
	v.Add(n)
}

// Value is the count for one label value.
func (c *CounterVec) Value(value string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v := c.values[value]; v != nil {
		return v.Load()
	}
	return 0
}

func (c *CounterVec) name() string { return c.n }
func (c *CounterVec) write(w *bytes.Buffer) {
	c.mu.Lock()
	keys := make([]string, 0, len(c.values))
	for k := range c.values {
		keys = append(keys, k)
	}
	c.mu.Unlock()
	slices.Sort(keys)
	header(w, c.n, c.help, "counter")
	for _, k := range keys {
		fmt.Fprintf(w, "%s{%s=\"%s\"} %d\n", c.n, c.label, escapeLabel(k), c.Value(k))
	}
}

// --- values read at scrape time ---

type funcMetric struct {
	n, help, typ string
	fn           func() float64
}

// GaugeFunc reports fn() at each scrape.
func (r *Registry) GaugeFunc(name, help string, fn func() float64) {
	register(r, &funcMetric{n: name, help: help, typ: "gauge", fn: fn})
}

// CounterFunc reports fn(), which must only grow, at each scrape.
func (r *Registry) CounterFunc(name, help string, fn func() float64) {
	register(r, &funcMetric{n: name, help: help, typ: "counter", fn: fn})
}

func (f *funcMetric) name() string { return f.n }
func (f *funcMetric) write(w *bytes.Buffer) {
	header(w, f.n, f.help, f.typ)
	fmt.Fprintf(w, "%s %s\n", f.n, formatFloat(f.fn()))
}

// --- histogram ---

// DeliveryBuckets suit latencies from sub-millisecond to seconds.
var DeliveryBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// Histogram counts observations in cumulative buckets (upper bounds, in
// increasing order; +Inf is implicit).
type Histogram struct {
	n, help string
	bounds  []float64

	mu     sync.Mutex
	counts []uint64 // per bucket, not cumulative; the last is +Inf
	sum    float64
	total  uint64
}

func (r *Registry) Histogram(name, help string, buckets []float64) *Histogram {
	return register(r, &Histogram{n: name, help: help, bounds: buckets, counts: make([]uint64, len(buckets)+1)})
}

func (h *Histogram) Observe(v float64) {
	i, _ := slices.BinarySearch(h.bounds, v) // first bound >= v
	h.mu.Lock()
	h.counts[i]++
	h.sum += v
	h.total++
	h.mu.Unlock()
}

// ObserveSince observes the time since t, in seconds.
func (h *Histogram) ObserveSince(t time.Time) { h.Observe(time.Since(t).Seconds()) }

// Count is how many observations there have been.
func (h *Histogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

func (h *Histogram) name() string { return h.n }
func (h *Histogram) write(w *bytes.Buffer) {
	h.mu.Lock()
	counts := slices.Clone(h.counts)
	sum, total := h.sum, h.total
	h.mu.Unlock()
	header(w, h.n, h.help, "histogram")
	var cum uint64
	for i, b := range h.bounds {
		cum += counts[i]
		fmt.Fprintf(w, "%s_bucket{le=\"%s\"} %d\n", h.n, formatFloat(b), cum)
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n%s_sum %s\n%s_count %d\n", h.n, total, h.n, formatFloat(sum), h.n, total)
}

// --- runtime ---

// RegisterRuntime adds the Go runtime's goroutines and memory, read at
// each scrape (runtime.ReadMemStats stops the world for a moment, which is
// fine at scrape frequency).
func RegisterRuntime(r *Registry) {
	var (
		mu   sync.Mutex
		ms   runtime.MemStats
		read time.Time
	)
	// One ReadMemStats per scrape, shared by the memory gauges.
	mem := func(get func(*runtime.MemStats) uint64) func() float64 {
		return func() float64 {
			mu.Lock()
			defer mu.Unlock()
			if time.Since(read) > 100*time.Millisecond {
				runtime.ReadMemStats(&ms)
				read = time.Now()
			}
			return float64(get(&ms))
		}
	}
	r.GaugeFunc("go_goroutines", "Number of goroutines that currently exist.",
		func() float64 { return float64(runtime.NumGoroutine()) })
	r.GaugeFunc("go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.",
		mem(func(m *runtime.MemStats) uint64 { return m.HeapAlloc }))
	r.GaugeFunc("go_memstats_heap_inuse_bytes", "Bytes in in-use heap spans.",
		mem(func(m *runtime.MemStats) uint64 { return m.HeapInuse }))
	r.GaugeFunc("go_memstats_stack_inuse_bytes", "Bytes in stack spans (goroutine stacks).",
		mem(func(m *runtime.MemStats) uint64 { return m.StackInuse }))
	r.GaugeFunc("go_memstats_sys_bytes", "Bytes of memory obtained from the OS.",
		mem(func(m *runtime.MemStats) uint64 { return m.Sys }))
	r.CounterFunc("go_gc_cycles_total", "Completed GC cycles.",
		mem(func(m *runtime.MemStats) uint64 { return uint64(m.NumGC) }))
}
