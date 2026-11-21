-- +goose NO TRANSACTION
-- +goose Up
-- An ID is never used again (docs/spec.md, API principles, Values). Until
-- now each table with an ID had `id INTEGER PRIMARY KEY`, and SQLite gave a new
-- row the highest ID in the table plus one. So the ID of a deleted row came
-- back when it was the highest. With AUTOINCREMENT, SQLite keeps the highest ID
-- that a table ever had in sqlite_sequence, and gives a new row a higher one.
--
-- SQLite can't add AUTOINCREMENT to a table, so this rebuilds each of the 16
-- tables that have an ID, by the steps of https://sqlite.org/lang_altertable.html
-- (Making Other Kinds Of Table Schema Changes). Each rebuild makes the new
-- table, copies every row with its ID, drops the old table, gives the new one
-- its name, and makes the indexes again. The copy also sets the table's row in
-- sqlite_sequence to its highest ID. Only AUTOINCREMENT is new in a table's
-- definition. The triggers on a rebuilt table are made again at the end.
--
-- Foreign keys are off for the rebuild. With them on, DROP TABLE deletes the
-- table's rows first, and that delete cascades to the rows that point at them.
-- SQLite ignores the pragma inside a transaction, so the migration runs with NO
-- TRANSACTION and opens its own. Nothing is kept unless all of it is.
--
-- It has no Down: to go back, restore a snapshot.
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

-- The two checks. An INSERT into this table fails, and stops the migration,
-- when foreign keys are still on, or when the rebuild broke a reference.
CREATE TEMP TABLE ids_rebuild_checks (
    foreign_keys     INTEGER CONSTRAINT foreign_keys_must_be_off_for_the_rebuild CHECK (foreign_keys = 0),
    broken_reference TEXT CONSTRAINT the_rebuild_broke_a_foreign_key CHECK (broken_reference IS NULL)
);
INSERT INTO ids_rebuild_checks (foreign_keys) SELECT foreign_keys FROM pragma_foreign_keys;

-- The references that are broken before the rebuild. The app keeps foreign
-- keys on, so there are none. If a database has one, the rebuild leaves it as
-- it is.
CREATE TEMP TABLE ids_rebuild_broken_before AS SELECT * FROM pragma_foreign_key_check;

CREATE TABLE new_users (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT NOT NULL,
    last_name            TEXT,
    -- A partial date: the year is optional, the month and day are not.
    birthday_month       INTEGER NOT NULL CHECK (birthday_month BETWEEN 1 AND 12),
    birthday_day         INTEGER NOT NULL CHECK (birthday_day BETWEEN 1 AND 31),
    birthday_year        INTEGER,
    is_admin             INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    profile_confirmed_at TIMESTAMP,
    created_at           TIMESTAMP NOT NULL,
    good_to_know         TEXT,
    timezone             TEXT,
    webauthn_handle      BLOB,
    avatar_image_id      INTEGER REFERENCES images (id),
    deactivated_at       TIMESTAMP
);
INSERT INTO new_users (
    id, name, last_name, birthday_month, birthday_day, birthday_year, is_admin, profile_confirmed_at,
    created_at, good_to_know, timezone, webauthn_handle, avatar_image_id, deactivated_at
)
SELECT
    id, name, last_name, birthday_month, birthday_day, birthday_year, is_admin, profile_confirmed_at,
    created_at, good_to_know, timezone, webauthn_handle, avatar_image_id, deactivated_at
FROM users;
DROP TABLE users;
ALTER TABLE new_users RENAME TO users;
CREATE UNIQUE INDEX users_one_admin ON users (is_admin) WHERE is_admin = 1;
CREATE UNIQUE INDEX users_webauthn_handle ON users (webauthn_handle) WHERE webauthn_handle IS NOT NULL;
CREATE UNIQUE INDEX users_avatar_image_id ON users (avatar_image_id) WHERE avatar_image_id IS NOT NULL;

CREATE TABLE new_setup_codes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code_hash  TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL
);
INSERT INTO new_setup_codes (
    id, code_hash, created_at
)
SELECT
    id, code_hash, created_at
FROM setup_codes;
DROP TABLE setup_codes;
ALTER TABLE new_setup_codes RENAME TO setup_codes;

CREATE TABLE new_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
INSERT INTO new_groups (
    id, name, created_at
)
SELECT
    id, name, created_at
FROM groups;
DROP TABLE groups;
ALTER TABLE new_groups RENAME TO groups;

CREATE TABLE new_blocked_stores (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    store      TEXT NOT NULL UNIQUE CHECK (store <> ''),
    created_at TIMESTAMP NOT NULL
);
INSERT INTO new_blocked_stores (
    id, store, created_at
)
SELECT
    id, store, created_at
FROM blocked_stores;
DROP TABLE blocked_stores;
ALTER TABLE new_blocked_stores RENAME TO blocked_stores;

CREATE TABLE new_lists (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    -- Markdown, or NULL for none.
    description TEXT,
    -- The user whose primary list this is, or NULL for an extra list. A user
    -- has at most one, and it goes with the user.
    primary_user_id INTEGER REFERENCES users (id) ON DELETE CASCADE,
    -- 1 while a primary list's name is its user's name. A rename by a user who
    -- can edit the list ends it.
    name_follows_user INTEGER NOT NULL DEFAULT 0 CHECK (name_follows_user IN (0, 1)),
    created_at  TIMESTAMP NOT NULL,
    deleted_at  TIMESTAMP
);
INSERT INTO new_lists (
    id, name, description, primary_user_id, name_follows_user, created_at, deleted_at
)
SELECT
    id, name, description, primary_user_id, name_follows_user, created_at, deleted_at
FROM lists;
DROP TABLE lists;
ALTER TABLE new_lists RENAME TO lists;
CREATE UNIQUE INDEX lists_primary_user_id ON lists (primary_user_id) WHERE primary_user_id IS NOT NULL;

CREATE TABLE new_store_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id     INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    -- The address after extract.NormalizeURL.
    url         TEXT NOT NULL,
    -- The host without a leading "www.", as extract.StoreFromURL gives it.
    store       TEXT NOT NULL,
    -- The price in the currency's minor unit. A price has a currency.
    price_minor INTEGER CHECK (price_minor IS NULL OR price_minor >= 0),
    currency    TEXT,
    -- When the price was captured. NULL when there is no price.
    captured_at TIMESTAMP,
    -- The store's own address of its picture. It is outside Wishbone.
    image_url   TEXT,
    created_at  TIMESTAMP NOT NULL,
    CHECK ((price_minor IS NULL) = (currency IS NULL))
);
INSERT INTO new_store_links (
    id, item_id, url, store, price_minor, currency, captured_at, image_url, created_at
)
SELECT
    id, item_id, url, store, price_minor, currency, captured_at, image_url, created_at
FROM store_links;
DROP TABLE store_links;
ALTER TABLE new_store_links RENAME TO store_links;
CREATE INDEX store_links_item_id ON store_links (item_id, id);

CREATE TABLE new_list_invitations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    list_id    INTEGER NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
    -- The invited user. The invitation goes with them.
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The recipient who sent it. NULL once that user is deleted.
    invited_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (list_id, user_id)
);
INSERT INTO new_list_invitations (
    id, list_id, user_id, invited_by, created_at
)
SELECT
    id, list_id, user_id, invited_by, created_at
FROM list_invitations;
DROP TABLE list_invitations;
ALTER TABLE new_list_invitations RENAME TO list_invitations;
CREATE INDEX list_invitations_user_id ON list_invitations (user_id);

CREATE TABLE new_occasions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    kind         TEXT NOT NULL CHECK (kind IN ('birthday', 'shared', 'custom')),
    name         TEXT NOT NULL,
    -- The user a birthday or custom occasion belongs to. It goes with the user.
    user_id      INTEGER REFERENCES users (id) ON DELETE CASCADE,
    -- The date rule. NULL for a birthday. Columns a rule kind doesn't use are NULL.
    rule_kind    TEXT CHECK (rule_kind IN ('annual', 'once', 'nth_weekday', 'last_weekday')),
    rule_year    INTEGER,
    rule_month   INTEGER CHECK (rule_month BETWEEN 1 AND 12),
    rule_day     INTEGER,
    rule_weekday INTEGER CHECK (rule_weekday BETWEEN 0 AND 6),
    rule_nth     INTEGER CHECK (rule_nth BETWEEN 1 AND 4),
    created_at   TIMESTAMP NOT NULL,
    is_everyone  INTEGER NOT NULL DEFAULT 0 CHECK (is_everyone IN (0, 1)),
    CHECK ((kind = 'shared') = (user_id IS NULL)),
    CHECK ((kind = 'birthday') = (rule_kind IS NULL)),
    CHECK ((rule_kind IS NULL) = (rule_month IS NULL))
);
INSERT INTO new_occasions (
    id, kind, name, user_id, rule_kind, rule_year, rule_month, rule_day, rule_weekday, rule_nth,
    created_at, is_everyone
)
SELECT
    id, kind, name, user_id, rule_kind, rule_year, rule_month, rule_day, rule_weekday, rule_nth,
    created_at, is_everyone
FROM occasions;
DROP TABLE occasions;
ALTER TABLE new_occasions RENAME TO occasions;
CREATE INDEX occasions_user_id ON occasions (user_id);
CREATE UNIQUE INDEX occasions_one_birthday ON occasions (user_id) WHERE kind = 'birthday';

CREATE TABLE new_gifts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id       INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    -- The giver. NULL once that user is deleted (their given gifts stay for the
    -- recipients).
    giver_id      INTEGER REFERENCES users (id) ON DELETE SET NULL,
    quantity      INTEGER NOT NULL CHECK (quantity >= 1),
    -- The occasion, and the date it fell on when the giver picked it, as
    -- YYYY-MM-DD. The date is stored because a yearly occasion's next date moves
    -- to next year once it has passed, while the gift is still waiting for its
    -- reveal. NULL occasion: the occasion was deleted.
    occasion_id   INTEGER REFERENCES occasions (id) ON DELETE SET NULL,
    occasion_date TEXT NOT NULL,
    from_label    TEXT,
    -- The giver's private note and bought mark. Recipients never see them.
    note          TEXT,
    is_bought     INTEGER NOT NULL DEFAULT 0 CHECK (is_bought IN (0, 1)),
    created_at    TIMESTAMP NOT NULL,
    -- When the gift was given, or NULL while it is open.
    given_at      TIMESTAMP,
    unshared      INTEGER NOT NULL DEFAULT 0 CHECK (unshared IN (0, 1)),
    title         TEXT,
    price_minor   INTEGER,
    top_price_minor INTEGER,
    currency      TEXT,
    image_id      INTEGER REFERENCES images (id),
    occasion_name TEXT,
    giver_saw_list INTEGER CHECK (giver_saw_list IN (0, 1)),
    giver_saw_occasion INTEGER CHECK (giver_saw_occasion IN (0, 1)),
    giver_name    TEXT
);
INSERT INTO new_gifts (
    id, item_id, giver_id, quantity, occasion_id, occasion_date, from_label, note, is_bought,
    created_at, given_at, unshared, title, price_minor, top_price_minor, currency, image_id,
    occasion_name, giver_saw_list, giver_saw_occasion, giver_name
)
SELECT
    id, item_id, giver_id, quantity, occasion_id, occasion_date, from_label, note, is_bought,
    created_at, given_at, unshared, title, price_minor, top_price_minor, currency, image_id,
    occasion_name, giver_saw_list, giver_saw_occasion, giver_name
FROM gifts;
DROP TABLE gifts;
ALTER TABLE new_gifts RENAME TO gifts;
CREATE INDEX gifts_item_id ON gifts (item_id);
CREATE INDEX gifts_giver_id ON gifts (giver_id);
CREATE UNIQUE INDEX gifts_one_open ON gifts (item_id, giver_id, occasion_id) WHERE given_at IS NULL;
CREATE UNIQUE INDEX gifts_image_id ON gifts (image_id) WHERE image_id IS NOT NULL;

CREATE TABLE new_gift_flags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    gift_id    INTEGER NOT NULL REFERENCES gifts (id) ON DELETE CASCADE,
    code       TEXT NOT NULL,
    data       TEXT,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (gift_id, code)
);
INSERT INTO new_gift_flags (
    id, gift_id, code, data, created_at
)
SELECT
    id, gift_id, code, data, created_at
FROM gift_flags;
DROP TABLE gift_flags;
ALTER TABLE new_gift_flags RENAME TO gift_flags;

CREATE TABLE new_gift_changes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    gift_id   INTEGER NOT NULL REFERENCES gifts (id) ON DELETE CASCADE,
    field     TEXT NOT NULL,
    label     TEXT NOT NULL DEFAULT '',
    old_value TEXT NOT NULL,
    new_value TEXT NOT NULL,
    UNIQUE (gift_id, field, label)
);
INSERT INTO new_gift_changes (
    id, gift_id, field, label, old_value, new_value
)
SELECT
    id, gift_id, field, label, old_value, new_value
FROM gift_changes;
DROP TABLE gift_changes;
ALTER TABLE new_gift_changes RENAME TO gift_changes;

CREATE TABLE new_items (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    list_id         INTEGER NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    notes           TEXT,
    -- Money is an integer in the currency's minor unit. The top price makes the
    -- price a range, and needs a price. A price has a currency.
    price_minor     INTEGER CHECK (price_minor IS NULL OR price_minor >= 0),
    top_price_minor INTEGER,
    currency        TEXT,
    -- 0 only for an item whose whole quantity was given: it is deleted softly.
    quantity        INTEGER NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    is_unlimited    INTEGER NOT NULL DEFAULT 0 CHECK (is_unlimited IN (0, 1)),
    -- A 0-based index on the list, with no gaps. The code shifts the other
    -- items when one is added, moved or deleted, so a list's positions are
    -- always 0 to n-1.
    position        INTEGER NOT NULL CHECK (position >= 0),
    -- Who added the item. NULL once that user is deleted.
    added_by        INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMP NOT NULL,
    price_cleared   INTEGER NOT NULL DEFAULT 0 CHECK (price_cleared IN (0, 1)),
    deleted_at      TIMESTAMP,
    CHECK (top_price_minor IS NULL OR (price_minor IS NOT NULL AND top_price_minor >= price_minor)),
    CHECK ((price_minor IS NULL) = (currency IS NULL))
);
INSERT INTO new_items (
    id, list_id, title, notes, price_minor, top_price_minor, currency, quantity, is_unlimited,
    position, added_by, created_at, price_cleared, deleted_at
)
SELECT
    id, list_id, title, notes, price_minor, top_price_minor, currency, quantity, is_unlimited,
    position, added_by, created_at, price_cleared, deleted_at
FROM items;
DROP TABLE items;
ALTER TABLE new_items RENAME TO items;
CREATE INDEX items_list_id_position ON items (list_id, position);

CREATE TABLE new_sessions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id        INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash     TEXT NOT NULL UNIQUE,
    user_agent     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL,
    last_used_at   TIMESTAMP NOT NULL,
    expires_at     TIMESTAMP NOT NULL,
    revoked_at     TIMESTAMP,
    cookie_sent_at TIMESTAMP NOT NULL
);
INSERT INTO new_sessions (
    id, user_id, token_hash, user_agent, created_at, last_used_at, expires_at, revoked_at,
    cookie_sent_at
)
SELECT
    id, user_id, token_hash, user_agent, created_at, last_used_at, expires_at, revoked_at,
    cookie_sent_at
FROM sessions;
DROP TABLE sessions;
ALTER TABLE new_sessions RENAME TO sessions;
CREATE INDEX sessions_user_id ON sessions (user_id);

CREATE TABLE new_login_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMP NOT NULL,
    expires_at  TIMESTAMP NOT NULL,
    redeemed_at TIMESTAMP
);
INSERT INTO new_login_links (
    id, user_id, token_hash, created_at, expires_at, redeemed_at
)
SELECT
    id, user_id, token_hash, created_at, expires_at, redeemed_at
FROM login_links;
DROP TABLE login_links;
ALTER TABLE new_login_links RENAME TO login_links;
CREATE INDEX login_links_user_id ON login_links (user_id);

CREATE TABLE new_passkeys (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id    BLOB NOT NULL UNIQUE,
    public_key       BLOB NOT NULL,
    attestation_type TEXT NOT NULL DEFAULT '',
    aaguid           BLOB NOT NULL DEFAULT x'',
    sign_count       INTEGER NOT NULL DEFAULT 0,
    -- JSON array of transport hints, such as ["internal","hybrid"].
    transports       TEXT NOT NULL DEFAULT '[]',
    backup_eligible  INTEGER NOT NULL DEFAULT 0,
    backup_state     INTEGER NOT NULL DEFAULT 0,
    name             TEXT NOT NULL,
    created_at       TIMESTAMP NOT NULL,
    last_used_at     TIMESTAMP
);
INSERT INTO new_passkeys (
    id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count, transports,
    backup_eligible, backup_state, name, created_at, last_used_at
)
SELECT
    id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count, transports,
    backup_eligible, backup_state, name, created_at, last_used_at
FROM passkeys;
DROP TABLE passkeys;
ALTER TABLE new_passkeys RENAME TO passkeys;
CREATE INDEX passkeys_user_id ON passkeys (user_id);

CREATE TABLE new_images (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    -- The name of the file in the image store. It is random, and never reused.
    key         TEXT NOT NULL UNIQUE,
    width       INTEGER NOT NULL,
    height      INTEGER NOT NULL,
    -- Until the image is attached, only this user sees it. NULL once that user is
    -- deleted.
    uploaded_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMP NOT NULL,
    -- When the image stopped being attached. NULL for an image that was never
    -- attached, or is attached now. The sweep deletes a detached image at once,
    -- and an image that was never attached 24 hours after created_at.
    detached_at TIMESTAMP
);
INSERT INTO new_images (
    id, key, width, height, uploaded_by, created_at, detached_at
)
SELECT
    id, key, width, height, uploaded_by, created_at, detached_at
FROM images;
DROP TABLE images;
ALTER TABLE new_images RENAME TO images;
CREATE INDEX images_uploaded_by ON images (uploaded_by);

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

-- Every reference that is broken now was broken before.
INSERT INTO ids_rebuild_checks (broken_reference)
SELECT "table" FROM (SELECT * FROM pragma_foreign_key_check EXCEPT SELECT * FROM ids_rebuild_broken_before);

DROP TABLE temp.ids_rebuild_broken_before;
DROP TABLE temp.ids_rebuild_checks;

COMMIT;
PRAGMA foreign_keys = ON;
