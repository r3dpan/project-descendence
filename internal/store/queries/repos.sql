-- name: CreateRepo :one
-- Task 3.2/3.6. The bare repository on disk is created first and the row
-- second: a row pointing at a directory that does not exist is a repository
-- nothing can read, whereas a directory with no row is merely orphaned and
-- visible to an operator. `path` is recorded rather than derived because the
-- database stays the source of truth for where a repository lives, even
-- though the layout under GIT_REPO_DIR is predictable (§2 principle 2).
INSERT INTO repos (name, path, kind, remote_url, default_branch)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, path, kind, remote_url, created_at, default_branch,
          last_synced_at, last_synced_commit_sha;

-- name: GetRepo :one
SELECT id, name, path, kind, remote_url, created_at, default_branch,
       last_synced_at, last_synced_commit_sha
FROM repos
WHERE id = $1;

-- name: GetRepoByName :one
-- The CLI addresses repositories by name; ids never appear in a command line.
SELECT id, name, path, kind, remote_url, created_at, default_branch,
       last_synced_at, last_synced_commit_sha
FROM repos
WHERE name = $1;

-- name: ListRepos :many
-- Keyset pagination on (name, id) ASC. A repository list is a catalogue rather
-- than a timeline, so it is ordered by name - the opposite of ListRuns, and
-- the reason the cursor here is a name rather than a timestamp.
SELECT id, name, path, kind, remote_url, created_at, default_branch,
       last_synced_at, last_synced_commit_sha
FROM repos
WHERE sqlc.narg(cursor_name)::text IS NULL
   OR (name, id) > (sqlc.narg(cursor_name)::text, sqlc.narg(cursor_id)::bigint)
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(row_limit);

-- name: MarkRepoSynced :exec
-- Records what the last successful scan saw (task 3.4). Deliberately *not* an
-- input to the next scan: a scan always re-reads HEAD and rebuilds the whole
-- projection, because trusting a stored "last seen" would make a sync that
-- half-failed invisible to the one after it.
UPDATE repos
SET last_synced_at = now(),
    last_synced_commit_sha = $2
WHERE id = $1;
