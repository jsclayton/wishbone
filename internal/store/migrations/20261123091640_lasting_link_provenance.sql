-- +goose NO TRANSACTION
-- +goose Up
-- What a lasting login link's sessions make records the link (docs/spec.md,
-- Access, Lasting login links). A passkey that one of its sessions adds, and a
-- login link that one of them makes, carry the link's ID, and so does a session
-- that such a passkey or login link starts. A revoke then finds all of them, at
-- any distance, and removes them in the same commit.
--
-- The ID is cleared, not cascaded, when the lasting link is deleted, which
-- happens only when its user is deleted. A login link that the user's session
-- made for a child carries the ID to the child's session and passkeys, and the
-- delete of the parent must not delete those. So sessions.lasting_link_id, which
-- cascaded, is cleared too. SQLite can't change a foreign key in place, so this
-- rebuilds sessions, by the steps of 20261121013109_ids_never_come_back.sql
-- (docs/development.md, Migrations), and carries its counter over.
--
-- Rows from before this migration have NULL: nothing says which lasting link
-- made them. lasting_links.predates_provenance marks the links that existed
-- then, so the CLI still says what else to check when it revokes one.
--
-- It has no Down: to go back, restore a snapshot.
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

-- The two checks. An INSERT into this table fails, and stops the migration,
-- when foreign keys are still on, or when the rebuild broke a reference.
CREATE TEMP TABLE provenance_checks (
    foreign_keys     INTEGER CONSTRAINT foreign_keys_must_be_off_for_the_rebuild CHECK (foreign_keys = 0),
    broken_reference TEXT CONSTRAINT the_rebuild_broke_a_foreign_key CHECK (broken_reference IS NULL)
);
INSERT INTO provenance_checks (foreign_keys) SELECT foreign_keys FROM pragma_foreign_keys;

-- The references that are broken before the rebuild. The app keeps foreign
-- keys on, so there are none. If a database has one, the rebuild leaves it as
-- it is.
CREATE TEMP TABLE provenance_broken_before AS SELECT * FROM pragma_foreign_key_check;

CREATE TABLE new_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,
    user_agent      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMP NOT NULL,
    last_used_at    TIMESTAMP NOT NULL,
    expires_at      TIMESTAMP NOT NULL,
    revoked_at      TIMESTAMP,
    cookie_sent_at  TIMESTAMP NOT NULL,
    -- The lasting link that started the session, directly or through a login
    -- link or a passkey that one of the link's sessions made. NULL for any
    -- other session. So a revoke finds the sessions to end. NULL again when
    -- the link is deleted with its user: the session stays.
    lasting_link_id INTEGER REFERENCES lasting_links (id) ON DELETE SET NULL
);
INSERT INTO new_sessions (
    id, user_id, token_hash, user_agent, created_at, last_used_at, expires_at, revoked_at,
    cookie_sent_at, lasting_link_id
)
SELECT
    id, user_id, token_hash, user_agent, created_at, last_used_at, expires_at, revoked_at,
    cookie_sent_at, lasting_link_id
FROM sessions;
UPDATE sqlite_sequence
SET seq = (SELECT max(seq) FROM sqlite_sequence WHERE name IN ('sessions', 'new_sessions'))
WHERE name = 'new_sessions';
DROP TABLE sessions;
ALTER TABLE new_sessions RENAME TO sessions;
CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_lasting_link_id ON sessions (lasting_link_id);

-- The trigger on sessions went with the old table (the idempotency_keys
-- migration made it).
-- +goose StatementBegin
CREATE TRIGGER idempotency_keys_session_revoked
AFTER UPDATE OF revoked_at ON sessions
WHEN NEW.revoked_at IS NOT NULL
BEGIN
    DELETE FROM idempotency_keys WHERE session_id = NEW.id;
END;
-- +goose StatementEnd

-- The lasting link whose session added the passkey, or NULL. A revoke of the
-- link deletes the passkey.
ALTER TABLE passkeys ADD COLUMN lasting_link_id INTEGER REFERENCES lasting_links (id) ON DELETE SET NULL;
CREATE INDEX passkeys_lasting_link_id ON passkeys (lasting_link_id);

-- The lasting link whose session made the login link, or NULL. A revoke of the
-- link ends the login link if it isn't used yet.
ALTER TABLE login_links ADD COLUMN lasting_link_id INTEGER REFERENCES lasting_links (id) ON DELETE SET NULL;
CREATE INDEX login_links_lasting_link_id ON login_links (lasting_link_id);

-- 1 for a lasting link made before this migration. Its sessions may have made
-- passkeys and login links that don't record it, so a revoke can't find them.
ALTER TABLE lasting_links ADD COLUMN predates_provenance INTEGER NOT NULL DEFAULT 0
    CHECK (predates_provenance IN (0, 1));
UPDATE lasting_links SET predates_provenance = 1;

-- Every reference that is broken now was broken before.
INSERT INTO provenance_checks (broken_reference)
SELECT "table" FROM (SELECT * FROM pragma_foreign_key_check EXCEPT SELECT * FROM provenance_broken_before);

DROP TABLE temp.provenance_broken_before;
DROP TABLE temp.provenance_checks;

COMMIT;
PRAGMA foreign_keys = ON;
