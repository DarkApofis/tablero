package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/db"
)

// Action is something a workspace member may be allowed to do.
type Action int

const (
	// ActionRead: see the workspace, its members, boards and tasks.
	ActionRead Action = iota + 1
	// ActionEditTasks: create, move and edit tasks.
	ActionEditTasks
	// ActionManageMembers: change roles and remove members.
	ActionManageMembers
	// ActionManageSettings: rename the workspace and other settings.
	ActionManageSettings
	// ActionManageBoards: create, rename, delete and reorder boards and columns.
	ActionManageBoards
)

func (a Action) String() string {
	switch a {
	case ActionRead:
		return "read"
	case ActionEditTasks:
		return "edit_tasks"
	case ActionManageMembers:
		return "manage_members"
	case ActionManageSettings:
		return "manage_settings"
	case ActionManageBoards:
		return "manage_boards"
	}
	return "unknown"
}

// permissions is the role matrix. Anything not listed is denied.
var permissions = map[db.WorkspaceRole]map[Action]bool{
	db.WorkspaceRoleAdmin: {
		ActionRead:           true,
		ActionEditTasks:      true,
		ActionManageMembers:  true,
		ActionManageSettings: true,
		// Board structure is admin-only: the matrix gives members tasks only.
		ActionManageBoards: true,
	},
	db.WorkspaceRoleMember: {
		ActionRead:      true,
		ActionEditTasks: true,
	},
	db.WorkspaceRoleViewer: {
		ActionRead: true,
	},
}

// Can reports whether role may perform action.
func Can(role db.WorkspaceRole, action Action) bool {
	return permissions[role][action]
}

type (
	roleCtxKey        struct{}
	workspaceIDCtxKey struct{}
)

// WithWorkspaceID stores the workspace the request was authorized against.
func WithWorkspaceID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, workspaceIDCtxKey{}, id)
}

// WorkspaceID returns the workspace the request was authorized against.
func WorkspaceID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(workspaceIDCtxKey{}).(uuid.UUID)
	return id, ok
}

// WithWorkspaceRole stores the caller's role in the current workspace.
func WithWorkspaceRole(ctx context.Context, role db.WorkspaceRole) context.Context {
	return context.WithValue(ctx, roleCtxKey{}, role)
}

// WorkspaceRole returns the caller's role in the workspace of the request, if
// the route went through the workspace authorization middleware.
func WorkspaceRole(ctx context.Context) (db.WorkspaceRole, bool) {
	role, ok := ctx.Value(roleCtxKey{}).(db.WorkspaceRole)
	return role, ok
}
