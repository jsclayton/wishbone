-- +goose Up
-- The answers that a repeated write gets again (docs/spec.md, API principles,
-- Writes, Retries). A row is the first answer to a write that came with an
-- Idempotency-Key header, kept for 24 hours. Package idempotency owns the table.
--
-- A key belongs to its session. The rows of a deleted session go with it (the
-- foreign key), and the rows of a revoked session go with it too (the trigger),
-- in the same commit, whichever code ends the session.
CREATE TABLE idempotency_keys (
    session_id   INTEGER NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    -- A hash of the request's method, path and body. The body is never kept.
    request_hash TEXT NOT NULL,
    -- When the first request with the key arrived.
    created_at   TIMESTAMP NOT NULL,
    status       INTEGER NOT NULL,
    -- The answer's Content-Type, Location, ETag and Link headers, as a JSON object.
    headers      TEXT NOT NULL,
    body         BLOB NOT NULL,
    PRIMARY KEY (session_id, key)
);
CREATE INDEX idempotency_keys_created_at ON idempotency_keys (created_at);

-- +goose StatementBegin
CREATE TRIGGER idempotency_keys_session_revoked
AFTER UPDATE OF revoked_at ON sessions
WHEN NEW.revoked_at IS NOT NULL
BEGIN
    DELETE FROM idempotency_keys WHERE session_id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER idempotency_keys_session_revoked;
DROP TABLE idempotency_keys;
