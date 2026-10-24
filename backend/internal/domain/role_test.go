package domain

import (
	"errors"
	"testing"
)

func TestPermissionMatrix(t *testing.T) {
	// Every role × action combination, written out so a change to the matrix
	// has to be made consciously in both places.
	want := map[Role]map[Action]bool{
		RoleAdmin: {
			ActionRead: true, ActionEditTasks: true, ActionManageMembers: true, ActionManageSettings: true, ActionManageBoards: true,
			ActionManageLabels: true,
		},
		RoleMember: {
			ActionRead: true, ActionEditTasks: true, ActionManageMembers: false, ActionManageSettings: false, ActionManageBoards: false,
			ActionManageLabels: true,
		},
		RoleViewer: {
			ActionRead: true, ActionEditTasks: false, ActionManageMembers: false, ActionManageSettings: false, ActionManageBoards: false,
			ActionManageLabels: false,
		},
	}

	actions := []Action{ActionRead, ActionEditTasks, ActionManageMembers, ActionManageSettings, ActionManageBoards, ActionManageLabels}
	for role, perms := range want {
		for _, action := range actions {
			t.Run(string(role)+"/"+action.String(), func(t *testing.T) {
				if got := Can(role, action); got != perms[action] {
					t.Errorf("Can(%s, %s) = %v, want %v", role, action, got, perms[action])
				}
				err := Access{Role: role}.Require(action)
				if perms[action] != (err == nil) || (err != nil && !errors.Is(err, ErrForbidden)) {
					t.Errorf("Require(%s, %s) = %v", role, action, err)
				}
			})
		}
	}
}

func TestUnknownRoleOrActionIsDenied(t *testing.T) {
	if Can("owner", ActionRead) {
		t.Error("unknown role was granted read")
	}
	if Can("", ActionRead) {
		t.Error("empty role was granted read")
	}
	if Can(RoleAdmin, Action(99)) {
		t.Error("admin was granted an unknown action")
	}
	if Role("owner").Valid() || !RoleViewer.Valid() {
		t.Error("Role.Valid is wrong")
	}
}
