-- Queries for sqlc. One comment names each query and its shape:
--   :one   a single row      :many  a list      :exec  no rows
-- Tables and columns come from db/schema.sql (generated from schema.lidza).
--
-- name: Now :one
SELECT now()::timestamptz AS now;
