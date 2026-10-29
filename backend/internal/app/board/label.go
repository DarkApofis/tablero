package board

import (
	"context"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// LabelService implements the label use cases. Each change is logged and
// published as a label.changed event.
type LabelService struct {
	labels LabelRepository
	tx     TxManager
	rec    recorder
}

func NewLabelService(labels LabelRepository, activity ActivityWriter, tx TxManager, events EventPublisher) *LabelService {
	return &LabelService{labels: labels, tx: tx, rec: recorder{activity: activity, events: events}}
}

// LocationOfLabel resolves a label to its board and workspace for authorization.
func (s *LabelService) LocationOfLabel(ctx context.Context, labelID domain.ID) (domain.Location, error) {
	return s.labels.LocationOfLabel(ctx, labelID)
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
	return s.change(ctx, acc, func(ctx context.Context) (domain.Label, domain.LabelChangedPayload, error) {
		l, err := s.labels.CreateLabel(ctx, boardID, *n, *c)
		return l, domain.LabelChangedPayload{Action: domain.LabelCreated, Label: domain.SnapshotOf(l)}, err
	})
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
	return s.change(ctx, acc, func(ctx context.Context) (domain.Label, domain.LabelChangedPayload, error) {
		before, err := s.labels.GetLabelForUpdate(ctx, labelID)
		if err != nil {
			return domain.Label{}, domain.LabelChangedPayload{}, err
		}
		l, err := s.labels.UpdateLabel(ctx, labelID, n, c)
		if err != nil {
			return domain.Label{}, domain.LabelChangedPayload{}, err
		}
		changes := map[string]domain.FieldChange{}
		if before.Name != l.Name {
			changes["name"] = domain.FieldChange{From: before.Name, To: l.Name}
		}
		if before.Color != l.Color {
			changes["color"] = domain.FieldChange{From: before.Color, To: l.Color}
		}
		return l, domain.LabelChangedPayload{Action: domain.LabelUpdated, Label: domain.SnapshotOf(l), Changes: changes}, nil
	})
}

// Delete removes the label from the board and from every task that had it.
// The tasks' versions do not change: the label is gone, no task was edited.
func (s *LabelService) Delete(ctx context.Context, acc domain.Access, labelID domain.ID) error {
	if err := acc.Require(domain.ActionManageLabels); err != nil {
		return err
	}
	_, err := s.change(ctx, acc, func(ctx context.Context) (domain.Label, domain.LabelChangedPayload, error) {
		l, err := s.labels.GetLabelForUpdate(ctx, labelID)
		if err != nil {
			return domain.Label{}, domain.LabelChangedPayload{}, err
		}
		if err := s.labels.DeleteLabel(ctx, labelID); err != nil {
			return domain.Label{}, domain.LabelChangedPayload{}, err
		}
		return l, domain.LabelChangedPayload{Action: domain.LabelDeleted, Label: domain.SnapshotOf(l)}, nil
	})
	return err
}

// change runs fn and logs its label.changed entry in one transaction, then
// publishes the event.
func (s *LabelService) change(ctx context.Context, acc domain.Access, fn func(ctx context.Context) (domain.Label, domain.LabelChangedPayload, error)) (domain.Label, error) {
	var label domain.Label
	var logged domain.ActivityEntry
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		l, payload, err := fn(ctx)
		if err != nil {
			return err
		}
		label = l
		logged, err = s.rec.log(ctx, acc, l.BoardID, domain.ActivityLabelChanged, payload)
		return err
	})
	if err != nil {
		return domain.Label{}, err
	}
	s.rec.publish(ctx, logged)
	return label, nil
}
