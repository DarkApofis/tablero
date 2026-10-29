// Package demo seeds the demo workspace and runs the optional demo mode:
// guest accounts and the periodic reset of the demo's data.
//
// It sits beside the use cases rather than among them: it drives them (so
// the demo's tasks, moves and activity are exactly what the app itself
// produces) and adds the few storage operations they have no reason to
// offer, such as clearing a board. Seeding is idempotent: it creates what is
// missing and puts the demo board back to its initial state, keeping the
// ids of the workspace and the board (and so their URLs).
package demo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/app/auth"
	"github.com/DarkApofis/tablero/backend/internal/app/task"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Ports. The use cases' services satisfy the ones named after them; they
// must be built with a publisher that discards events (see New).

type Store interface {
	UpsertUser(ctx context.Context, email, name, passwordHash string) (domain.User, error)
	UserByEmail(ctx context.Context, email string) (domain.User, bool, error)
	AdminWorkspace(ctx context.Context, adminID domain.ID, name string) (domain.ID, bool, error)
	SetMember(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) error
	BoardByName(ctx context.Context, workspaceID domain.ID, name string) (domain.ID, bool, error)
	DeleteOtherBoards(ctx context.Context, workspaceID, keepID domain.ID) error
	ClearBoard(ctx context.Context, boardID domain.ID) error
	SetActivityTimes(ctx context.Context, boardID domain.ID, times []time.Time) error
	CountUsersSince(ctx context.Context, emailSuffix string, since time.Time) (int64, error)
	DeleteUsersBefore(ctx context.Context, emailSuffix string, before time.Time) (int64, error)
	// TryLock takes the demo's lock until the transaction ends.
	TryLock(ctx context.Context) (bool, error)
}

type TxManager interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type PasswordHasher interface {
	Hash(password string) (string, error)
}

type Workspaces interface {
	Create(ctx context.Context, creatorID domain.ID, name string) (domain.WorkspaceSummary, error)
}

type Boards interface {
	CreateBoard(ctx context.Context, acc domain.Access, name string, columns []string) (domain.Board, error)
	CreateColumn(ctx context.Context, acc domain.Access, boardID domain.ID, name string) (domain.Column, error)
}

type Labels interface {
	Create(ctx context.Context, acc domain.Access, boardID domain.ID, name, color string) (domain.Label, error)
}

type Tasks interface {
	Create(ctx context.Context, acc domain.Access, in task.CreateInput) (domain.TaskDetail, error)
	Update(ctx context.Context, acc domain.Access, taskID domain.ID, version int32, edit domain.TaskEdit) (domain.TaskDetail, error)
	Move(ctx context.Context, acc domain.Access, taskID domain.ID, in task.MoveInput) (domain.TaskDetail, error)
}

// Registrar creates accounts; *auth.Service is one.
type Registrar interface {
	Register(ctx context.Context, email, name, password string) (auth.Session, error)
}

// EventPublisher reaches the live streams: after a reset it tells them to
// reload the board.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event)
}

type Clock interface {
	Now() time.Time
}

type Deps struct {
	Store      Store
	Tx         TxManager
	Hasher     PasswordHasher
	Workspaces Workspaces
	Boards     Boards
	Labels     Labels
	Tasks      Tasks
	Registrar  Registrar
	Events     EventPublisher // nil: nobody to tell (cmd/seed)
	Clock      Clock

	// Guests: at most MaxGuests accounts younger than GuestTTL; the reset
	// deletes the older ones.
	MaxGuests int
	GuestTTL  time.Duration
}

type Service struct{ d Deps }

// New returns the service. Workspaces, Boards, Labels and Tasks must not
// publish their events: the whole seed runs in one transaction and its
// events would go out before the commit, and possibly for nothing. Once it
// commits, Seed publishes a single domain.EventReset instead.
func New(d Deps) *Service { return &Service{d: d} }

// Result is what Seed left in place.
type Result struct {
	WorkspaceID domain.ID
	BoardID     domain.ID
	Users       map[string]domain.User // by Person.Key
	Tasks       int
}

// ErrBusy: another process is seeding right now.
var ErrBusy = errors.New("demo: another seed is in progress")

// ErrNotSeeded: the demo workspace does not exist (yet).
var ErrNotSeeded = errors.New("demo: not seeded")

// ErrGuestLimit: there are already as many guests as allowed.
var ErrGuestLimit = errors.New("demo: too many guest accounts")

// Seed creates the demo users (or restores their names and passwords), the
// workspace and its members, and puts the board back to its initial state
// with a scripted history, all in one transaction.
func (s *Service) Seed(ctx context.Context) (Result, error) {
	// Hashing is slow; do it before taking any lock.
	hashes := make([]string, len(People))
	for i, p := range People {
		h, err := s.d.Hasher.Hash(p.Password)
		if err != nil {
			return Result{}, fmt.Errorf("hash password: %w", err)
		}
		hashes[i] = h
	}

	res := Result{Users: map[string]domain.User{}}
	err := s.d.Tx.InTx(ctx, func(ctx context.Context) error {
		ok, err := s.d.Store.TryLock(ctx)
		if err != nil {
			return fmt.Errorf("demo lock: %w", err)
		}
		if !ok {
			return ErrBusy
		}
		for i, p := range People {
			u, err := s.d.Store.UpsertUser(ctx, p.Email, p.Name, hashes[i])
			if err != nil {
				return fmt.Errorf("user %s: %w", p.Email, err)
			}
			res.Users[p.Key] = u
		}
		admin := res.Users["ana"]

		wsID, found, err := s.d.Store.AdminWorkspace(ctx, admin.ID, WorkspaceName)
		if err != nil {
			return fmt.Errorf("find workspace: %w", err)
		}
		if !found {
			ws, err := s.d.Workspaces.Create(ctx, admin.ID, WorkspaceName)
			if err != nil {
				return fmt.Errorf("create workspace: %w", err)
			}
			wsID = ws.ID
		}
		res.WorkspaceID = wsID
		for _, p := range People {
			if err := s.d.Store.SetMember(ctx, wsID, res.Users[p.Key].ID, p.Role); err != nil {
				return fmt.Errorf("member %s: %w", p.Email, err)
			}
		}

		acc := func(key string) domain.Access {
			for _, p := range People {
				if p.Key == key {
					return domain.Access{UserID: res.Users[key].ID, WorkspaceID: wsID, Role: p.Role}
				}
			}
			panic("demo: unknown person " + key)
		}
		boardID, found, err := s.d.Store.BoardByName(ctx, wsID, BoardName)
		if err != nil {
			return fmt.Errorf("find board: %w", err)
		}
		if !found {
			b, err := s.d.Boards.CreateBoard(ctx, acc("ana"), BoardName, nil) // columns: script()
			if err != nil {
				return fmt.Errorf("create board: %w", err)
			}
			boardID = b.ID
		}
		res.BoardID = boardID
		if err := s.d.Store.DeleteOtherBoards(ctx, wsID, boardID); err != nil {
			return fmt.Errorf("delete other boards: %w", err)
		}
		if err := s.d.Store.ClearBoard(ctx, boardID); err != nil {
			return fmt.Errorf("clear board: %w", err)
		}

		res.Tasks, err = s.script(ctx, boardID, acc, res.Users)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if s.d.Events != nil {
		s.d.Events.Publish(ctx, domain.Event{Type: domain.EventReset, WorkspaceID: res.WorkspaceID, BoardID: res.BoardID})
	}
	return res, nil
}

// script builds the board through the use cases, in the order things would
// have happened, and then dates the activity over the past days.
func (s *Service) script(ctx context.Context, boardID domain.ID, acc func(string) domain.Access, users map[string]domain.User) (int, error) {
	columns := map[string]domain.ID{}
	for _, name := range BoardColumns {
		c, err := s.d.Boards.CreateColumn(ctx, acc("ana"), boardID, name)
		if err != nil {
			return 0, fmt.Errorf("column %s: %w", name, err)
		}
		columns[name] = c.ID
	}
	labels := map[string]domain.ID{}
	for _, l := range BoardLabels {
		created, err := s.d.Labels.Create(ctx, acc("ana"), boardID, l.Name, l.Color)
		if err != nil {
			return 0, fmt.Errorf("label %s: %w", l.Name, err)
		}
		labels[l.Name] = created.ID
	}
	setup := len(BoardColumns) + len(BoardLabels) // entries logged so far

	today := s.d.Clock.Now().UTC().Truncate(24 * time.Hour)
	type made struct {
		spec    taskSpec
		id      domain.ID
		version int32
	}
	created := make([]made, 0, len(BoardTasks))
	for _, t := range BoardTasks {
		in := task.CreateInput{ColumnID: columns[t.Path[0]], Title: t.Title, Description: t.Description, Priority: &t.Priority}
		if t.Due != nil {
			d := today.AddDate(0, 0, *t.Due)
			in.DueDate = &d
		}
		if t.Assignee != "" {
			id := users[t.Assignee].ID
			in.AssigneeID = &id
		}
		for _, name := range t.Labels {
			in.LabelIDs = append(in.LabelIDs, labels[name])
		}
		d, err := s.d.Tasks.Create(ctx, acc(t.By), in)
		if err != nil {
			return 0, fmt.Errorf("task %q: %w", t.Title, err)
		}
		created = append(created, made{spec: t, id: d.ID, version: d.Version})
	}

	// Moves, one step of each task's path at a time: everything leaves
	// "Pendiente" before anything reaches "Hecho", as on a real board.
	moves := 0
	for step := 1; ; step++ {
		any := false
		for i, m := range created {
			if step >= len(m.spec.Path) {
				continue
			}
			any = true
			// Whoever is assigned moves their own work; the admin otherwise.
			actor := m.spec.Assignee
			if actor == "" || acc(actor).Role == domain.RoleViewer {
				actor = "ana"
			}
			d, err := s.d.Tasks.Move(ctx, acc(actor), m.id, task.MoveInput{ColumnID: columns[m.spec.Path[step]], Version: m.version})
			if err != nil {
				return 0, fmt.Errorf("move %q: %w", m.spec.Title, err)
			}
			created[i].version = d.Version
			moves++
		}
		if !any {
			break
		}
	}

	edits := 0
	for _, m := range created {
		e := m.spec.Edit
		if e == nil {
			continue
		}
		var edit domain.TaskEdit
		edit.Priority = e.Priority
		if e.Assignee != nil {
			id := users[*e.Assignee].ID
			edit.AssigneeID = domain.Optional[domain.ID]{Set: true, Value: &id}
		}
		if e.Archive {
			archived := true
			edit.Archived = &archived
		}
		if _, err := s.d.Tasks.Update(ctx, acc(m.spec.EditBy), m.id, m.version, edit); err != nil {
			return 0, fmt.Errorf("edit %q: %w", m.spec.Title, err)
		}
		edits++
	}

	// The board was set up ten days ago; tasks were added over the next week,
	// moved during the last few days and edited in the last hours.
	now := s.d.Clock.Now()
	var times []time.Time
	spread := func(n int, from, to time.Duration) {
		for i := range n {
			at := from
			if n > 1 {
				at = from + (to-from)*time.Duration(i)/time.Duration(n-1)
			}
			times = append(times, now.Add(-at).Truncate(time.Minute))
		}
	}
	spread(setup, 10*24*time.Hour, 10*24*time.Hour-30*time.Minute)
	spread(len(created), 9*24*time.Hour, 3*24*time.Hour)
	spread(moves, 3*24*time.Hour-5*time.Hour, 3*time.Hour)
	spread(edits, 2*time.Hour, 20*time.Minute)
	if err := s.d.Store.SetActivityTimes(ctx, boardID, times); err != nil {
		return 0, fmt.Errorf("date activity: %w", err)
	}
	return len(created), nil
}

// --- demo mode ---

// GuestSession is a guest's new session and where to send them.
type GuestSession struct {
	auth.Session
	WorkspaceID domain.ID
	BoardID     domain.ID
}

// StartGuest creates a temporary account, member of the demo workspace, and
// signs it in. ErrGuestLimit if MaxGuests were created within GuestTTL.
func (s *Service) StartGuest(ctx context.Context) (GuestSession, error) {
	n, err := s.d.Store.CountUsersSince(ctx, GuestEmailSuffix, s.d.Clock.Now().Add(-s.d.GuestTTL))
	if err != nil {
		return GuestSession{}, fmt.Errorf("count guests: %w", err)
	}
	if n >= int64(s.d.MaxGuests) {
		return GuestSession{}, ErrGuestLimit
	}

	random := make([]byte, 16)
	_, _ = rand.Read(random)
	tag := hex.EncodeToString(random)
	email := "invitado-" + tag[:12] + GuestEmailSuffix
	name := "Invitado " + strings.ToUpper(tag[:4])
	password := hex.EncodeToString(random) + tag // never shown: the session is all they get

	var out GuestSession
	err = s.d.Tx.InTx(ctx, func(ctx context.Context) error {
		admin, found, err := s.d.Store.UserByEmail(ctx, People[0].Email)
		if err != nil || !found {
			return errors.Join(ErrNotSeeded, err)
		}
		if out.WorkspaceID, found, err = s.d.Store.AdminWorkspace(ctx, admin.ID, WorkspaceName); err != nil || !found {
			return errors.Join(ErrNotSeeded, err)
		}
		if out.BoardID, found, err = s.d.Store.BoardByName(ctx, out.WorkspaceID, BoardName); err != nil || !found {
			return errors.Join(ErrNotSeeded, err)
		}
		if out.Session, err = s.d.Registrar.Register(ctx, email, name, password); err != nil {
			return fmt.Errorf("register guest: %w", err)
		}
		return s.d.Store.SetMember(ctx, out.WorkspaceID, out.User.ID, domain.RoleMember)
	})
	return out, err
}

// Reset deletes the guests older than GuestTTL and seeds the demo again.
func (s *Service) Reset(ctx context.Context) error {
	deleted, err := s.d.Store.DeleteUsersBefore(ctx, GuestEmailSuffix, s.d.Clock.Now().Add(-s.d.GuestTTL))
	if err != nil {
		return fmt.Errorf("delete guests: %w", err)
	}
	res, err := s.Seed(ctx)
	if err != nil {
		return err
	}
	slog.Info("demo reset", "guests_deleted", deleted, "board", res.BoardID.String(), "tasks", res.Tasks)
	return nil
}

// RunResets resets the demo now and then every interval until ctx ends. A
// failed reset is logged and retried at the next tick; ErrBusy means another
// instance is doing it.
func (s *Service) RunResets(ctx context.Context, interval time.Duration) {
	run := func() {
		if err := s.Reset(ctx); err != nil && ctx.Err() == nil {
			if errors.Is(err, ErrBusy) {
				slog.Info("demo reset skipped: another instance is resetting")
				return
			}
			slog.Error("demo reset failed", "err", err)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
