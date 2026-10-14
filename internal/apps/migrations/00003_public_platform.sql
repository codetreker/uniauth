-- +goose Up
-- The platform upgrade intentionally resets the old single-administrator registry.
DROP TABLE app_secrets;
DROP TABLE apps;

CREATE TABLE accounts (
    id TEXT PRIMARY KEY NOT NULL,
    email TEXT,
    name TEXT,
    avatar_url TEXT,
    is_admin INTEGER NOT NULL CHECK (is_admin IN (0, 1)),
    created_at INTEGER NOT NULL
);
CREATE INDEX accounts_created_at ON accounts (created_at, id);
CREATE TABLE account_identities (
    provider TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    email TEXT,
    email_verified INTEGER CHECK (email_verified IN (0, 1)),
    name TEXT,
    linked_at INTEGER NOT NULL,
    PRIMARY KEY (provider, provider_id)
);
CREATE INDEX account_identities_account_id ON account_identities (account_id);
CREATE TABLE platform_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    initial_admin_email TEXT NOT NULL,
    claimed_account_id TEXT,
    console_app_id TEXT
);
CREATE TABLE apps (
    id TEXT PRIMARY KEY NOT NULL,
    owner_id TEXT NOT NULL,
    system INTEGER NOT NULL CHECK (system IN (0, 1)),
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    redirect_uris TEXT NOT NULL,
    providers TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX apps_owner_id ON apps (owner_id, created_at, id);
CREATE TABLE app_secrets (
    id TEXT PRIMARY KEY NOT NULL,
    app_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);
CREATE INDEX app_secrets_app_id ON app_secrets (app_id);

-- +goose Down
DROP TABLE app_secrets;
DROP TABLE apps;
DROP TABLE platform_state;
DROP TABLE account_identities;
DROP TABLE accounts;
CREATE TABLE apps (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    redirect_uris TEXT NOT NULL,
    providers TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE app_secrets (
    id TEXT PRIMARY KEY NOT NULL,
    app_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);
CREATE INDEX app_secrets_app_id ON app_secrets (app_id);
