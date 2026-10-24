package domain

// Role is a member's role within a workspace.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleMember, RoleViewer:
		return true
	}
	return false
}

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
	// ActionManageLabels: create, edit and delete a board's labels.
	ActionManageLabels
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
	case ActionManageLabels:
		return "manage_labels"
	}
	return "unknown"
}

// permissions is the role matrix. Anything not listed is denied.
var permissions = map[Role]map[Action]bool{
	RoleAdmin: {
		ActionRead:           true,
		ActionEditTasks:      true,
		ActionManageMembers:  true,
		ActionManageSettings: true,
		// Board structure is admin-only: the matrix gives members tasks only.
		ActionManageBoards: true,
		ActionManageLabels: true,
	},
	RoleMember: {
		ActionRead:      true,
		ActionEditTasks: true,
		// Labels are part of working with tasks, not of the board's structure.
		ActionManageLabels: true,
	},
	RoleViewer: {
		ActionRead: true,
	},
}

// Can reports whether role may perform action.
func Can(role Role, action Action) bool {
	return permissions[role][action]
}

// Access is a user's standing in one workspace, as established by the
// workspace authorizer (app/workspace.Authorizer). Every use case on
// workspace resources takes one and checks the action it needs with Require.
type Access struct {
	UserID      ID
	WorkspaceID ID
	Role        Role
}

// Require returns ErrForbidden unless the role allows action.
func (a Access) Require(action Action) error {
	if !Can(a.Role, action) {
		return ErrForbidden
	}
	return nil
}
