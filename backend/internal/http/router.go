package httpapi

import (
	"net/http"

	"github.com/DarkApofis/tablero/backend/internal/auth"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

type Deps struct {
	CORSOrigins  []string
	CookieSecure bool
	Health       *service.HealthService
	Auth         *service.AuthService
	Tokens       *auth.TokenIssuer
	Workspaces   *service.WorkspaceService
	Boards       *service.BoardService
	Tasks        *service.TaskService
}

// NewRouter registers the routes and wraps them in the middleware chain:
// request id → logging → recover → CORS → mux.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth(d.Health))

	ah := &authHandler{auth: d.Auth, cookieSecure: d.CookieSecure}
	mux.HandleFunc("POST /auth/register", ah.register)
	mux.HandleFunc("POST /auth/login", ah.login)
	mux.Handle("GET /me", requireAuth(d.Tokens, http.HandlerFunc(ah.me)))

	authed := func(h http.HandlerFunc) http.Handler { return requireAuth(d.Tokens, h) }
	// Every route inside a workspace is authorized against the caller's role there.
	guarded := func(resolve workspaceResolver, action auth.Action, h http.HandlerFunc) http.Handler {
		return requireAuth(d.Tokens, requireAccess(d.Workspaces, action, resolve, h))
	}

	wh := &workspaceHandler{ws: d.Workspaces}
	mux.Handle("POST /workspaces", authed(wh.create))
	mux.Handle("GET /workspaces", authed(wh.list))
	mux.Handle("GET /workspaces/{id}/members", guarded(workspaceFromPath, auth.ActionRead, wh.listMembers))
	mux.Handle("PATCH /workspaces/{id}/members/{userId}", guarded(workspaceFromPath, auth.ActionManageMembers, wh.changeRole))
	mux.Handle("DELETE /workspaces/{id}/members/{userId}", guarded(workspaceFromPath, auth.ActionManageMembers, wh.removeMember))

	bh := &boardHandler{boards: d.Boards}
	mux.Handle("GET /workspaces/{id}/boards", guarded(workspaceFromPath, auth.ActionRead, bh.listBoards))
	mux.Handle("POST /workspaces/{id}/boards", guarded(workspaceFromPath, auth.ActionManageBoards, bh.createBoard))
	mux.Handle("GET /boards/{id}", guarded(bh.boardWorkspace, auth.ActionRead, bh.getBoard))
	mux.Handle("PATCH /boards/{id}", guarded(bh.boardWorkspace, auth.ActionManageBoards, bh.renameBoard))
	mux.Handle("DELETE /boards/{id}", guarded(bh.boardWorkspace, auth.ActionManageBoards, bh.deleteBoard))
	mux.Handle("POST /boards/{id}/columns", guarded(bh.boardWorkspace, auth.ActionManageBoards, bh.createColumn))
	mux.Handle("PATCH /columns/{id}", guarded(bh.columnWorkspace, auth.ActionManageBoards, bh.renameColumn))
	mux.Handle("DELETE /columns/{id}", guarded(bh.columnWorkspace, auth.ActionManageBoards, bh.deleteColumn))
	mux.Handle("POST /columns/{id}/move", guarded(bh.columnWorkspace, auth.ActionManageBoards, bh.moveColumn))

	th := &taskHandler{tasks: d.Tasks}
	mux.Handle("POST /columns/{id}/tasks", guarded(bh.columnWorkspace, auth.ActionEditTasks, th.create))
	mux.Handle("GET /tasks/{id}", guarded(th.taskWorkspace, auth.ActionRead, th.get))
	mux.Handle("PATCH /tasks/{id}", guarded(th.taskWorkspace, auth.ActionEditTasks, th.update))
	mux.Handle("PATCH /tasks/{id}/move", guarded(th.taskWorkspace, auth.ActionEditTasks, th.move))
	mux.Handle("DELETE /tasks/{id}", guarded(th.taskWorkspace, auth.ActionEditTasks, th.delete))

	var h http.Handler = withJSONMuxErrors(mux)
	h = withCORS(d.CORSOrigins, h)
	h = withRecover(h)
	h = withLogging(h)
	h = withRequestID(h)
	return h
}

// withJSONMuxErrors replaces the mux's plain-text 404 and 405 replies with the
// uniform JSON error. It runs the mux's own fallback handler against a writer
// that discards the body, so headers such as Allow are kept.
func withJSONMuxErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}

		probe := &discardWriter{header: w.Header()}
		h.ServeHTTP(probe, r)
		switch probe.status {
		case http.StatusMethodNotAllowed:
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		case http.StatusNotFound, 0:
			WriteError(w, http.StatusNotFound, CodeNotFound, "route not found")
		default:
			// Redirects (e.g. path cleaning) and anything else: replay as-is.
			mux.ServeHTTP(w, r)
		}
	})
}

type discardWriter struct {
	header http.Header
	status int
}

func (d *discardWriter) Header() http.Header         { return d.header }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(status int)      { d.status = status }
