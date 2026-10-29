package domain

import (
	"errors"
	"fmt"
)

// Domain errors. Adapters translate them (the HTTP adapter to status codes);
// use cases return them, possibly wrapped.
var (
	// ErrNotFound is wrapped by every "X not found" error below.
	ErrNotFound = errors.New("not found")

	ErrUserNotFound = fmt.Errorf("user %w", ErrNotFound)
	// ErrMemberNotFound: the target user of a membership change is not a member.
	ErrMemberNotFound = fmt.Errorf("member %w", ErrNotFound)
	// Board, column, task and label "not found" only surface when the resource
	// is deleted between authorization and the operation: before that, a
	// missing resource is ErrNotMember.
	ErrBoardNotFound  = fmt.Errorf("board %w", ErrNotFound)
	ErrColumnNotFound = fmt.Errorf("column %w", ErrNotFound)
	ErrTaskNotFound   = fmt.Errorf("task %w", ErrNotFound)
	ErrLabelNotFound  = fmt.Errorf("label %w", ErrNotFound)

	// ErrLabelNameTaken: the board already has a label with that name.
	ErrLabelNameTaken = errors.New("label name taken")
	// ErrInvalidCursor: a pagination cursor that this API did not hand out.
	ErrInvalidCursor = errors.New("invalid cursor")

	// ErrNotMember: the user does not belong to the workspace, or the resource
	// (workspace, board, column, task, label) does not exist. Callers must not
	// tell the two apart.
	ErrNotMember = errors.New("not a member of the workspace")
	// ErrForbidden: the user is a member but their role does not allow the action.
	ErrForbidden = errors.New("action not allowed for role")
	// ErrLastAdmin: the change would leave the workspace without an admin.
	ErrLastAdmin = errors.New("workspace must keep at least one admin")
	// ErrWorkspaceLimit: the user already created MaxCreatedWorkspaces.
	ErrWorkspaceLimit = errors.New("workspace limit reached")

	// ErrEmailTaken: an account with that email already exists.
	ErrEmailTaken = errors.New("email already registered")
	// ErrInvalidCredentials covers an unknown email and a wrong password alike.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrInvalidToken: the access token is malformed, forged or expired.
	ErrInvalidToken = errors.New("invalid token")
	// ErrInvalidRefreshToken: the refresh token is unknown, expired or revoked.
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	// ErrRefreshTokenReused: an already rotated refresh token came back, so it
	// may have been stolen; every session of its user was revoked.
	ErrRefreshTokenReused = fmt.Errorf("%w: reused", ErrInvalidRefreshToken)
)

// ValidationError maps field names to a human-readable problem.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %v", e.Fields)
}

// Invalid is a ValidationError for a single field.
func Invalid(field, message string) *ValidationError {
	return &ValidationError{Fields: map[string]string{field: message}}
}
