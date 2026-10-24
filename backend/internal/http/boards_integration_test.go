package httpapi

// Integration tests for boards and columns. They run against a real Postgres
// (see testdb_test.go) because the interesting parts are SQL: ordering,
// cascades, rebalancing and row locks.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/db"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

type dbEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	q      *db.Queries
	tokens *auth.TokenIssuer
	boards *service.BoardService
	tasks  *service.TaskService
	router http.Handler
}

func newDBEnv(t *testing.T) *dbEnv {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	tokens := auth.NewTokenIssuer(testSecret, 15*time.Minute)
	boards := service.NewBoardService(pool)
	tasks := service.NewTaskService(pool)
	return &dbEnv{
		t: t, pool: pool, q: db.New(pool), tokens: tokens, boards: boards, tasks: tasks,
		router: NewRouter(Deps{
			Health:     service.NewHealthService(pool),
			Tokens:     tokens,
			Workspaces: service.NewWorkspaceService(service.NewPostgresWorkspaceStore(pool)),
			Boards:     boards,
			Tasks:      tasks,
			Labels:     service.NewLabelService(pool),
			Activity:   service.NewActivityService(pool),
		}),
	}
}

func (e *dbEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

// user inserts a user and removes it (and its memberships) when the test ends.
func (e *dbEnv) user(name string) testUser {
	e.t.Helper()
	u, err := e.q.CreateUser(context.Background(), db.CreateUserParams{
		Email: fmt.Sprintf("%s-%s@test.local", strings.ToLower(name), uuid.NewString()[:8]), Name: name, PasswordHash: "x",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _, _ = e.pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID) })
	token, _, _ := e.tokens.Issue(u.ID)
	return testUser{ID: u.ID, Token: token}
}

// workspace inserts a workspace (deleted, with its boards, when the test ends).
func (e *dbEnv) workspace(members map[testUser]db.WorkspaceRole) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	ws, err := e.q.CreateWorkspace(ctx, "Equipo")
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _, _ = e.pool.Exec(context.Background(), "DELETE FROM workspaces WHERE id = $1", ws.ID) })
	for u, role := range members {
		if err := e.q.AddWorkspaceMember(ctx, db.AddWorkspaceMemberParams{WorkspaceID: ws.ID, UserID: u.ID, Role: role}); err != nil {
			e.t.Fatal(err)
		}
	}
	return ws.ID
}

func (e *dbEnv) board(wsID uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	b, err := e.q.CreateBoard(context.Background(), db.CreateBoardParams{WorkspaceID: wsID, Name: name})
	if err != nil {
		e.t.Fatal(err)
	}
	return b.ID
}

// column appends a column through the service, like the API does.
func (e *dbEnv) column(boardID uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	c, err := e.boards.CreateColumn(context.Background(), boardID, name)
	if err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

// task inserts a task at an explicit position.
func (e *dbEnv) task(columnID uuid.UUID, title string, pos float64, by testUser) uuid.UUID {
	e.t.Helper()
	t, err := e.q.CreateTask(context.Background(), db.CreateTaskParams{
		ColumnID: columnID, Title: title, Position: pos, CreatedBy: uuid.NullUUID{UUID: by.ID, Valid: true},
		Priority: db.TaskPriorityMedium,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return t.ID
}

func (e *dbEnv) do(method, path, body string, as testUser) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if as.Token != "" {
		req.Header.Set("Authorization", "Bearer "+as.Token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *dbEnv) columnNames(boardID uuid.UUID) string {
	e.t.Helper()
	cols, err := e.q.ListColumnsByBoard(context.Background(), boardID)
	if err != nil {
		e.t.Fatal(err)
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return strings.Join(names, ",")
}

// boardFingerprint captures what a forbidden request must not change.
func (e *dbEnv) boardFingerprint(boardID uuid.UUID) string {
	e.t.Helper()
	b, err := e.q.GetBoard(context.Background(), boardID)
	if err != nil {
		return "missing"
	}
	return b.Name + "|" + e.columnNames(boardID)
}

// --- authorization ---

func TestBoardRoutesPermissionMatrix(t *testing.T) {
	type route struct {
		name, method, body string
		path               func(ws, board, col uuid.UUID) string
		write              bool
		success            int
	}
	routes := []route{
		{"list boards", "GET", "", func(ws, _, _ uuid.UUID) string { return "/workspaces/" + ws.String() + "/boards" }, false, 200},
		{"create board", "POST", `{"name":"Nuevo"}`, func(ws, _, _ uuid.UUID) string { return "/workspaces/" + ws.String() + "/boards" }, true, 201},
		{"get board", "GET", "", func(_, b, _ uuid.UUID) string { return "/boards/" + b.String() }, false, 200},
		{"rename board", "PATCH", `{"name":"Renombrado"}`, func(_, b, _ uuid.UUID) string { return "/boards/" + b.String() }, true, 200},
		{"delete board", "DELETE", "", func(_, b, _ uuid.UUID) string { return "/boards/" + b.String() }, true, 204},
		{"create column", "POST", `{"name":"Nueva"}`, func(_, b, _ uuid.UUID) string { return "/boards/" + b.String() + "/columns" }, true, 201},
		{"rename column", "PATCH", `{"name":"Renombrada"}`, func(_, _, c uuid.UUID) string { return "/columns/" + c.String() }, true, 200},
		{"delete column", "DELETE", "", func(_, _, c uuid.UUID) string { return "/columns/" + c.String() }, true, 204},
		{"move column", "POST", `{"after_id":null}`, func(_, _, c uuid.UUID) string { return "/columns/" + c.String() + "/move" }, true, 200},
	}

	for _, rt := range routes {
		for _, actor := range []string{"admin", "member", "viewer", "outsider"} {
			t.Run(rt.name+"/"+actor, func(t *testing.T) {
				e := newDBEnv(t)
				users := map[string]testUser{
					"admin": e.user("Admin"), "member": e.user("Member"),
					"viewer": e.user("Viewer"), "outsider": e.user("Outsider"),
				}
				ws := e.workspace(map[testUser]db.WorkspaceRole{
					users["admin"]: db.WorkspaceRoleAdmin, users["member"]: db.WorkspaceRoleMember, users["viewer"]: db.WorkspaceRoleViewer,
				})
				board := e.board(ws, "Sprint")
				e.column(board, "A")
				col := e.column(board, "B")
				before := e.boardFingerprint(board)

				rec := e.do(rt.method, rt.path(ws, board, col), rt.body, users[actor])

				var wantStatus int
				var wantMsg string
				switch {
				case actor == "outsider":
					wantStatus, wantMsg = http.StatusForbidden, msgNoWorkspaceAccess
				case rt.write && actor != "admin":
					wantStatus, wantMsg = http.StatusForbidden, msgRoleNotAllowed
				default:
					wantStatus = rt.success
				}
				if rec.Code != wantStatus {
					t.Fatalf("status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body)
				}
				if wantStatus == http.StatusForbidden {
					if got := decodeError(t, rec); got.Message != wantMsg {
						t.Errorf("message = %q, want %q", got.Message, wantMsg)
					}
					if after := e.boardFingerprint(board); after != before {
						t.Errorf("forbidden request changed the board: %q -> %q", before, after)
					}
				}
			})
		}
	}
}

// Outsiders get the same 403 for an existing board or column, a missing one,
// a malformed id, and one in another workspace where they are admin.
func TestBoardRoutesDoNotRevealExistence(t *testing.T) {
	e := newDBEnv(t)
	owner, outsider := e.user("Owner"), e.user("Outsider")
	ws := e.workspace(map[testUser]db.WorkspaceRole{owner: db.WorkspaceRoleAdmin})
	e.workspace(map[testUser]db.WorkspaceRole{outsider: db.WorkspaceRoleAdmin}) // outsider's own workspace
	board := e.board(ws, "Privado")
	col := e.column(board, "A")

	for _, rt := range []struct {
		method, prefix, suffix, body string
		existing                     uuid.UUID
	}{
		{"GET", "/boards/", "", "", board},
		{"PATCH", "/boards/", "", `{"name":"x"}`, board},
		{"DELETE", "/boards/", "", "", board},
		{"POST", "/boards/", "/columns", `{"name":"x"}`, board},
		{"PATCH", "/columns/", "", `{"name":"x"}`, col},
		{"DELETE", "/columns/", "", "", col},
		{"POST", "/columns/", "/move", `{"after_id":null}`, col},
	} {
		t.Run(rt.method+" "+rt.prefix+"{id}"+rt.suffix, func(t *testing.T) {
			var bodies []string
			for _, id := range []string{rt.existing.String(), uuid.NewString(), "no-es-un-uuid"} {
				rec := e.do(rt.method, rt.prefix+id+rt.suffix, rt.body, outsider)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("id %s: status = %d, want 403", id, rec.Code)
				}
				bodies = append(bodies, rec.Body.String())
			}
			if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
				t.Errorf("responses differ:\n%s", strings.Join(bodies, "\n"))
			}
		})
	}
	if got := e.boardFingerprint(board); got != "Privado|A" {
		t.Errorf("board changed: %q", got)
	}
}

// --- GET /boards/{id} ---

func TestGetBoardReturnsColumnsAndTasksOrdered(t *testing.T) {
	e := newDBEnv(t)
	admin, viewer := e.user("Admin"), e.user("Viewer")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin, viewer: db.WorkspaceRoleViewer})
	board := e.board(ws, "Sprint 1")
	todo := e.column(board, "Por hacer")
	doing := e.column(board, "En curso")
	e.column(board, "Hecho")
	// Inserted out of order on purpose.
	e.task(todo, "c", 3000, admin)
	e.task(todo, "a", 1000, admin)
	e.task(doing, "x", 5, admin)
	e.task(todo, "b", 2000, admin)

	rec := e.do("GET", "/boards/"+board.String(), "", viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	raw := rec.Body.String()

	var got struct {
		ID          uuid.UUID        `json:"id"`
		WorkspaceID uuid.UUID        `json:"workspace_id"`
		Name        string           `json:"name"`
		Role        db.WorkspaceRole `json:"role"`
		Columns     []struct {
			ID       uuid.UUID `json:"id"`
			Name     string    `json:"name"`
			Position float64   `json:"position"`
			Tasks    []struct {
				Title     string        `json:"title"`
				ColumnID  uuid.UUID     `json:"column_id"`
				Version   int32         `json:"version"`
				CreatedBy uuid.NullUUID `json:"created_by"`
			} `json:"tasks"`
		} `json:"columns"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}

	if got.ID != board || got.WorkspaceID != ws || got.Name != "Sprint 1" || got.Role != db.WorkspaceRoleViewer {
		t.Errorf("board header = %+v", got)
	}
	var names []string
	for _, c := range got.Columns {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "Por hacer,En curso,Hecho" {
		t.Fatalf("columns = %v", names)
	}
	var titles []string
	for _, tk := range got.Columns[0].Tasks {
		titles = append(titles, tk.Title)
		if tk.ColumnID != todo || tk.Version != 1 || tk.CreatedBy.UUID != admin.ID {
			t.Errorf("task = %+v", tk)
		}
	}
	if strings.Join(titles, ",") != "a,b,c" {
		t.Errorf("tasks of first column = %v, want a,b,c", titles)
	}
	if len(got.Columns[1].Tasks) != 1 || got.Columns[1].Tasks[0].Title != "x" {
		t.Errorf("second column tasks = %+v", got.Columns[1].Tasks)
	}
	if !strings.Contains(raw, `"name":"Hecho","position":3072,"created_at":`) || !strings.HasSuffix(strings.TrimSpace(raw), `"tasks":[]}]}`) {
		t.Errorf("empty column must have \"tasks\":[]; body = %s", raw)
	}
}

func TestListBoardsOnlyFromThatWorkspace(t *testing.T) {
	e := newDBEnv(t)
	ana := e.user("Ana")
	ws1 := e.workspace(map[testUser]db.WorkspaceRole{ana: db.WorkspaceRoleMember})
	ws2 := e.workspace(map[testUser]db.WorkspaceRole{ana: db.WorkspaceRoleAdmin})
	e.board(ws1, "Uno")
	e.board(ws1, "Dos")
	e.board(ws2, "Otro")

	rec := e.do("GET", "/workspaces/"+ws1.String()+"/boards", "", ana)
	var body struct {
		Boards []boardResponse `json:"boards"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Boards) != 2 || body.Boards[0].Name != "Uno" || body.Boards[1].Name != "Dos" {
		t.Errorf("boards = %+v", body.Boards)
	}
}

// --- create, rename, delete ---

func TestCreateBoardAndColumns(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})

	rec := e.do("POST", "/workspaces/"+ws.String()+"/boards", `{"name":"  Roadmap  "}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create board: %d %s", rec.Code, rec.Body)
	}
	var b boardResponse
	_ = json.NewDecoder(rec.Body).Decode(&b)
	if b.Name != "Roadmap" || b.WorkspaceID != ws {
		t.Errorf("board = %+v", b)
	}

	var positions []float64
	for _, name := range []string{"A", "B", "C"} {
		rec := e.do("POST", "/boards/"+b.ID.String()+"/columns", `{"name":"`+name+`"}`, admin)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create column: %d %s", rec.Code, rec.Body)
		}
		var c columnResponse
		_ = json.NewDecoder(rec.Body).Decode(&c)
		positions = append(positions, c.Position)
	}
	if positions[0] != 1024 || positions[1] != 2048 || positions[2] != 3072 {
		t.Errorf("new columns must be appended: positions = %v", positions)
	}
}

func TestBoardAndColumnValidation(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
	board := e.board(ws, "B")
	col := e.column(board, "C")

	long := `{"name":"` + strings.Repeat("x", 101) + `"}`
	for _, rt := range []struct{ method, path, body string }{
		{"POST", "/workspaces/" + ws.String() + "/boards", `{"name":"  "}`},
		{"POST", "/workspaces/" + ws.String() + "/boards", long},
		{"PATCH", "/boards/" + board.String(), `{}`},
		{"POST", "/boards/" + board.String() + "/columns", `{"name":""}`},
		{"PATCH", "/columns/" + col.String(), long},
	} {
		rec := e.do(rt.method, rt.path, rt.body, admin)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s %s %s: status = %d, want 422", rt.method, rt.path, rt.body, rec.Code)
			continue
		}
		if got := decodeError(t, rec); got.Fields["name"] == "" {
			t.Errorf("error = %+v", got)
		}
	}
	if got := e.boardFingerprint(board); got != "B|C" {
		t.Errorf("board changed: %q", got)
	}
}

func TestRenameBoardAndColumn(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
	board := e.board(ws, "Viejo")
	col := e.column(board, "vieja")

	if rec := e.do("PATCH", "/boards/"+board.String(), `{"name":" Nuevo "}`, admin); rec.Code != 200 {
		t.Fatalf("rename board: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("PATCH", "/columns/"+col.String(), `{"name":"nueva"}`, admin); rec.Code != 200 {
		t.Fatalf("rename column: %d %s", rec.Code, rec.Body)
	}
	if got := e.boardFingerprint(board); got != "Nuevo|nueva" {
		t.Errorf("board = %q", got)
	}
}

func TestDeleteCascades(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
	board := e.board(ws, "B")
	keep, drop := e.column(board, "keep"), e.column(board, "drop")
	e.task(keep, "k", 1, admin)
	e.task(drop, "d1", 1, admin)
	e.task(drop, "d2", 2, admin)

	countTasks := func() (n int) {
		_ = e.pool.QueryRow(context.Background(),
			"SELECT count(*) FROM tasks t JOIN columns c ON c.id = t.column_id WHERE c.board_id = $1", board).Scan(&n)
		return n
	}

	if rec := e.do("DELETE", "/columns/"+drop.String(), "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete column: %d %s", rec.Code, rec.Body)
	}
	if e.columnNames(board) != "keep" || countTasks() != 1 {
		t.Errorf("after column delete: columns=%q tasks=%d", e.columnNames(board), countTasks())
	}

	if rec := e.do("DELETE", "/boards/"+board.String(), "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete board: %d %s", rec.Code, rec.Body)
	}
	var cols int
	_ = e.pool.QueryRow(context.Background(), "SELECT count(*) FROM columns WHERE board_id = $1", board).Scan(&cols)
	if cols != 0 {
		t.Errorf("columns left after board delete: %d", cols)
	}
	// The board is gone, so the next request is indistinguishable from "no access".
	if rec := e.do("GET", "/boards/"+board.String(), "", admin); rec.Code != http.StatusForbidden {
		t.Errorf("get deleted board: status = %d, want 403", rec.Code)
	}
}

// --- reordering ---

func TestMoveColumn(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
	board := e.board(ws, "B")
	ids := map[string]uuid.UUID{}
	for _, n := range []string{"A", "B", "C", "D"} {
		ids[n] = e.column(board, n)
	}
	move := func(col string, after any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"after_id": after})
		return e.do("POST", "/columns/"+ids[col].String()+"/move", string(body), admin)
	}

	steps := []struct {
		col   string
		after any // nil or a column name
		want  string
	}{
		{"D", nil, "D,A,B,C"}, // to the start
		{"A", "C", "D,B,C,A"}, // to the end
		{"B", "A", "D,C,A,B"}, // after the last
		{"B", "D", "D,B,C,A"}, // into the middle
		{"B", "D", "D,B,C,A"}, // already there: still valid
	}
	for _, s := range steps {
		var after any
		if s.after != nil {
			after = ids[s.after.(string)]
		}
		rec := move(s.col, after)
		if rec.Code != http.StatusOK {
			t.Fatalf("move %s after %v: %d %s", s.col, s.after, rec.Code, rec.Body)
		}
		if got := e.columnNames(board); got != s.want {
			t.Fatalf("move %s after %v: order = %s, want %s", s.col, s.after, got, s.want)
		}
	}
}

func TestMoveColumnErrors(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
	board, other := e.board(ws, "B"), e.board(ws, "Otro")
	a, _ := e.column(board, "A"), e.column(board, "B")
	foreign := e.column(other, "X")
	path := "/columns/" + a.String() + "/move"

	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"missing after_id", `{}`, 422},
		{"after itself", `{"after_id":"` + a.String() + `"}`, 422},
		{"after a column of another board", `{"after_id":"` + foreign.String() + `"}`, 422},
		{"after a column that does not exist", `{"after_id":"` + uuid.NewString() + `"}`, 422},
		{"after_id not a uuid", `{"after_id":"abc"}`, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if rec := e.do("POST", path, tt.body, admin); rec.Code != tt.status {
				t.Errorf("status = %d, want %d; body = %s", rec.Code, tt.status, rec.Body)
			}
		})
	}
	if got := e.columnNames(board); got != "A,B" {
		t.Errorf("order changed: %s", got)
	}

	// Guards the message text itself: it was once saved double-encoded (UTF-8 read as ANSI).
	rec := e.do("POST", path, `{"after_id":"`+a.String()+`"}`, admin)
	if got := decodeError(t, rec).Fields["after_id"]; got != "Una columna no puede ir después de sí misma" {
		t.Errorf("message = %q", got)
	}
}

func TestMoveColumnRebalancesWhenGapIsExhausted(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
	board := e.board(ws, "B")
	a, b, c := e.column(board, "A"), e.column(board, "B"), e.column(board, "C")
	// A and B adjacent floats: no position fits between them.
	e.exec("UPDATE columns SET position = $2 WHERE id = $1", a, 1.0)
	e.exec("UPDATE columns SET position = $2 WHERE id = $1", b, math.Nextafter(1, 2))
	e.exec("UPDATE columns SET position = $2 WHERE id = $1", c, 10.0)

	rec := e.do("POST", "/columns/"+c.String()+"/move", `{"after_id":"`+a.String()+`"}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	if got := e.columnNames(board); got != "A,C,B" {
		t.Errorf("order = %s, want A,C,B", got)
	}
	cols, _ := e.q.ListColumnsByBoard(context.Background(), board)
	if cols[0].Position != 1024 || cols[1].Position != 1536 || cols[2].Position != 2048 {
		t.Errorf("positions after rebalance = %v, %v, %v", cols[0].Position, cols[1].Position, cols[2].Position)
	}
}

// Many moves into the same gap at once must still produce distinct positions:
// the board lock makes each move see the previous one.
func TestConcurrentColumnMovesKeepDistinctPositions(t *testing.T) {
	e := newDBEnv(t)
	admin := e.user("Admin")
	ws := e.workspace(map[testUser]db.WorkspaceRole{admin: db.WorkspaceRoleAdmin})
	board := e.board(ws, "B")
	first := e.column(board, "first")
	var movers []uuid.UUID
	for i := range 12 {
		movers = append(movers, e.column(board, fmt.Sprintf("m%02d", i)))
	}
	e.column(board, "last")

	var wg sync.WaitGroup
	errs := make(chan error, len(movers))
	for _, id := range movers {
		wg.Go(func() {
			if _, err := e.boards.MoveColumn(context.Background(), id, &first); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	cols, _ := e.q.ListColumnsByBoard(context.Background(), board)
	seen := map[float64]string{}
	for _, c := range cols {
		if prev, dup := seen[c.Position]; dup {
			t.Errorf("%s and %s share position %v", prev, c.Name, c.Position)
		}
		seen[c.Position] = c.Name
	}
	if cols[0].Name != "first" || cols[len(cols)-1].Name != "last" {
		t.Errorf("order = %s", e.columnNames(board))
	}
}
