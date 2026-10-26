package httpapi

// Integration tests of GET /boards/{id}/events against a real HTTP server
// (httptest.Server, not a recorder): streaming, flushing, write deadlines
// and disconnects only behave for real over a connection.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// streamServer serves the fixture's API with the given heartbeat over a real
// server whose WriteTimeout is writeTimeout (0: none).
func (f *taskFixture) streamServer(heartbeat, writeTimeout time.Duration) *httptest.Server {
	f.t.Helper()
	deps := f.deps
	deps.StreamHeartbeat = heartbeat
	srv := httptest.NewUnstartedServer(NewRouter(deps))
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	f.t.Cleanup(srv.Close)
	return srv
}

type sseStream struct {
	resp   *http.Response
	frames chan string // each SSE message (the lines between blank lines)
	// presence gets the presence messages, which next skips.
	presence chan string
	cancel   context.CancelFunc
}

// openStream connects as `as`, authenticating with the access_token cookie;
// query is appended to the URL and setup can add headers (Last-Event-ID…).
func (f *taskFixture) openStream(srv *httptest.Server, board uuid.UUID, as testUser, opts ...func(*http.Request)) *sseStream {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/boards/"+board.String()+"/events", nil)
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: as.Token})
	for _, o := range opts {
		o(req)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		f.t.Fatal(err)
	}
	s := &sseStream{resp: resp, frames: make(chan string, 64), presence: make(chan string, 64), cancel: cancel}
	f.t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	go func() {
		defer close(s.frames)
		sc := bufio.NewScanner(resp.Body)
		var frame []string
		for sc.Scan() {
			if line := sc.Text(); line != "" {
				frame = append(frame, line)
				continue
			}
			msg := strings.Join(frame, "\n")
			frame = nil
			if strings.HasPrefix(msg, "event: presence.") {
				s.presence <- msg
				continue
			}
			s.frames <- msg
		}
	}()
	return s
}

// next returns the next frame that is not a ping, failing after timeout.
func (s *sseStream) next(t *testing.T, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case fr, ok := <-s.frames:
			if !ok {
				t.Fatal("stream ended")
			}
			if fr != ": ping" {
				return fr
			}
		case <-deadline:
			t.Fatal("no frame in time")
		}
	}
}

// parseEvent splits an SSE message into its id, event and decoded data.
func parseEvent(t *testing.T, frame string) (id string, typ domain.EventType, data streamEvent) {
	t.Helper()
	for line := range strings.SplitSeq(frame, "\n") {
		k, v, _ := strings.Cut(line, ": ")
		switch k {
		case "id":
			id = v
		case "event":
			typ = domain.EventType(v)
		case "data":
			if err := json.Unmarshal([]byte(v), &data); err != nil {
				t.Fatalf("data %q: %v", v, err)
			}
		}
	}
	return id, typ, data
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEventStreamDeliversBoardChanges(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	s := f.openStream(srv, f.board, f.viewer)

	if s.resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", s.resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no",
	} {
		if got := s.resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if fr := s.next(t, time.Second); fr != "retry: 3000" {
		t.Errorf("first frame = %q, want the retry hint", fr)
	}
	waitFor(t, "the subscription", func() bool { return f.hub.Subscribers(domain.ID(f.board)) == 1 })

	// A change on another board of the workspace is not sent here.
	other := f.dbEnv.board(f.ws, "Otro")
	f.createTask(f.column(other, "X"), "allá", f.ana)

	created := f.createTask(f.todo, "aquí", f.ana)
	id, typ, data := parseEvent(t, s.next(t, 2*time.Second))
	var payload domain.TaskCreatedPayload
	_ = json.Unmarshal(data.Payload, &payload)
	if typ != domain.EventTaskCreated || id == "" || id != jsonNumber(data.ID) || data.BoardID != f.board ||
		data.ActorID == nil || *data.ActorID != f.ana.ID || payload.TaskID != domain.ID(created.ID) || payload.Title != "aquí" {
		t.Errorf("event = id %s, %s, %+v, payload %+v", id, typ, data, payload)
	}

	f.patchOK(created.ID, map[string]any{"priority": "high", "version": 1}, f.beto)
	if _, typ, _ := parseEvent(t, s.next(t, 2*time.Second)); typ != domain.EventTaskUpdated {
		t.Errorf("second event = %s", typ)
	}
	if rec := f.move(created.ID, map[string]any{"columnId": f.doing, "version": 2}, f.ana); rec.Code != http.StatusOK {
		t.Fatalf("move: %d", rec.Code)
	}
	if _, typ, _ := parseEvent(t, s.next(t, 2*time.Second)); typ != domain.EventTaskMoved {
		t.Errorf("third event = %s", typ)
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// The server's WriteTimeout is far shorter than the stream lives: the
// handler replaces it with a per-write deadline on its own connection.
func TestEventStreamOutlivesTheServerWriteTimeout(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(50*time.Millisecond, 150*time.Millisecond)
	s := f.openStream(srv, f.board, f.viewer)
	if fr := s.next(t, time.Second); fr != "retry: 3000" {
		t.Fatalf("first frame = %q", fr)
	}

	pings := 0
	for deadline := time.After(700 * time.Millisecond); ; {
		select {
		case fr, ok := <-s.frames:
			if !ok {
				t.Fatalf("stream cut after %d pings (WriteTimeout 150ms)", pings)
			}
			if fr == ": ping" {
				pings++
			}
			continue
		case <-deadline:
		}
		break
	}
	if pings < 5 {
		t.Errorf("pings = %d in 700ms with a 50ms heartbeat", pings)
	}
	// Still delivering events well past the WriteTimeout.
	f.createTask(f.todo, "tarde", f.ana)
	if _, typ, _ := parseEvent(t, s.next(t, 2*time.Second)); typ != domain.EventTaskCreated {
		t.Errorf("event after the timeout = %s", typ)
	}
}

func TestEventStreamEndsCleanlyWhenTheClientLeaves(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	s := f.openStream(srv, f.board, f.viewer)
	s.next(t, time.Second)
	board := domain.ID(f.board)
	waitFor(t, "the subscription", func() bool { return f.hub.Subscribers(board) == 1 })

	s.cancel() // the client disconnects
	waitFor(t, "the handler to unsubscribe", func() bool { return f.hub.Subscribers(board) == 0 })
	f.createTask(f.todo, "nadie escucha", f.ana) // publishing to nobody is fine
}

// Shutdown waits for open requests; with the hub closed on shutdown (as
// main.go does) the streams end instead of holding it until its deadline.
func TestEventStreamEndsOnServerShutdown(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	srv.Config.RegisterOnShutdown(f.hub.Close)
	s := f.openStream(srv, f.board, f.viewer)
	s.next(t, time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown = %v after %v; the open stream held it", err, time.Since(start))
	}
	for range s.frames { // drained: the stream ended
	}
}

// The heartbeat re-checks access: a member removed from the workspace stops
// receiving the board's events at the next ping.
func TestEventStreamClosesWhenTheMemberIsRemoved(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(50*time.Millisecond, 0)
	s := f.openStream(srv, f.board, f.viewer)
	s.next(t, time.Second)

	if rec := f.do("DELETE", "/workspaces/"+f.ws.String()+"/members/"+f.viewer.ID.String(), "", f.admin); rec.Code != http.StatusNoContent {
		t.Fatalf("remove member: %d", rec.Code)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-s.frames:
			if !ok {
				waitFor(t, "the unsubscribe", func() bool { return f.hub.Subscribers(domain.ID(f.board)) == 0 })
				return
			}
		case <-deadline:
			t.Fatal("the stream of a removed member is still open")
		}
	}
}

func TestEventStreamAuth(t *testing.T) {
	f := newTaskFixture(t)
	srv := f.streamServer(time.Minute, 0)
	get := func(board string, setup func(*http.Request)) *http.Response {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		t.Cleanup(cancel)
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/boards/"+board+"/events", nil)
		setup(req)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	cookie := func(token string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: accessCookieName, Value: token}) }
	}
	body := func(r *http.Response) errorDetail {
		var b errorBody
		_ = json.NewDecoder(r.Body).Decode(&b)
		return b.Error
	}

	if r := get(f.board.String(), func(*http.Request) {}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credentials: %d", r.StatusCode)
	}
	if r := get(f.board.String(), cookie("garbage")); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad cookie: %d", r.StatusCode)
	}
	// Outsiders get the same 403 for an existing board, a missing one and a bad id.
	var bodies []string
	for _, b := range []string{f.board.String(), uuid.NewString(), "no-es-un-uuid"} {
		r := get(b, cookie(f.outsider.Token))
		raw, _ := io.ReadAll(r.Body)
		if r.StatusCode != http.StatusForbidden {
			t.Errorf("outsider, board %s: %d", b, r.StatusCode)
		}
		bodies = append(bodies, string(raw))
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] || !strings.Contains(bodies[0], msgNoWorkspaceAccess) {
		t.Errorf("403 bodies differ or leak:\n%s", strings.Join(bodies, "\n"))
	}
	// Every role of the workspace, viewer included, may watch; Bearer works too.
	for _, u := range []testUser{f.admin, f.ana, f.viewer} {
		if r := get(f.board.String(), cookie(u.Token)); r.StatusCode != http.StatusOK {
			t.Errorf("member: %d %+v", r.StatusCode, body(r))
		}
	}
	bearer := get(f.board.String(), func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.viewer.Token) })
	if bearer.StatusCode != http.StatusOK {
		t.Errorf("bearer: %d", bearer.StatusCode)
	}
}

// The cookie only authenticates the stream: other routes still need the
// Authorization header, so a cross-site request carrying it changes nothing.
func TestAccessCookieOnlyAuthenticatesTheStream(t *testing.T) {
	f := newTaskFixture(t)
	req := httptest.NewRequest("POST", "/columns/"+f.todo.String()+"/tasks", strings.NewReader(`{"title":"CSRF"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: f.ana.Token})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || f.titles(f.todo) != "" {
		t.Errorf("cookie-only write: %d, tasks %q", rec.Code, f.titles(f.todo))
	}
}

func TestStreamLimitsPerUserAndBoard(t *testing.T) {
	f := newTaskFixture(t)
	deps := f.deps
	deps.StreamLimits = StreamLimits{PerUser: 1, PerBoard: 2}
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	status := func(as testUser) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+"/boards/"+f.board.String()+"/events", nil)
		req.AddCookie(&http.Cookie{Name: accessCookieName, Value: as.Token})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := srv.Client().Do(req.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var b errorBody
		if resp.StatusCode != http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&b)
		}
		return resp.StatusCode, b.Error.Code
	}

	first := f.openStream(srv, f.board, f.ana)
	if code, errCode := status(f.ana); code != http.StatusTooManyRequests || errCode != CodeTooManyStreams {
		t.Errorf("ana's second stream: %d %s", code, errCode)
	}
	f.openStream(srv, f.board, f.admin)
	if code, _ := status(f.viewer); code != http.StatusTooManyRequests {
		t.Errorf("third stream on a board limited to 2: %d", code)
	}
	// Closing a stream frees its place.
	first.cancel()
	waitFor(t, "ana's stream to be released", func() bool {
		code, _ := status(f.viewer)
		return code == http.StatusOK
	})
}

// The server's ReadTimeout bounds reading requests; a stream must outlive it.
func TestStreamOutlivesTheServerReadTimeout(t *testing.T) {
	f := newTaskFixture(t)
	srv := httptest.NewUnstartedServer(NewRouter(f.deps))
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	s := f.openStream(srv, f.board, f.viewer)
	if fr := s.next(t, time.Second); fr != "retry: 3000" {
		t.Fatalf("first frame %q", fr)
	}
	time.Sleep(600 * time.Millisecond) // three read timeouts
	f.createTask(f.todo, "Después del ReadTimeout", f.ana)
	if _, typ, _ := parseEvent(t, s.next(t, 2*time.Second)); typ != domain.EventTaskCreated {
		t.Errorf("got %s, want task.created", typ)
	}
}
