package board

import (
	"context"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// LabelService implements the label use cases.
type LabelService struct {
	labels LabelRepository
}

func NewLabelService(labels LabelRepository) *LabelService {
	return &LabelService{labels: labels}
}

// WorkspaceOfLabel resolves a label to its workspace for authorization.
func (s *LabelService) WorkspaceOfLabel(ctx context.Context, labelID domain.ID) (domain.ID, error) {
	return s.labels.WorkspaceOfLabel(ctx, labelID)
}

func (s *LabelService) List(ctx context.Context, acc domain.Access, boardID domain.ID) ([]domain.Label, error) {
	if err := acc.Require(domain.ActionRead); err != nil {
		return nil, err
	}
	return s.labels.ListLabels(ctx, boardID)
}

func (s *LabelService) Create(ctx context.Context, acc domain.Access, boardID domain.ID, name, color string) (domain.Label, error) {
	if err := acc.Require(domain.ActionManageLabels); err != nil {
		return domain.Label{}, err
	}
	n, c, err := domain.LabelFields(&name, &color)
	if err != nil {
		return domain.Label{}, err
	}
	return s.labels.CreateLabel(ctx, boardID, *n, *c)
}

// Update changes the name and/or color; nil keeps the current value.
func (s *LabelService) Update(ctx context.Context, acc domain.Access, labelID domain.ID, name, color *string) (domain.Label, error) {
	if err := acc.Require(domain.ActionManageLabels); err != nil {
		return domain.Label{}, err
	}
	if name == nil && color == nil {
		return domain.Label{}, domain.Invalid("body", "Indica name y/o color")
	}
	n, c, err := domain.LabelFields(name, color)
	if err != nil {
		return domain.Label{}, err
	}
	return s.labels.UpdateLabel(ctx, labelID, n, c)
}

// Delete removes the label from the board and from every task that had it.
// The tasks' versions do not change: the label is gone, no task was edited.
func (s *LabelService) Delete(ctx context.Context, acc domain.Access, labelID domain.ID) error {
	if err := acc.Require(domain.ActionManageLabels); err != nil {
		return err
	}
	return s.labels.DeleteLabel(ctx, labelID)
}
