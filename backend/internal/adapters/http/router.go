package httpapi

import (
	"net/http"

	appauth "github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/app/board"
	"github.com/DarkApofis/tablero/backend/internal/app/workspace"
	"github.com/DarkApofis/tablero/backend/internal/domain"
	"github.com/DarkApofis/tablero/backend/internal/service"
)

type Deps struct {
	CORSOrigins  []string
	CookieSecure bool
	Health       Pinger
	Auth         *appauth.Service
	Authorizer   *workspace.Authorizer
	Workspaces   *workspace.Service
	Boards       *board.Service
	Tasks        *service.TaskService
	Labels       *board.LabelService
	Activity     *board.ActivityService
}

// NewRouter registers the routes and wraps them in the middleware chain:
// request id → logging → recover → CORS → mux.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth(d.Health))

	ah := &authHandler{auth: d.Auth, cookieSecure: d.CookieSecure}
	mux.HandleFunc("POST /auth/register", ah.register)
	mux.HandleFunc("POST /auth/login", ah.login)
	mux.Handle("GET /me", requireAuth(d.Auth, http.HandlerFunc(ah.me)))

	authed := func(h http.HandlerFunc) http.Handler { return requireAuth(d.Auth, h) }
	// Every route inside a workspace is authorized against the caller's role there.
	guarded := func(resolve workspaceResolver, action domain.Action, h http.HandlerFunc) http.Handler {
		return requireAuth(d.Auth, requireAccess(d.Authorizer, action, resolve, h))
	}

	wh := &workspaceHandler{ws: d.Workspaces}
	mux.Handle("POST /workspaces", authed(wh.create))
	mux.Handle("GET /workspaces", authed(wh.list))
	mux.Handle("GET /workspaces/{id}/members", guarded(workspaceFromPath, domain.ActionRead, wh.listMembers))
	mux.Handle("PATCH /workspaces/{id}/members/{userId}", guarded(workspaceFromPath, domain.ActionManageMembers, wh.changeRole))
	mux.Handle("DELETE /workspaces/{id}/members/{userId}", guarded(workspaceFromPath, domain.ActionManageMembers, wh.removeMember))

	bh := &boardHandler{boards: d.Boards}
	mux.Handle("GET /workspaces/{id}/boards", guarded(workspaceFromPath, domain.ActionRead, bh.listBoards))
	mux.Handle("POST /workspaces/{id}/boards", guarded(workspaceFromPath, domain.ActionManageBoards, bh.createBoard))
	mux.Handle("GET /boards/{id}", guarded(bh.boardWorkspace, domain.ActionRead, bh.getBoard))
	mux.Handle("PATCH /boards/{id}", guarded(bh.boardWorkspace, domain.ActionManageBoards, bh.renameBoard))
	mux.Handle("DELETE /boards/{id}", guarded(bh.boardWorkspace, domain.ActionManageBoards, bh.deleteBoard))
	mux.Handle("POST /boards/{id}/columns", guarded(bh.boardWorkspace, domain.ActionManageBoards, bh.createColumn))
	mux.Handle("PATCH /columns/{id}", guarded(bh.columnWorkspace, domain.ActionManageBoards, bh.renameColumn))
	mux.Handle("DELETE /columns/{id}", guarded(bh.columnWorkspace, domain.ActionManageBoards, bh.deleteColumn))
	mux.Handle("POST /columns/{id}/move", guarded(bh.columnWorkspace, domain.ActionManageBoards, bh.moveColumn))

	ach := &activityHandler{activity: d.Activity}
	mux.Handle("GET /boards/{id}/activity", guarded(bh.boardWorkspace, domain.ActionRead, ach.list))

	lh := &labelHandler{labels: d.Labels}
	mux.Handle("GET /boards/{id}/labels", guarded(bh.boardWorkspace, domain.ActionRead, lh.list))
	mux.Handle("POST /boards/{id}/labels", guarded(bh.boardWorkspace, domain.ActionManageLabels, lh.create))
	mux.Handle("PATCH /labels/{id}", guarded(lh.labelWorkspace, domain.ActionManageLabels, lh.update))
	mux.Handle("DELETE /labels/{id}", guarded(lh.labelWorkspace, domain.ActionManageLabels, lh.delete))

	th := &taskHandler{tasks: d.Tasks}
	mux.Handle("POST /columns/{id}/tasks", guarded(bh.columnWorkspace, domain.ActionEditTasks, th.create))
	mux.Handle("GET /tasks/{id}", guarded(th.taskWorkspace, domain.ActionRead, th.get))
	mux.Handle("PATCH /tasks/{id}", guarded(th.taskWorkspace, domain.ActionEditTasks, th.update))
	mux.Handle("PATCH /tasks/{id}/move", guarded(th.taskWorkspace, domain.ActionEditTasks, th.move))
	mux.Handle("DELETE /tasks/{id}", guarded(th.taskWorkspace, domain.ActionEditTasks, th.delete))

	h := withJSONMuxErrors(mux)
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
