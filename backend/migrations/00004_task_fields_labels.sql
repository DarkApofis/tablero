-- +goose Up
CREATE TYPE task_priority AS ENUM ('low', 'medium', 'high', 'urgent');

ALTER TABLE tasks
    ADD COLUMN priority    task_priority NOT NULL DEFAULT 'medium',
    ADD COLUMN due_date    date,
    -- The service only assigns workspace members; removing a member unassigns them.
    ADD COLUMN assignee_id uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN archived_at timestamptz;

CREATE INDEX tasks_assignee_id_idx ON tasks (assignee_id);
CREATE INDEX tasks_due_date_idx ON tasks (due_date);

CREATE TABLE labels (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id   uuid        NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    -- Stored lowercase so "#AABBCC" and "#aabbcc" are the same color.
    color      text        NOT NULL CHECK (color ~ '^#[0-9a-f]{6}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Also serves lookups by board_id.
    UNIQUE (board_id, name)
);

-- A task only gets labels of its own board; the service enforces it.
CREATE TABLE task_labels (
    task_id  uuid NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    label_id uuid NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, label_id)
);

CREATE INDEX task_labels_label_id_idx ON task_labels (label_id);

-- +goose Down
DROP TABLE task_labels;
DROP TABLE labels;
DROP INDEX tasks_due_date_idx;
DROP INDEX tasks_assignee_id_idx;
ALTER TABLE tasks
    DROP COLUMN archived_at,
    DROP COLUMN assignee_id,
    DROP COLUMN due_date,
    DROP COLUMN priority;
DROP TYPE task_priority;
