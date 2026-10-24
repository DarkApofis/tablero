-- +goose Up
-- One row per change, written in the same transaction as the change itself.
-- id is the pagination cursor: it only grows, so "id < cursor" pages back in time.
CREATE TABLE activity_log (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id uuid        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    board_id     uuid        NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    actor_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    type         text        NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_log_board_id_id_idx ON activity_log (board_id, id DESC);
-- Supports the ON DELETE CASCADE from workspaces and users.
CREATE INDEX activity_log_workspace_id_idx ON activity_log (workspace_id);
CREATE INDEX activity_log_actor_id_idx ON activity_log (actor_id);

-- +goose Down
DROP TABLE activity_log;
