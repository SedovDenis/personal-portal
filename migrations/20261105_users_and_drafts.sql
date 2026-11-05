-- Apply only after taking a database backup. Existing notes/folders/templates are preserved.
BEGIN;
CREATE TABLE IF NOT EXISTS portal_users (
 id BIGSERIAL PRIMARY KEY,
 username TEXT NOT NULL UNIQUE CHECK (username ~ '^[a-zA-Z0-9_.-]{3,40}$'),
 display_name TEXT NOT NULL,
 password_hash TEXT NOT NULL,
 is_admin BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS portal_sessions (
 token_hash BYTEA PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES portal_users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS portal_sessions_user_idx ON portal_sessions(user_id);
CREATE INDEX IF NOT EXISTS portal_sessions_expiry_idx ON portal_sessions(expires_at);
ALTER TABLE folders ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES portal_users(id) ON DELETE RESTRICT;
ALTER TABLE note_templates ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES portal_users(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS folders_owner_idx ON folders(owner_id);
CREATE INDEX IF NOT EXISTS note_templates_owner_idx ON note_templates(owner_id);
CREATE TABLE IF NOT EXISTS folder_shares (
 folder_id BIGINT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES portal_users(id) ON DELETE CASCADE,
 PRIMARY KEY (folder_id, user_id)
);
-- A draft belongs to one user. target_id=0 means a new unsaved note/template.
CREATE TABLE IF NOT EXISTS note_drafts (
 user_id BIGINT NOT NULL REFERENCES portal_users(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('note', 'template')),
 target_id BIGINT NOT NULL DEFAULT 0 CHECK(target_id >= 0),
 title TEXT NOT NULL DEFAULT '',
 folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL,
 content TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (user_id, kind, target_id)
);
-- Share recipients can read/write notes of a shared folder, including descendants.
-- Only a folder's owner can administer its sharing/metadata.
CREATE OR REPLACE FUNCTION notes_can_access_folder(p_folder BIGINT, p_user BIGINT)
RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 WITH RECURSIVE ancestors AS (
  SELECT id, parent_id, owner_id, is_shared FROM folders WHERE id=p_folder
  UNION
  SELECT f.id, f.parent_id, f.owner_id, f.is_shared
  FROM folders f JOIN ancestors a ON a.parent_id=f.id
 )
 SELECT EXISTS (
  SELECT 1 FROM ancestors a
  WHERE a.owner_id=p_user OR (
    a.is_shared AND EXISTS (
      SELECT 1 FROM folder_shares s WHERE s.folder_id=a.id AND s.user_id=p_user
    )
  )
 );
$$;
COMMIT;
-- NEXT: create first admin using scripts/create-user.go. That command assigns
-- legacy folders/templates with NULL owner_id to that admin. Until then legacy
-- data stays inaccessible to new accounts.
