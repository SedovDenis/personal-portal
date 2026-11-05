-- Individual favorites for folders owned by someone else.
-- The owner's folders.is_favorite flag is not modified by recipients.
-- Does not alter or delete existing notes, folders, shares, or users.
BEGIN;

CREATE TABLE IF NOT EXISTS public.folder_user_favorites (
    user_id BIGINT NOT NULL REFERENCES public.portal_users(id) ON DELETE CASCADE,
    folder_id BIGINT NOT NULL REFERENCES public.folders(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, folder_id)
);

COMMIT;
