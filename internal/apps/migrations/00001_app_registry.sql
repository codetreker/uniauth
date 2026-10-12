-- +goose Up
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
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);

CREATE INDEX app_secrets_app_id ON app_secrets (app_id);

-- +goose Down
DROP TABLE app_secrets;
DROP TABLE apps;
