-- Buzzit schema v1. All timestamps are Unix milliseconds.

CREATE TABLE users (
    id                 TEXT PRIMARY KEY,
    username           TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name       TEXT NOT NULL,
    bio                TEXT NOT NULL DEFAULT '',
    password_hash      TEXT,                 -- NULL for bot users
    avatar_media_id    TEXT,
    is_bot             INTEGER NOT NULL DEFAULT 0,
    show_last_seen     INTEGER NOT NULL DEFAULT 1,
    send_read_receipts INTEGER NOT NULL DEFAULT 1,
    last_seen_at       INTEGER NOT NULL DEFAULT 0,
    event_seq          INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL
);

CREATE TABLE devices (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    platform     TEXT NOT NULL DEFAULT '',
    token_hash   TEXT NOT NULL UNIQUE,
    push_token   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL
);
CREATE INDEX devices_user ON devices(user_id);

CREATE TABLE friend_requests (
    from_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (from_id, to_id)
);
CREATE INDEX friend_requests_to ON friend_requests(to_id);

-- One row per friendship, user_a < user_b.
CREATE TABLE friendships (
    user_a     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_b     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b)
);
CREATE INDEX friendships_b ON friendships(user_b);

CREATE TABLE blocks (
    blocker_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (blocker_id, blocked_id)
);

CREATE TABLE buzz_passes (
    grantor_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    grantee_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    max_level       INTEGER NOT NULL,
    quiet_start_min INTEGER,                 -- minutes after local midnight
    quiet_end_min   INTEGER,
    tz              TEXT NOT NULL DEFAULT 'UTC',
    expires_at      INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    PRIMARY KEY (grantor_id, grantee_id)
);

CREATE TABLE buzz_events (
    id           TEXT PRIMARY KEY,
    sender_id    TEXT NOT NULL,
    recipient_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level        INTEGER NOT NULL,
    note         TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,
    app_id       TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX buzz_events_recipient ON buzz_events(recipient_id, created_at);

CREATE TABLE media (
    id         TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mime       TEXT NOT NULL,
    size       INTEGER NOT NULL,
    filename   TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE conversations (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL CHECK (type IN ('direct','group','bot')),
    title           TEXT NOT NULL DEFAULT '',
    avatar_media_id TEXT,
    direct_key      TEXT UNIQUE,             -- sorted member pair for direct/bot chats
    created_by      TEXT NOT NULL,
    last_seq        INTEGER NOT NULL DEFAULT 0,
    last_message_at INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);

CREATE TABLE members (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','admin','member')),
    last_read_seq   INTEGER NOT NULL DEFAULT 0,
    muted_until     INTEGER NOT NULL DEFAULT 0,
    joined_at       INTEGER NOT NULL,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX members_user ON members(user_id);

CREATE TABLE messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    sender_id       TEXT NOT NULL,
    client_id       TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL CHECK (kind IN ('text','image','file','voice','video','system')),
    body            BLOB,                    -- encrypted at rest
    media_id        TEXT,
    reply_to        TEXT,
    deleted         INTEGER NOT NULL DEFAULT 0,
    edited_at       INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    UNIQUE (conversation_id, seq)
);
CREATE UNIQUE INDEX messages_client ON messages(conversation_id, sender_id, client_id) WHERE client_id <> '';
CREATE INDEX messages_media ON messages(media_id) WHERE media_id IS NOT NULL;

CREATE TABLE reactions (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (message_id, user_id)
);

-- Per-user event log, used for realtime replay after reconnect (/v1/sync).
CREATE TABLE events (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    type       TEXT NOT NULL,
    payload    BLOB NOT NULL,                -- encrypted at rest
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, seq)
);
CREATE INDEX events_created ON events(created_at);

-- Buzzit Platform
CREATE TABLE apps (
    id             TEXT PRIMARY KEY,
    owner_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_user_id    TEXT NOT NULL REFERENCES users(id),
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    webhook_url    TEXT NOT NULL DEFAULT '',
    webhook_secret TEXT NOT NULL,
    token_hash     TEXT NOT NULL UNIQUE,
    tier           TEXT NOT NULL DEFAULT 'free',
    created_at     INTEGER NOT NULL
);

CREATE TABLE app_grants (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scopes     TEXT NOT NULL,               -- space-separated
    created_at INTEGER NOT NULL,
    PRIMARY KEY (app_id, user_id)
);

CREATE TABLE app_usage (
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    day    TEXT NOT NULL,                   -- YYYY-MM-DD (UTC)
    kind   TEXT NOT NULL,
    count  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, day, kind)
);

CREATE TABLE webhook_outbox (
    id              TEXT PRIMARY KEY,
    app_id          TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    payload         BLOB NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending | delivered | failed
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL
);
CREATE INDEX webhook_outbox_due ON webhook_outbox(status, next_attempt_at);
