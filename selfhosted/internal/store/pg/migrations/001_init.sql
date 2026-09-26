CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    api_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    phone_number TEXT NOT NULL,
    base_url TEXT NOT NULL,
    username TEXT NOT NULL,
    password TEXT NOT NULL,
    mode TEXT NOT NULL,
    ingest_token TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, phone_number)
);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id TEXT REFERENCES devices (id) ON DELETE SET NULL,
    request_id TEXT NOT NULL DEFAULT '',
    owner TEXT NOT NULL,
    contact TEXT NOT NULL,
    content TEXT NOT NULL,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    sim TEXT NOT NULL,
    gateway_id TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX messages_user_gateway_uidx
    ON messages (user_id, gateway_id)
    WHERE gateway_id <> '';

CREATE TABLE webhooks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    signing_key TEXT NOT NULL DEFAULT '',
    events TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
