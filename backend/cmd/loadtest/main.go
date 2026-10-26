// Command loadtest connects many SSE clients to one board while tasks are
// moved on it, and reports how long events take to reach the clients and
// how much memory the API uses (read from its GET /metrics).
//
//	go run ./cmd/loadtest -url http://localhost:8080 -clients 500 -duration 30s -rate 10
//
// It registers a throwaway user and builds its own workspace and board.
// Every client is that same user (the API has no invitations yet), so
// presence stays at one person; the cost per connection is the same. Run
// the API with SSE_MAX_PER_USER at least -clients, or it refuses the rest.
//
// Latency is measured on the client: from just before sending the move
// (PATCH /tasks/{id}/move) to the moment each client reads its task.moved.
// It includes the write's transaction, so it is an upper bound of the
// server's own tablero_sse_delivery_seconds, which is also reported.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	var cfg config
	flag.StringVar(&cfg.url, "url", "http://localhost:8080", "API base URL")
	flag.IntVar(&cfg.clients, "clients", 500, "SSE clients connected to the board")
	flag.DurationVar(&cfg.duration, "duration", 30*time.Second, "how long to keep moving tasks")
	flag.Float64Var(&cfg.rate, "rate", 10, "task moves per second")
	flag.IntVar(&cfg.tasks, "tasks", 20, "tasks on the board")
	flag.IntVar(&cfg.dialers, "dialers", 50, "clients connecting at the same time")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

type config struct {
	url      string
	clients  int
	duration time.Duration
	rate     float64
	tasks    int
	dialers  int
}

func run(ctx context.Context, cfg config) error {
	api := &apiClient{base: strings.TrimRight(cfg.url, "/"), http: &http.Client{Timeout: 10 * time.Second}}
	fmt.Printf("API %s: %d clients, %s of moves at %.0f/s\n\n", api.base, cfg.clients, cfg.duration, cfg.rate)

	before, err := api.metrics(ctx)
	if err != nil {
		return fmt.Errorf("GET /metrics (is the API running?): %w", err)
	}
	b, err := setup(ctx, api, cfg.tasks)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	fmt.Printf("board %s with %d tasks\n", b.id, len(b.tasks))

	// --- connect ---
	sent := &sentMoves{at: map[string]time.Time{}}
	streamsCtx, closeStreams := context.WithCancel(ctx)
	defer closeStreams()
	clients := make([]*sseClient, cfg.clients)
	var connected, running sync.WaitGroup
	gate := make(chan struct{}, max(cfg.dialers, 1))
	start := time.Now()
	for i := range clients {
		c := &sseClient{api: api, board: b.id, sent: sent, ready: make(chan struct{})}
		clients[i] = c
		connected.Go(func() {
			gate <- struct{}{}
			running.Go(func() { c.run(streamsCtx) })
			select {
			case <-c.ready:
			case <-streamsCtx.Done():
			}
			<-gate
		})
	}
	connected.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	fmt.Printf("%d clients connected in %s\n", cfg.clients, time.Since(start).Round(time.Millisecond))
	time.Sleep(2 * time.Second) // let the server settle
	idle, err := api.metrics(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("server reports %.0f open streams\n", idle["tablero_sse_connections"])

	// --- move tasks, sampling the server's memory meanwhile ---
	peak := idle
	sampleCtx, stopSampling := context.WithCancel(ctx)
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for t := time.NewTicker(time.Second); ; {
			select {
			case <-sampleCtx.Done():
				return
			case <-t.C:
				if m, err := api.metrics(sampleCtx); err == nil {
					peak = maxEach(peak, m)
				}
			}
		}
	}()
	moves, moveLatencies, err := moveTasks(ctx, api, b, sent, cfg)
	if err != nil {
		stopSampling()
		return err
	}
	time.Sleep(2 * time.Second) // the last events in flight
	stopSampling()
	<-sampled
	after, err := api.metrics(ctx)
	if err != nil {
		return err
	}
	peak = maxEach(peak, after)
	closeStreams()
	running.Wait() // the clients' results are theirs until they stop

	report(cfg, clients, moves, moveLatencies, before, idle, peak, after)
	return nil
}

// --- the board ---

type boardFixture struct {
	id      string
	columns [2]string
	tasks   []*task
}

type task struct {
	id      string
	column  int // index in columns
	version int32
}

func setup(ctx context.Context, api *apiClient, n int) (*boardFixture, error) {
	suffix := randomHex(6)
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if err := api.do(ctx, "POST", "/auth/register", map[string]string{
		"email": "load-" + suffix + "@example.com", "name": "Carga " + suffix, "password": "carga-" + suffix,
	}, &session, http.StatusCreated); err != nil {
		return nil, err
	}
	api.token = session.AccessToken

	var ws, board, col struct {
		ID string `json:"id"`
	}
	if err := api.do(ctx, "POST", "/workspaces", map[string]string{"name": "Carga " + suffix}, &ws, http.StatusCreated); err != nil {
		return nil, err
	}
	if err := api.do(ctx, "POST", "/workspaces/"+ws.ID+"/boards", map[string]string{"name": "Carga"}, &board, http.StatusCreated); err != nil {
		return nil, err
	}
	b := &boardFixture{id: board.ID}
	for i, name := range []string{"Por hacer", "Hecho"} {
		if err := api.do(ctx, "POST", "/boards/"+b.id+"/columns", map[string]string{"name": name}, &col, http.StatusCreated); err != nil {
			return nil, err
		}
		b.columns[i] = col.ID
	}
	for i := range n {
		var t struct {
			ID      string `json:"id"`
			Version int32  `json:"version"`
		}
		if err := api.do(ctx, "POST", "/columns/"+b.columns[0]+"/tasks", map[string]string{"title": fmt.Sprintf("Tarea %d", i+1)}, &t, http.StatusCreated); err != nil {
			return nil, err
		}
		b.tasks = append(b.tasks, &task{id: t.ID, version: t.Version})
	}
	return b, nil
}

// moveTasks moves the tasks round-robin to the other column at cfg.rate,
// for cfg.duration, one at a time. It returns how many moves succeeded and
// how long each PATCH took.
func moveTasks(ctx context.Context, api *apiClient, b *boardFixture, sent *sentMoves, cfg config) (int, []time.Duration, error) {
	interval := time.Duration(float64(time.Second) / cfg.rate)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	end := time.After(cfg.duration)
	var latencies []time.Duration
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return len(latencies), latencies, ctx.Err()
		case <-end:
			return len(latencies), latencies, nil
		case <-tick.C:
		}
		t := b.tasks[i%len(b.tasks)]
		to := 1 - t.column
		var moved struct {
			Version int32 `json:"version"`
		}
		at := time.Now()
		sent.put(t.id, t.version+1, at) // before sending: the event may beat the response
		err := api.do(ctx, "PATCH", "/tasks/"+t.id+"/move", map[string]any{"columnId": b.columns[to], "version": t.version}, &moved, http.StatusOK)
		if err != nil {
			return len(latencies), latencies, fmt.Errorf("move %s: %w", t.id, err)
		}
		latencies = append(latencies, time.Since(at))
		t.column, t.version = to, moved.Version
	}
}

// sentMoves is when each move was sent, by task and resulting version.
type sentMoves struct {
	mu sync.RWMutex
	at map[string]time.Time
}

func (s *sentMoves) put(taskID string, version int32, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at[taskID+"/"+strconv.Itoa(int(version))] = at
}

func (s *sentMoves) get(taskID string, version int32) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	at, ok := s.at[taskID+"/"+strconv.Itoa(int(version))]
	return at, ok
}

// --- SSE clients ---

type sseClient struct {
	api   *apiClient
	board string
	sent  *sentMoves
	ready chan struct{} // closed once the first stream is open
	once  sync.Once

	lastID     string
	latencies  []time.Duration // of each task.moved received
	reconnects int
	resyncs    int
	err        error // why the last stream failed, if it did
}

// run keeps a stream open until ctx ends, reconnecting with Last-Event-ID
// like EventSource does if the server closes it.
func (c *sseClient) run(ctx context.Context) {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		if attempt > 0 {
			c.reconnects++
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		if err := c.stream(ctx); err != nil && ctx.Err() == nil {
			c.err = err
		}
	}
}

func (c *sseClient) stream(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.api.base+"/boards/"+c.board+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.api.token)
	req.Header.Set("Accept", "text/event-stream")
	if c.lastID != "" {
		req.Header.Set("Last-Event-ID", c.lastID)
	}
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stream: status %d", resp.StatusCode)
	}
	c.once.Do(func() { close(c.ready) })

	r := bufio.NewReader(resp.Body)
	var id, event, data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			k, v, _ := strings.Cut(line, ":")
			v = strings.TrimPrefix(v, " ")
			switch k {
			case "id":
				id = v
			case "event":
				event = v
			case "data":
				data = v
			}
			continue
		}
		// End of a message.
		if id != "" {
			c.lastID = id
		}
		switch event {
		case "task.moved":
			c.received(data)
		case "resync":
			c.resyncs++
		}
		id, event, data = "", "", ""
	}
}

func (c *sseClient) received(data string) {
	now := time.Now()
	var ev struct {
		Payload struct {
			TaskID  string `json:"task_id"`
			Version int32  `json:"version"`
		} `json:"payload"`
	}
	if json.Unmarshal([]byte(data), &ev) != nil {
		return
	}
	if at, ok := c.sent.get(ev.Payload.TaskID, ev.Payload.Version); ok {
		c.latencies = append(c.latencies, now.Sub(at))
	}
}

// streamHTTP has no timeout (streams last) and no limit of connections.
var streamHTTP = &http.Client{Transport: &http.Transport{
	MaxIdleConnsPerHost: 1, DisableCompression: true, ResponseHeaderTimeout: 10 * time.Second,
}}

// --- report ---

func report(cfg config, clients []*sseClient, moves int, moveLatencies []time.Duration, before, idle, peak, after map[string]float64) {
	var all []time.Duration
	reconnects, resyncs, failing := 0, 0, 0
	var lastErr error
	for _, c := range clients {
		all = append(all, c.latencies...)
		reconnects += c.reconnects
		resyncs += c.resyncs
		if c.err != nil {
			failing++
			lastErr = c.err
		}
	}
	expected := moves * len(clients)

	fmt.Printf("\n== Entrega (cliente: desde el envío del PATCH hasta leer task.moved) ==\n")
	fmt.Printf("movimientos       %d (%.1f/s)\n", moves, float64(moves)/cfg.duration.Seconds())
	fmt.Printf("eventos recibidos %d de %d esperados (%.2f%%)\n", len(all), expected, 100*float64(len(all))/math.Max(float64(expected), 1))
	fmt.Printf("latencia          p50 %s  p90 %s  p99 %s  max %s\n", pct(all, 50), pct(all, 90), pct(all, 99), pct(all, 100))
	fmt.Printf("PATCH /move       p50 %s  p99 %s\n", pct(moveLatencies, 50), pct(moveLatencies, 99))
	fmt.Printf("reconexiones      %d (resync %d, clientes con error %d)\n", reconnects, resyncs, failing)
	if lastErr != nil && !errors.Is(lastErr, io.EOF) {
		fmt.Printf("último error      %v\n", lastErr)
	}

	fmt.Printf("\n== Servidor (/metrics) ==\n")
	count := after["tablero_sse_delivery_seconds_count"] - before["tablero_sse_delivery_seconds_count"]
	sum := after["tablero_sse_delivery_seconds_sum"] - before["tablero_sse_delivery_seconds_sum"]
	fmt.Printf("entrega (publicar → flush)  %0.f mensajes, media %s, p99 ≤ %s\n", count,
		time.Duration(sum/math.Max(count, 1)*float64(time.Second)).Round(time.Microsecond), histogramP99(before, after))
	fmt.Printf("eventos publicados          %.0f\n", sumPrefix(after, "tablero_events_published_total")-sumPrefix(before, "tablero_events_published_total"))
	fmt.Printf("clientes expulsados (lentos) %.0f\n", after["tablero_sse_slow_clients_dropped_total"]-before["tablero_sse_slow_clients_dropped_total"])

	fmt.Printf("\n== Memoria del servidor ==\n")
	fmt.Printf("%-24s %12s %12s %12s\n", "", "antes", "conectados", "pico carga")
	for _, m := range []struct{ label, name string }{
		{"heap en uso", "go_memstats_heap_inuse_bytes"},
		{"stacks", "go_memstats_stack_inuse_bytes"},
		{"total del SO (sys)", "go_memstats_sys_bytes"},
	} {
		fmt.Printf("%-24s %12s %12s %12s\n", m.label, mib(before[m.name]), mib(idle[m.name]), mib(peak[m.name]))
	}
	fmt.Printf("%-24s %12.0f %12.0f %12.0f\n", "goroutines", before["go_goroutines"], idle["go_goroutines"], peak["go_goroutines"])
	perConn := (idle["go_memstats_heap_inuse_bytes"] + idle["go_memstats_stack_inuse_bytes"] -
		before["go_memstats_heap_inuse_bytes"] - before["go_memstats_stack_inuse_bytes"]) / float64(len(clients))
	fmt.Printf("por conexión (heap+stack)  ≈ %.1f KiB\n", perConn/1024)
}

func pct(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[min(max(i, 0), len(s)-1)].Round(10 * time.Microsecond)
}

// histogramP99 is the smallest bucket bound under which 99% of the
// observations made during the run fall.
func histogramP99(before, after map[string]float64) string {
	const prefix = `tablero_sse_delivery_seconds_bucket{le="`
	type bucket struct{ le, n float64 }
	var bs []bucket
	for k, v := range after {
		if le, ok := strings.CutPrefix(k, prefix); ok {
			le = strings.TrimSuffix(le, `"}`)
			bound := math.Inf(1)
			if le != "+Inf" {
				bound, _ = strconv.ParseFloat(le, 64)
			}
			bs = append(bs, bucket{bound, v - before[k]})
		}
	}
	slices.SortFunc(bs, func(a, b bucket) int { return cmpFloat(a.le, b.le) })
	if len(bs) == 0 || bs[len(bs)-1].n == 0 {
		return "-"
	}
	total := bs[len(bs)-1].n
	for _, b := range bs {
		if b.n >= 0.99*total {
			if math.IsInf(b.le, 1) {
				return "+Inf"
			}
			return time.Duration(b.le * float64(time.Second)).String()
		}
	}
	return "-"
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func sumPrefix(m map[string]float64, prefix string) float64 {
	var s float64
	for k, v := range m {
		if strings.HasPrefix(k, prefix) {
			s += v
		}
	}
	return s
}

func maxEach(a, b map[string]float64) map[string]float64 {
	out := maps.Clone(a)
	for k, v := range b {
		out[k] = max(out[k], v)
	}
	return out
}

func mib(b float64) string { return fmt.Sprintf("%.1f MiB", b/(1<<20)) }

// --- HTTP ---

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func (a *apiClient) do(ctx context.Context, method, path string, body, out any, want int) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

// metrics reads GET /metrics into name{labels} → value.
func (a *apiClient) metrics(ctx context.Context) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.base+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	out := map[string]float64{}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		if v, err := strconv.ParseFloat(line[i+1:], 64); err == nil {
			out[line[:i]] = v
		}
	}
	return out, sc.Err()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
