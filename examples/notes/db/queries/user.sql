-- Queries for User. sqlc turns them into Go (db/queries/gen) on lidza gen.

-- name: CreateUser :one
INSERT INTO app_user (email, password_hash) VALUES ($1, $2) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM app_user WHERE email = $1;

-- name: MarkVerified :execrows
UPDATE app_user SET verified_at = now() WHERE email = $1 AND verified_at IS NULL;

-- name: SetPassword :execrows
UPDATE app_user SET password_hash = $2 WHERE email = $1;
