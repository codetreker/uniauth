package apps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, config Config) (*Store, error) {
	if config.Path == "" {
		return nil, fmt.Errorf("%w: database path is required", ErrInvalid)
	}
	db, err := sql.Open("sqlite", config.Path)
	if err != nil {
		return nil, fmt.Errorf("open app database: %w", err)
	}
	// One connection serializes configuration writes and keeps authentication
	// snapshots ordered with credential revocation in this single-process service.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fail := func(cause error) (*Store, error) {
		return nil, errors.Join(cause, db.Close())
	}
	for _, pragma := range []string{"PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL", "PRAGMA synchronous = FULL"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fail(fmt.Errorf("configure app database: %w", err))
		}
	}
	fsy, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fail(fmt.Errorf("open embedded app migrations: %w", err))
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, fsy, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fail(fmt.Errorf("initialize app migrations: %w", err))
	}
	if _, err := provider.Up(ctx); err != nil {
		return fail(fmt.Errorf("migrate app database: %w", err))
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Create(ctx context.Context, ownerID string, input CreateAppInput) (CreatedApp, error) {
	if err := validateInput(input.AppInput); err != nil {
		return CreatedApp{}, err
	}
	if input.InitialSecret != nil {
		if err := validateSecretInput(*input.InitialSecret); err != nil {
			return CreatedApp{}, err
		}
	}
	id := randomToken(16)
	now := time.Now().UTC()
	app := App{ID: id, Name: input.Name, Enabled: input.Enabled, RedirectURIs: slices.Clone(input.RedirectURIs), Providers: slices.Clone(input.Providers), CreatedAt: now, UpdatedAt: now}
	redirects, providers := encodeLists(input.AppInput)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedApp{}, fmt.Errorf("begin app creation: %w", err)
	}
	defer tx.Rollback()
	if err := requireAccount(ctx, tx, ownerID); err != nil {
		return CreatedApp{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO apps (id, name, enabled, redirect_uris, providers, created_at, updated_at, owner_id, system) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`, id, input.Name, input.Enabled, redirects, providers, now.UnixNano(), now.UnixNano(), ownerID); err != nil {
		return CreatedApp{}, fmt.Errorf("create app: %w", err)
	}
	created := CreatedApp{App: app}
	if input.InitialSecret != nil {
		issued, digest := newCredential(*input.InitialSecret)
		issued.CreatedAt = now
		if err := insertCredential(ctx, tx, id, issued, digest); err != nil {
			return CreatedApp{}, err
		}
		created.Secret, created.SecretID = issued.Secret, issued.ID
	}
	if err := tx.Commit(); err != nil {
		return CreatedApp{}, fmt.Errorf("commit app creation: %w", err)
	}
	return created, nil
}

const appColumns = "id, name, enabled, redirect_uris, providers, created_at, updated_at"

func (s *Store) Get(ctx context.Context, id string) (App, error) {
	return scanApp(s.db.QueryRowContext(ctx, "SELECT "+appColumns+" FROM apps WHERE id = ?", id))
}

func (s *Store) GetOwned(ctx context.Context, ownerID, appID string) (App, error) {
	return scanApp(s.db.QueryRowContext(ctx, "SELECT "+appColumns+" FROM apps WHERE id = ? AND owner_id = ? AND system = 0", appID, ownerID))
}

func (s *Store) List(ctx context.Context, ownerID string) ([]App, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+appColumns+" FROM apps WHERE owner_id = ? AND system = 0 ORDER BY created_at, id", ownerID)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	defer rows.Close()
	apps := make([]App, 0)
	for rows.Next() {
		app, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read apps: %w", err)
	}
	return apps, nil
}

func (s *Store) Update(ctx context.Context, ownerID, id string, input AppInput) (App, error) {
	if err := validateInput(input); err != nil {
		return App{}, err
	}
	redirects, providers := encodeLists(input)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return App{}, fmt.Errorf("begin app update: %w", err)
	}
	defer tx.Rollback()
	app, err := scanApp(tx.QueryRowContext(ctx, "SELECT "+appColumns+" FROM apps WHERE id = ? AND owner_id = ? AND system = 0", id, ownerID))
	if err != nil {
		return App{}, err
	}
	app.Name, app.Enabled = input.Name, input.Enabled
	app.RedirectURIs, app.Providers = slices.Clone(input.RedirectURIs), slices.Clone(input.Providers)
	app.UpdatedAt = time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "UPDATE apps SET name = ?, enabled = ?, redirect_uris = ?, providers = ?, updated_at = ? WHERE id = ?", input.Name, input.Enabled, redirects, providers, app.UpdatedAt.UnixNano(), id); err != nil {
		return App{}, fmt.Errorf("update app: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return App{}, fmt.Errorf("commit app update: %w", err)
	}
	return app, nil
}

func (s *Store) Delete(ctx context.Context, ownerID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin app deletion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM apps WHERE id = ? AND owner_id = ? AND system = 0", id, ownerID)
	if err != nil {
		return fmt.Errorf("delete app: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM app_secrets WHERE app_id = ?", id); err != nil {
		return fmt.Errorf("delete app credentials: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit app deletion: %w", err)
	}
	return nil
}

const secretColumns = "id, name, description, created_at, revoked_at"

func (s *Store) ListSecrets(ctx context.Context, ownerID, appID string) ([]Secret, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin credential listing: %w", err)
	}
	defer tx.Rollback()
	if err := requireOwnedApp(ctx, tx, ownerID, appID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+secretColumns+" FROM app_secrets WHERE app_id = ? ORDER BY created_at, id", appID)
	if err != nil {
		return nil, fmt.Errorf("list app credentials: %w", err)
	}
	defer rows.Close()
	secrets := make([]Secret, 0)
	for rows.Next() {
		secret, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read app credentials: %w", err)
	}
	return secrets, nil
}

func (s *Store) CreateSecret(ctx context.Context, ownerID, appID string, input SecretInput) (IssuedSecret, error) {
	if err := validateSecretInput(input); err != nil {
		return IssuedSecret{}, err
	}
	issued, digest := newCredential(input)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedSecret{}, fmt.Errorf("begin credential creation: %w", err)
	}
	defer tx.Rollback()
	if err := requireOwnedApp(ctx, tx, ownerID, appID); err != nil {
		return IssuedSecret{}, err
	}
	if err := insertCredential(ctx, tx, appID, issued, digest); err != nil {
		return IssuedSecret{}, err
	}
	if err := tx.Commit(); err != nil {
		return IssuedSecret{}, fmt.Errorf("commit credential creation: %w", err)
	}
	return issued, nil
}

func (s *Store) UpdateSecret(ctx context.Context, ownerID, appID, secretID string, input SecretInput) (Secret, error) {
	if err := validateSecretInput(input); err != nil {
		return Secret{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Secret{}, fmt.Errorf("begin credential metadata update: %w", err)
	}
	defer tx.Rollback()
	if err := requireOwnedApp(ctx, tx, ownerID, appID); err != nil {
		return Secret{}, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE app_secrets SET name = ?, description = ? WHERE app_id = ? AND id = ?", input.Name, input.Description, appID, secretID)
	if err != nil {
		return Secret{}, fmt.Errorf("update app credential metadata: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return Secret{}, err
	}
	secret, err := scanSecret(tx.QueryRowContext(ctx, "SELECT "+secretColumns+" FROM app_secrets WHERE app_id = ? AND id = ?", appID, secretID))
	if err != nil {
		return Secret{}, err
	}
	if err := tx.Commit(); err != nil {
		return Secret{}, fmt.Errorf("commit credential metadata update: %w", err)
	}
	return secret, nil
}

func (s *Store) RevokeSecret(ctx context.Context, ownerID, appID, secretID string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE app_secrets SET revoked_at = COALESCE(revoked_at, ?) WHERE app_id = ? AND id = ? AND EXISTS (SELECT 1 FROM apps WHERE id = ? AND owner_id = ? AND system = 0)", time.Now().UTC().UnixNano(), appID, secretID, appID, ownerID)
	if err != nil {
		return fmt.Errorf("revoke app credential: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) ResetSecret(ctx context.Context, ownerID, appID, secretID string) (IssuedSecret, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedSecret{}, fmt.Errorf("begin credential reset: %w", err)
	}
	defer tx.Rollback()
	if err := requireOwnedApp(ctx, tx, ownerID, appID); err != nil {
		return IssuedSecret{}, err
	}
	var metadata SecretInput
	err = tx.QueryRowContext(ctx, "UPDATE app_secrets SET revoked_at = ? WHERE app_id = ? AND id = ? AND revoked_at IS NULL RETURNING name, description", time.Now().UTC().UnixNano(), appID, secretID).Scan(&metadata.Name, &metadata.Description)
	if errors.Is(err, sql.ErrNoRows) {
		var found int
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM app_secrets WHERE app_id = ? AND id = ?", appID, secretID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return IssuedSecret{}, ErrNotFound
		}
		if err != nil {
			return IssuedSecret{}, fmt.Errorf("check credential reset state: %w", err)
		}
		return IssuedSecret{}, ErrRevoked
	}
	if err != nil {
		return IssuedSecret{}, fmt.Errorf("revoke app credential for reset: %w", err)
	}
	issued, digest := newCredential(metadata)
	if err := insertCredential(ctx, tx, appID, issued, digest); err != nil {
		return IssuedSecret{}, err
	}
	if err := tx.Commit(); err != nil {
		return IssuedSecret{}, fmt.Errorf("commit credential reset: %w", err)
	}
	return issued, nil
}

func (s *Store) Authenticate(ctx context.Context, appID, plaintext string) (App, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return App{}, fmt.Errorf("begin app authentication: %w", err)
	}
	defer tx.Rollback()
	app, err := scanApp(tx.QueryRowContext(ctx, "SELECT "+appColumns+" FROM apps WHERE id = ?", appID))
	if errors.Is(err, ErrNotFound) {
		return App{}, ErrUnauthorized
	}
	if err != nil {
		return App{}, err
	}
	if !app.Enabled {
		return App{}, ErrUnauthorized
	}
	rows, err := tx.QueryContext(ctx, "SELECT digest FROM app_secrets WHERE app_id = ? AND revoked_at IS NULL", appID)
	if err != nil {
		return App{}, fmt.Errorf("query live app credentials: %w", err)
	}
	defer rows.Close()
	digest := sha256.Sum256([]byte(plaintext))
	matched := 0
	for rows.Next() {
		var stored []byte
		if err := rows.Scan(&stored); err != nil {
			return App{}, fmt.Errorf("read app credential digest: %w", err)
		}
		matched |= subtle.ConstantTimeCompare(digest[:], stored)
	}
	if err := rows.Err(); err != nil {
		return App{}, fmt.Errorf("read live app credentials: %w", err)
	}
	if matched != 1 {
		return App{}, ErrUnauthorized
	}
	return app, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanSecret(row rowScanner) (Secret, error) {
	var secret Secret
	var created int64
	var revoked sql.NullInt64
	if err := row.Scan(&secret.ID, &secret.Name, &secret.Description, &created, &revoked); err != nil {
		return Secret{}, fmt.Errorf("read app credential metadata: %w", err)
	}
	secret.CreatedAt = time.Unix(0, created).UTC()
	if revoked.Valid {
		timestamp := time.Unix(0, revoked.Int64).UTC()
		secret.RevokedAt = &timestamp
	}
	return secret, nil
}

func scanApp(row rowScanner) (App, error) {
	var app App
	var redirects, providers string
	var created, updated int64
	if err := row.Scan(&app.ID, &app.Name, &app.Enabled, &redirects, &providers, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return App{}, ErrNotFound
		}
		return App{}, fmt.Errorf("read app: %w", err)
	}
	if err := json.Unmarshal([]byte(redirects), &app.RedirectURIs); err != nil {
		return App{}, fmt.Errorf("decode app redirect URIs: %w", err)
	}
	if err := json.Unmarshal([]byte(providers), &app.Providers); err != nil {
		return App{}, fmt.Errorf("decode app providers: %w", err)
	}
	app.CreatedAt, app.UpdatedAt = time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
	return app, nil
}

func encodeLists(input AppInput) (string, string) {
	redirects, _ := json.Marshal(input.RedirectURIs)
	providers, _ := json.Marshal(input.Providers)
	return string(redirects), string(providers)
}

func requireOwnedApp(ctx context.Context, tx *sql.Tx, ownerID, appID string) error {
	var id string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM apps WHERE id = ? AND owner_id = ? AND system = 0", appID, ownerID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("check app existence: %w", err)
	}
	return nil
}

func requireAffected(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count affected app records: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func randomToken(bytes int) string {
	value := make([]byte, bytes)
	rand.Read(value)
	return base64.RawURLEncoding.EncodeToString(value)
}

func newCredential(input SecretInput) (IssuedSecret, []byte) {
	id := randomToken(16)
	secret := randomToken(32)
	digest := sha256.Sum256([]byte(secret))
	return IssuedSecret{ID: id, Name: input.Name, Description: input.Description, Secret: secret, CreatedAt: time.Now().UTC()}, digest[:]
}

func insertCredential(ctx context.Context, tx *sql.Tx, appID string, issued IssuedSecret, digest []byte) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO app_secrets (id, app_id, name, description, digest, created_at) VALUES (?, ?, ?, ?, ?, ?)", issued.ID, appID, issued.Name, issued.Description, digest, issued.CreatedAt.UnixNano()); err != nil {
		return fmt.Errorf("create app credential: %w", err)
	}
	return nil
}
