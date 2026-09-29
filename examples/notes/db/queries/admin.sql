-- Queries for the app's admin page (handlers/admin.go): across every
-- user, because only admins reach them. Each is exempt from the owner
-- rule (lidza check L018) by the comment before it.

-- lidza:ignore L018
-- name: AdminRecentNotes :many
SELECT n.id, n.title, u.email AS owner_email, n.created_at
FROM note n JOIN app_user u ON u.id = n.owner_id
ORDER BY n.created_at DESC LIMIT $1;

-- lidza:ignore L018
-- name: AdminCountNotes :one
SELECT count(*) FROM note;

-- lidza:ignore L018
-- name: AdminDeleteNote :execrows
DELETE FROM note WHERE id = $1;
