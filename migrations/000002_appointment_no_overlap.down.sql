ALTER TABLE appointments DROP CONSTRAINT IF EXISTS appointments_no_overlap;

-- btree_gist is intentionally left installed: other constraints may come
-- to depend on it, and dropping an extension is not a safe inverse of
-- "CREATE EXTENSION IF NOT EXISTS".
