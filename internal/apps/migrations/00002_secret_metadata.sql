-- +goose Up
CREATE TABLE app_secrets_with_metadata (
    id TEXT PRIMARY KEY NOT NULL,
    app_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);

INSERT INTO app_secrets_with_metadata (id, app_id, name, description, digest, created_at, revoked_at)
SELECT id, app_id, 'Secret ' || id, '', digest, created_at, revoked_at FROM app_secrets;

DROP TABLE app_secrets;
ALTER TABLE app_secrets_with_metadata RENAME TO app_secrets;
CREATE INDEX app_secrets_app_id ON app_secrets (app_id);

-- +goose Down
CREATE TABLE app_secrets_without_metadata (
    id TEXT PRIMARY KEY NOT NULL,
    app_id TEXT NOT NULL,
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);

INSERT INTO app_secrets_without_metadata (id, app_id, digest, created_at, revoked_at)
SELECT id, app_id, digest, created_at, revoked_at FROM app_secrets;

DROP TABLE app_secrets;
ALTER TABLE app_secrets_without_metadata RENAME TO app_secrets;
CREATE INDEX app_secrets_app_id ON app_secrets (app_id);
