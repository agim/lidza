-- Queries for the app's admin page (handlers/admin.go): across every
-- user, because only admins reach them.

-- name: AdminRecentNotes :many
SELECT n.id, n.title, u.email AS owner_email, n.created_at
FROM note n JOIN app_user u ON u.id = n.owner_id
ORDER BY n.created_at DESC LIMIT $1;

-- name: AdminCountNotes :one
SELECT count(*) FROM note;

-- name: AdminDeleteNote :execrows
DELETE FROM note WHERE id = $1;
