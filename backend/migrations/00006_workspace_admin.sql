-- +goose Up
-- Workspace administration: an address (slug), a description, the role new
-- members get by default and an owner; boards that can be private, archived
-- and remember who made them; invitations by email and by link.
--
-- Everything is nullable or has a default, so the running version keeps
-- inserting workspaces and boards without knowing about it.

-- The address. Nullable: a workspace the previous version creates during the
-- deploy has none until an admin gives it one in its settings.
ALTER TABLE workspaces
    ADD COLUMN slug         text CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) BETWEEN 3 AND 40),
    ADD COLUMN description  text           NOT NULL DEFAULT '',
    ADD COLUMN default_role workspace_role NOT NULL DEFAULT 'member',
    ADD COLUMN owner_id     uuid REFERENCES users (id) ON DELETE SET NULL;

CREATE UNIQUE INDEX workspaces_slug_key ON workspaces (slug);
CREATE INDEX workspaces_owner_id_idx ON workspaces (owner_id);

-- Whoever created a workspace owns it.
UPDATE workspaces SET owner_id = created_by;

-- Existing workspaces get an address from their name: lower case, without
-- accents, words joined by dashes, numbered when two names collide.
WITH base AS (
    SELECT id, left(coalesce(nullif(trim(BOTH '-' FROM regexp_replace(
               translate(lower(name), 'áàäâãåéèëêíìïîóòöôõúùüûñç', 'aaaaaaeeeeiiiiooooouuuunc'),
               '[^a-z0-9]+', '-', 'g')), ''), 'workspace'), 34) AS slug
    FROM workspaces
), numbered AS (
    SELECT id, rtrim(slug, '-') AS slug, row_number() OVER (PARTITION BY rtrim(slug, '-') ORDER BY id) AS n
    FROM base
)
UPDATE workspaces w
SET slug = CASE
               WHEN length(n.slug) < 3 THEN 'workspace-' || n.n
               WHEN n.n = 1 THEN n.slug
               ELSE n.slug || '-' || n.n
           END
FROM numbered n
WHERE n.id = w.id;

CREATE TYPE board_visibility AS ENUM ('workspace', 'private');

ALTER TABLE boards
    ADD COLUMN visibility  board_visibility NOT NULL DEFAULT 'workspace',
    ADD COLUMN archived_at timestamptz,
    ADD COLUMN created_by  uuid REFERENCES users (id) ON DELETE SET NULL;

CREATE INDEX boards_created_by_idx ON boards (created_by);

-- Who may open a private board besides the workspace's admins.
CREATE TABLE board_members (
    board_id   uuid        NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (board_id, user_id)
);

CREATE INDEX board_members_user_id_idx ON board_members (user_id);

-- An invitation to an email without an account yet: it is accepted when an
-- account registers with that email, while it has not expired. No email is
-- sent; the admin tells the person.
CREATE TABLE workspace_invitations (
    id           uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid           NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    email        text           NOT NULL CHECK (email = lower(email)),
    role         workspace_role NOT NULL,
    invited_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz    NOT NULL DEFAULT now(),
    expires_at   timestamptz    NOT NULL,
    UNIQUE (workspace_id, email)
);

CREATE INDEX workspace_invitations_email_idx ON workspace_invitations (email);
CREATE INDEX workspace_invitations_invited_by_idx ON workspace_invitations (invited_by);

-- The workspace's invitation link: whoever has it may join with its role
-- until it expires. One per workspace; a new one replaces it.
CREATE TABLE workspace_invite_links (
    workspace_id uuid           PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    token        text           NOT NULL UNIQUE,
    role         workspace_role NOT NULL,
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz    NOT NULL DEFAULT now(),
    expires_at   timestamptz    NOT NULL
);

CREATE INDEX workspace_invite_links_created_by_idx ON workspace_invite_links (created_by);

-- +goose Down
DROP TABLE workspace_invite_links;
DROP TABLE workspace_invitations;
DROP TABLE board_members;
DROP INDEX boards_created_by_idx;
ALTER TABLE boards
    DROP COLUMN created_by,
    DROP COLUMN archived_at,
    DROP COLUMN visibility;
DROP TYPE board_visibility;
DROP INDEX workspaces_owner_id_idx;
DROP INDEX workspaces_slug_key;
ALTER TABLE workspaces
    DROP COLUMN owner_id,
    DROP COLUMN default_role,
    DROP COLUMN description,
    DROP COLUMN slug;
