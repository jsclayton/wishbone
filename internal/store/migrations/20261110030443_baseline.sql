-- +goose Up
-- The baseline. A new install creates the whole schema here (docs/spec.md,
-- Migration). A database that already lists this version runs nothing.
--
-- Never edit this file. A schema change is a new migration (`just migration
-- <name>`). It has no Down: to go back, restore a snapshot.

-- Instance-wide key/value settings (instance ID, generated secrets).
CREATE TABLE instance_meta (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Extraction results by normalized URL. Rows older than 24 hours are ignored
-- and deleted when the next result is stored.
CREATE TABLE extraction_cache (
    url        TEXT PRIMARY KEY,
    result     TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX extraction_cache_created_at ON extraction_cache (created_at);

CREATE TABLE users (
    id                   INTEGER PRIMARY KEY,
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
CREATE UNIQUE INDEX users_one_admin ON users (is_admin) WHERE is_admin = 1;

CREATE TABLE setup_codes (
    id         INTEGER PRIMARY KEY,
    code_hash  TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX users_webauthn_handle ON users (webauthn_handle) WHERE webauthn_handle IS NOT NULL;
CREATE UNIQUE INDEX users_avatar_image_id ON users (avatar_image_id) WHERE avatar_image_id IS NOT NULL;

CREATE TABLE groups (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE group_users (
    group_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX group_users_user_id ON group_users (user_id);

CREATE TABLE group_groups (
    group_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    child_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, child_id),
    CHECK (group_id <> child_id)
);
CREATE INDEX group_groups_child_id ON group_groups (child_id);

CREATE TABLE user_parents (
    child_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    parent_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (child_id, parent_id),
    CHECK (child_id <> parent_id)
);
CREATE INDEX user_parents_parent_id ON user_parents (parent_id);

CREATE TABLE blocked_stores (
    id         INTEGER PRIMARY KEY,
    store      TEXT NOT NULL UNIQUE CHECK (store <> ''),
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE lists (
    id          INTEGER PRIMARY KEY,
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
CREATE UNIQUE INDEX lists_primary_user_id ON lists (primary_user_id) WHERE primary_user_id IS NOT NULL;

CREATE TABLE list_recipients (
    list_id    INTEGER NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- When the user became a recipient.
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (list_id, user_id)
);
CREATE INDEX list_recipients_user_id ON list_recipients (user_id);

CREATE TABLE store_links (
    id          INTEGER PRIMARY KEY,
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
CREATE INDEX store_links_item_id ON store_links (item_id, id);

CREATE TABLE item_images (
    item_id    INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    image_id   INTEGER NOT NULL REFERENCES images (id),
    -- 0 to n-1, with no gaps: the order of the item's images.
    position   INTEGER NOT NULL CHECK (position >= 0),
    -- 1 for the primary image. An item with images has exactly one.
    is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    PRIMARY KEY (item_id, image_id)
);
CREATE UNIQUE INDEX item_images_image_id ON item_images (image_id);
CREATE UNIQUE INDEX item_images_primary ON item_images (item_id) WHERE is_primary = 1;
CREATE UNIQUE INDEX item_images_position ON item_images (item_id, position);

CREATE TABLE list_invitations (
    id         INTEGER PRIMARY KEY,
    list_id    INTEGER NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
    -- The invited user. The invitation goes with them.
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The recipient who sent it. NULL once that user is deleted.
    invited_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (list_id, user_id)
);
CREATE INDEX list_invitations_user_id ON list_invitations (user_id);

CREATE TABLE occasions (
    id           INTEGER PRIMARY KEY,
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
CREATE INDEX occasions_user_id ON occasions (user_id);
CREATE UNIQUE INDEX occasions_one_birthday ON occasions (user_id) WHERE kind = 'birthday';

CREATE TABLE occasion_groups (
    occasion_id INTEGER NOT NULL REFERENCES occasions (id) ON DELETE CASCADE,
    group_id    INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    PRIMARY KEY (occasion_id, group_id)
);
CREATE INDEX occasion_groups_group_id ON occasion_groups (group_id);

CREATE TABLE gifts (
    id            INTEGER PRIMARY KEY,
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
CREATE INDEX gifts_item_id ON gifts (item_id);
CREATE INDEX gifts_giver_id ON gifts (giver_id);
CREATE UNIQUE INDEX gifts_one_open ON gifts (item_id, giver_id, occasion_id) WHERE given_at IS NULL;

CREATE TABLE gift_flags (
    id         INTEGER PRIMARY KEY,
    gift_id    INTEGER NOT NULL REFERENCES gifts (id) ON DELETE CASCADE,
    code       TEXT NOT NULL,
    data       TEXT,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (gift_id, code)
);

CREATE TABLE gift_changes (
    id        INTEGER PRIMARY KEY,
    gift_id   INTEGER NOT NULL REFERENCES gifts (id) ON DELETE CASCADE,
    field     TEXT NOT NULL,
    label     TEXT NOT NULL DEFAULT '',
    old_value TEXT NOT NULL,
    new_value TEXT NOT NULL,
    UNIQUE (gift_id, field, label)
);

CREATE TABLE kept_list_recipients (
    list_id INTEGER NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (list_id, user_id)
);
CREATE INDEX kept_list_recipients_user_id ON kept_list_recipients (user_id);

CREATE UNIQUE INDEX gifts_image_id ON gifts (image_id) WHERE image_id IS NOT NULL;

CREATE TABLE gift_recipients (
    gift_id  INTEGER NOT NULL REFERENCES gifts (id) ON DELETE CASCADE,
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    PRIMARY KEY (gift_id, user_id)
);
CREATE INDEX gift_recipients_user_id ON gift_recipients (user_id);

CREATE TABLE items (
    id              INTEGER PRIMARY KEY,
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
CREATE INDEX items_list_id_position ON items (list_id, position);

CREATE TABLE sessions (
    id             INTEGER PRIMARY KEY,
    user_id        INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash     TEXT NOT NULL UNIQUE,
    user_agent     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL,
    last_used_at   TIMESTAMP NOT NULL,
    expires_at     TIMESTAMP NOT NULL,
    revoked_at     TIMESTAMP,
    cookie_sent_at TIMESTAMP NOT NULL
);
CREATE INDEX sessions_user_id ON sessions (user_id);

CREATE TABLE login_links (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMP NOT NULL,
    expires_at  TIMESTAMP NOT NULL,
    redeemed_at TIMESTAMP
);
CREATE INDEX login_links_user_id ON login_links (user_id);

CREATE TABLE passkeys (
    id               INTEGER PRIMARY KEY,
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
CREATE INDEX passkeys_user_id ON passkeys (user_id);

CREATE TABLE images (
    id          INTEGER PRIMARY KEY,
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
CREATE INDEX images_uploaded_by ON images (uploaded_by);
