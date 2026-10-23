-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TYPE workspace_role AS ENUM ('admin', 'member', 'viewer');

CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        NOT NULL UNIQUE CHECK (email = lower(email)),
    password_hash text        NOT NULL,
    name          text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspaces (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspace_members (
    workspace_id uuid           NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid           NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         workspace_role NOT NULL DEFAULT 'member',
    created_at   timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);

-- The PK covers lookups by workspace; this one covers "workspaces of a user".
CREATE INDEX workspace_members_user_id_idx ON workspace_members (user_id);

CREATE TABLE boards (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX boards_workspace_id_idx ON boards (workspace_id);

CREATE TABLE columns (
    id         uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id   uuid             NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    name       text             NOT NULL,
    position   double precision NOT NULL,
    created_at timestamptz      NOT NULL DEFAULT now()
);

CREATE INDEX columns_board_id_position_idx ON columns (board_id, position);

CREATE TABLE tasks (
    id          uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
    column_id   uuid             NOT NULL REFERENCES columns (id) ON DELETE CASCADE,
    title       text             NOT NULL,
    description text             NOT NULL DEFAULT '',
    position    double precision NOT NULL,
    version     integer          NOT NULL DEFAULT 1,
    created_by  uuid             REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz      NOT NULL DEFAULT now(),
    updated_at  timestamptz      NOT NULL DEFAULT now()
);

CREATE INDEX tasks_column_id_position_idx ON tasks (column_id, position);
CREATE INDEX tasks_created_by_idx ON tasks (created_by);

CREATE TRIGGER tasks_set_updated_at
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE tasks;
DROP TABLE columns;
DROP TABLE boards;
DROP TABLE workspace_members;
DROP TABLE workspaces;
DROP TABLE users;
DROP TYPE workspace_role;
DROP FUNCTION set_updated_at();
