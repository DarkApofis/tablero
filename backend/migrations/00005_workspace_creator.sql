-- +goose Up
-- Who created each workspace: everyone may create at most
-- domain.MaxCreatedWorkspaces. Nullable, so the running version keeps
-- inserting without it, and workspaces from before (or whose creator was
-- deleted) count for nobody.
ALTER TABLE workspaces
    ADD COLUMN created_by uuid REFERENCES users (id) ON DELETE SET NULL;

CREATE INDEX workspaces_created_by_idx ON workspaces (created_by);

-- +goose Down
DROP INDEX workspaces_created_by_idx;
ALTER TABLE workspaces DROP COLUMN created_by;
