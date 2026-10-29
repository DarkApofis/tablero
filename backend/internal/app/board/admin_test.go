package board_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

func TestPrivateBoardIsGivenToItsCreator(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin := e.as(domain.RoleAdmin)

	b, err := e.svc.CreateBoard(ctx, admin, "Secreto", nil, domain.VisibilityPrivate)
	if err != nil || b.Visibility != domain.VisibilityPrivate {
		t.Fatalf("CreateBoard = %+v, %v", b, err)
	}
	if members, _ := e.svc.BoardMembers(ctx, admin, b.ID); len(members) != 1 || members[0].UserID != admin.UserID {
		t.Errorf("members = %+v, want its creator", members)
	}
	if open, _ := e.svc.CreateBoard(ctx, admin, "Abierto", nil, ""); open.Visibility != domain.VisibilityWorkspace {
		t.Errorf("default visibility = %q", open.Visibility)
	}
	if _, err := e.svc.CreateBoard(ctx, admin, "Mal", nil, "secret"); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("invalid visibility: err = %v", err)
	}
}

func TestListBoardsHidesPrivateOnesAndSplitsArchived(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin, member := e.as(domain.RoleAdmin), e.as(domain.RoleMember)
	_, _ = e.svc.CreateBoard(ctx, admin, "Abierto", nil, "")
	private, _ := e.svc.CreateBoard(ctx, admin, "Privado", nil, domain.VisibilityPrivate)
	archived := true
	old, _ := e.svc.CreateBoard(ctx, admin, "Viejo", nil, "")
	if _, err := e.svc.UpdateBoard(ctx, admin, old.ID, domain.BoardChanges{Archived: &archived}); err != nil {
		t.Fatal(err)
	}

	names := func(acc domain.Access, archived bool) string {
		list, _ := e.svc.ListBoards(ctx, acc, archived)
		var out []string
		for _, b := range list {
			out = append(out, b.Name)
		}
		slices.Sort(out)
		return strings.Join(out, ",")
	}
	if got := names(admin, false); got != "Abierto,Privado" {
		t.Errorf("admin sees %q", got)
	}
	if got := names(member, false); got != "Abierto" {
		t.Errorf("member sees %q", got)
	}
	if got := names(member, true); got != "Viejo" {
		t.Errorf("archived: %q", got)
	}
	// Given the private board, the member sees it too.
	if _, err := e.svc.SetBoardMembers(ctx, admin, private.ID, []domain.ID{member.UserID}); err != nil {
		t.Fatal(err)
	}
	if got := names(member, false); got != "Abierto,Privado" {
		t.Errorf("member given the board sees %q", got)
	}

}

func TestUpdateBoard(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin := e.as(domain.RoleAdmin)
	b, _ := e.svc.CreateBoard(ctx, admin, "Sprint", nil, "")

	name, private, yes, no := " Sprint 2 ", domain.VisibilityPrivate, true, false
	got, err := e.svc.UpdateBoard(ctx, admin, b.ID, domain.BoardChanges{Name: &name, Visibility: &private, Archived: &yes})
	if err != nil || got.Name != "Sprint 2" || got.Visibility != private || got.ArchivedAt == nil {
		t.Fatalf("UpdateBoard = %+v, %v", got, err)
	}
	archivedAt := *got.ArchivedAt
	// Archiving again keeps the first time; restoring clears it.
	if again, _ := e.svc.UpdateBoard(ctx, admin, b.ID, domain.BoardChanges{Archived: &yes}); !again.ArchivedAt.Equal(archivedAt) {
		t.Error("archiving again changed when it was archived")
	}
	if restored, _ := e.svc.UpdateBoard(ctx, admin, b.ID, domain.BoardChanges{Archived: &no}); restored.ArchivedAt != nil {
		t.Error("restoring left it archived")
	}

	empty, bad := " ", domain.Visibility("secret")
	if _, err := e.svc.UpdateBoard(ctx, admin, b.ID, domain.BoardChanges{Name: &empty}); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("empty name: err = %v", err)
	}
	if _, err := e.svc.UpdateBoard(ctx, admin, b.ID, domain.BoardChanges{Visibility: &bad}); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("invalid visibility: err = %v", err)
	}
	if _, err := e.svc.UpdateBoard(ctx, admin, domain.NewID(), domain.BoardChanges{Name: &name}); !errors.Is(err, domain.ErrBoardNotFound) {
		t.Errorf("unknown board: err = %v", err)
	}
}

func TestSetBoardMembersOnlyGivesItToWorkspaceMembers(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin := e.as(domain.RoleAdmin)
	b, _ := e.svc.CreateBoard(ctx, admin, "Privado", nil, domain.VisibilityPrivate)
	ana, outsider := domain.NewID(), domain.NewID()
	e.boards.outsiders[outsider] = true

	members, err := e.svc.SetBoardMembers(ctx, admin, b.ID, []domain.ID{ana, ana, admin.UserID})
	if err != nil || len(members) != 2 {
		t.Fatalf("SetBoardMembers = %+v, %v (duplicates must count once)", members, err)
	}
	if _, err := e.svc.SetBoardMembers(ctx, admin, b.ID, []domain.ID{ana, outsider}); !errors.As(err, new(*domain.ValidationError)) {
		t.Errorf("outsider: err = %v", err)
	}
	if e.tx.rollbacks == 0 {
		t.Error("refusing an outsider did not roll back")
	}
}

func TestDuplicateBoard(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin := e.as(domain.RoleAdmin)
	b, _ := e.svc.CreateBoard(ctx, admin, "Sprint", []string{"A", "B"}, domain.VisibilityPrivate)

	cp, err := e.svc.DuplicateBoard(ctx, admin, b.ID, "")
	if err != nil || cp.Name != "Sprint (copia)" || cp.ID == b.ID || cp.Visibility != domain.VisibilityPrivate || cp.WorkspaceID != e.ws {
		t.Fatalf("DuplicateBoard = %+v, %v", cp, err)
	}
	if got := e.names(t, cp.ID); got != "A,B" {
		t.Errorf("copied columns = %q", got)
	}
	if members, _ := e.svc.BoardMembers(ctx, admin, cp.ID); len(members) != 1 {
		t.Errorf("copied members = %+v", members)
	}
	long := strings.Repeat("x", domain.MaxBoardNameLen)
	lb, _ := e.svc.CreateBoard(ctx, admin, long, nil, "")
	if cp, err := e.svc.DuplicateBoard(ctx, admin, lb.ID, ""); err != nil || !strings.HasSuffix(cp.Name, " (copia)") {
		t.Errorf("long name: %q, %v", cp.Name, err)
	}
	if cp, _ := e.svc.DuplicateBoard(ctx, admin, b.ID, "Otro"); cp.Name != "Otro" {
		t.Errorf("named copy = %q", cp.Name)
	}
}

func TestMoveBoardNeedsBothWorkspacesAndPublishesAfterCommit(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	admin := e.as(domain.RoleAdmin)
	b, _ := e.svc.CreateBoard(ctx, admin, "Sprint", nil, "")
	target := domain.Access{UserID: admin.UserID, WorkspaceID: domain.NewID(), Role: domain.RoleAdmin}
	e.pub.events = nil

	if _, err := e.svc.MoveBoard(ctx, admin, domain.Access{UserID: admin.UserID, WorkspaceID: target.WorkspaceID, Role: domain.RoleMember}, b.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member in the target: err = %v", err)
	}
	moved, err := e.svc.MoveBoard(ctx, admin, target, b.ID)
	if err != nil || moved.WorkspaceID != target.WorkspaceID {
		t.Fatalf("MoveBoard = %+v, %v", moved, err)
	}
	if len(e.pub.events) != 1 || e.pub.events[0].Type != domain.EventTaskUpdated {
		t.Errorf("events = %+v, want the unassignment", e.pub.events)
	}
}
