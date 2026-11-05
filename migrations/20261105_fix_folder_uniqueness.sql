-- PostgreSQL 15+ (Personal Portal runs PostgreSQL 17).
-- Folder names must be unique only within one owner's parent folder.
-- Different users may create folders with identical names.
-- Safe migration: transaction rolls back if existing folders conflict.
BEGIN;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'folders'
          AND column_name = 'owner_id'
    ) THEN
        RAISE EXCEPTION 'Missing folders.owner_id: apply the users migration first';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM public.folders
        GROUP BY owner_id, parent_id, lower(btrim(name))
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'Duplicate folders already exist for the same owner and parent. Resolve them before applying this migration';
    END IF;
END;
$$;

-- NULLS NOT DISTINCT also protects names in the root (parent_id IS NULL).
CREATE UNIQUE INDEX IF NOT EXISTS folders_owner_parent_name_uix
    ON public.folders (owner_id, parent_id, lower(btrim(name)))
    NULLS NOT DISTINCT;

-- The old folders_unique constraint/index ignored the owner; it prevented
-- separate accounts from using the same folder names.
DO $$
DECLARE
    constraint_type "char";
    old_definition text;
    index_is_unique boolean;
BEGIN
    SELECT c.contype INTO constraint_type
    FROM pg_constraint AS c
    WHERE c.conrelid = 'public.folders'::regclass
      AND c.conname = 'folders_unique';

    IF constraint_type IS NOT NULL THEN
        IF constraint_type <> 'u' THEN
            RAISE EXCEPTION 'folders_unique is not a UNIQUE constraint; inspect schema manually';
        END IF;
        SELECT pg_get_constraintdef(c.oid) INTO old_definition
        FROM pg_constraint AS c
        WHERE c.conrelid = 'public.folders'::regclass
          AND c.conname = 'folders_unique';
        IF old_definition ILIKE '%owner_id%' THEN
            RAISE EXCEPTION 'folders_unique already includes owner_id; inspect the reported duplicate before changing it';
        END IF;
        ALTER TABLE public.folders DROP CONSTRAINT folders_unique;
    ELSIF to_regclass('public.folders_unique') IS NOT NULL THEN
        SELECT i.indisunique INTO index_is_unique
        FROM pg_index AS i
        WHERE i.indexrelid = 'public.folders_unique'::regclass;
        IF index_is_unique IS DISTINCT FROM TRUE THEN
            RAISE EXCEPTION 'folders_unique is not a UNIQUE index; inspect schema manually';
        END IF;
        SELECT pg_get_indexdef('public.folders_unique'::regclass) INTO old_definition;
        IF old_definition ILIKE '%owner_id%' THEN
            RAISE EXCEPTION 'folders_unique already includes owner_id; inspect the reported duplicate before changing it';
        END IF;
        DROP INDEX public.folders_unique;
    END IF;
END;
$$;

COMMIT;
