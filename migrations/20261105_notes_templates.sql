-- Add the template library without touching existing notes/folders or their contents.
BEGIN;

CREATE TABLE IF NOT EXISTS note_templates (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS notes_folder_id_idx ON notes(folder_id);
CREATE INDEX IF NOT EXISTS folders_parent_id_idx ON folders(parent_id);

COMMIT;
