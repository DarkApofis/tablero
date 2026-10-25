package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTextFormat(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("a_total", "A counter.")
	c.Add(3)
	v := r.CounterVec("b_total", "By type.", "type")
	v.Add("task.moved", 2)
	v.Add(`we"ird`, 1)
	r.GaugeFunc("c", "A gauge.", func() float64 { return 1.5 })
	h := r.Histogram("d_seconds", "A histogram.", []float64{0.5, 1})
	h.Observe(0.25)
	h.Observe(0.5) // on the bound: le="0.5"
	h.Observe(3)

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type = %q", ct)
	}
	want := `# HELP a_total A counter.
# TYPE a_total counter
a_total 3
# HELP b_total By type.
# TYPE b_total counter
b_total{type="task.moved"} 2
b_total{type="we\"ird"} 1
# HELP c A gauge.
# TYPE c gauge
c 1.5
# HELP d_seconds A histogram.
# TYPE d_seconds histogram
d_seconds_bucket{le="0.5"} 2
d_seconds_bucket{le="1"} 2
d_seconds_bucket{le="+Inf"} 3
d_seconds_sum 3.75
d_seconds_count 3
`
	if got := rec.Body.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRegisterTwiceReturnsTheSameMetric(t *testing.T) {
	r := NewRegistry()
	a := r.Counter("x_total", "X.")
	b := r.Counter("x_total", "X.")
	if a != b {
		t.Fatal("second registration made a new counter")
	}
	defer func() {
		if recover() == nil {
			t.Error("registering x_total as a histogram did not panic")
		}
	}()
	r.Histogram("x_total", "X.", DeliveryBuckets)
}

func TestRuntime(t *testing.T) {
	r := NewRegistry()
	RegisterRuntime(r)
	var b strings.Builder
	_ = r.WriteText(&b)
	for _, name := range []string{"go_goroutines ", "go_memstats_heap_inuse_bytes ", "go_memstats_sys_bytes "} {
		if !strings.Contains(b.String(), "\n"+name) {
			t.Errorf("no %s in\n%s", name, b.String())
		}
	}
}
