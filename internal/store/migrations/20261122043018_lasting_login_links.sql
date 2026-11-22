-- +goose Up
-- Lasting login links (docs/spec.md, Access, Lasting login links). The operator
-- makes one with the CLI. It works any number of times and doesn't expire: it
-- works until the operator revokes it. Package loginlink owns the table, as it
-- owns login_links.
CREATE TABLE lasting_links (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The hash of the token's canonical form: its 16 characters in lower case,
    -- with no hyphens.
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMP NOT NULL,
    -- When it was last redeemed, or NULL if it never was.
    last_used_at TIMESTAMP,
    -- When the operator revoked it. A revoked link never works again, and the
    -- revoke ends every session it started.
    revoked_at   TIMESTAMP
);
CREATE INDEX lasting_links_user_id ON lasting_links (user_id);

-- The lasting link that started a session, or NULL for a session that one didn't
-- start. So a revoke finds the sessions to end. A session goes with its link.
ALTER TABLE sessions ADD COLUMN lasting_link_id INTEGER REFERENCES lasting_links (id) ON DELETE CASCADE;
CREATE INDEX sessions_lasting_link_id ON sessions (lasting_link_id);
