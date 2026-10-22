package apps

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/codetreker/uniauth/internal/providers"
)

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) InitializePlatform(ctx context.Context, email string) error {
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 || !utf8.ValidString(email) || strings.IndexFunc(email, unicode.IsSpace) >= 0 {
		return fmt.Errorf("%w: initial administrator email must be an email address", ErrInvalid)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO platform_state (id, initial_admin_email) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET initial_admin_email = excluded.initial_admin_email WHERE platform_state.claimed_account_id IS NULL`, strings.ToLower(email))
	if err != nil {
		return fmt.Errorf("initialize platform: %w", err)
	}
	return nil
}

func (s *Store) ProvisionConsole(ctx context.Context, callback string, providerNames []string) (CreatedApp, error) {
	input := AppInput{Name: "UniAuth Console", Enabled: true, RedirectURIs: []string{callback}, Providers: providerNames}
	if err := validateInput(input); err != nil {
		return CreatedApp{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedApp{}, fmt.Errorf("begin console provisioning: %w", err)
	}
	defer tx.Rollback()
	var stored sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT console_app_id FROM platform_state WHERE id = 1").Scan(&stored); err != nil {
		return CreatedApp{}, fmt.Errorf("read console application state: %w", err)
	}
	id := stored.String
	now := time.Now().UTC()
	redirects, names := encodeLists(input)
	var app App
	if !stored.Valid {
		id = randomToken(16)
		if _, err := tx.ExecContext(ctx, `INSERT INTO apps (id,owner_id,system,name,enabled,redirect_uris,providers,created_at,updated_at) VALUES (?, 'console', 1, ?, 1, ?, ?, ?, ?)`, id, input.Name, redirects, names, now.UnixNano(), now.UnixNano()); err != nil {
			return CreatedApp{}, fmt.Errorf("create console application: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE platform_state SET console_app_id = ? WHERE id = 1", id); err != nil {
			return CreatedApp{}, fmt.Errorf("record console application: %w", err)
		}
	} else {
		result, err := tx.ExecContext(ctx, "UPDATE apps SET name = ?, enabled = 1, redirect_uris = ?, providers = ?, updated_at = ? WHERE id = ? AND system = 1", input.Name, redirects, names, now.UnixNano(), id)
		if err != nil {
			return CreatedApp{}, fmt.Errorf("reconcile console application: %w", err)
		}
		if err := requireAffected(result); err != nil {
			return CreatedApp{}, err
		}
	}
	app, err = scanApp(tx.QueryRowContext(ctx, "SELECT "+appColumns+" FROM apps WHERE id = ?", id))
	if err != nil {
		return CreatedApp{}, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM app_secrets WHERE app_id = ?", id); err != nil {
		return CreatedApp{}, fmt.Errorf("replace console credentials: %w", err)
	}
	issued, digest := newCredential(SecretInput{Name: "Console backend", Description: "Server-managed credential"})
	if err := insertCredential(ctx, tx, id, issued, digest); err != nil {
		return CreatedApp{}, err
	}
	if err := tx.Commit(); err != nil {
		return CreatedApp{}, fmt.Errorf("commit console provisioning: %w", err)
	}
	return CreatedApp{App: app, Secret: issued.Secret, SecretID: issued.ID}, nil
}

const accountColumns = "id, email, name, avatar_url, is_admin, created_at"

func (s *Store) Account(ctx context.Context, id string) (Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE id = ?", id))
}

func (s *Store) ResolveAccount(ctx context.Context, identity providers.Identity) (Account, error) {
	if err := validateIdentity(identity); err != nil {
		return Account{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, fmt.Errorf("begin account resolution: %w", err)
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, "SELECT account_id FROM account_identities WHERE provider = ? AND provider_id = ?", identity.Provider, identity.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = randomToken(16)
		if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id,email,name,avatar_url,is_admin,created_at) VALUES (?, ?, ?, ?, 0, ?)`, id, identity.Email, identity.Name, identity.AvatarURL, time.Now().UTC().UnixNano()); err != nil {
			return Account{}, fmt.Errorf("create platform account: %w", err)
		}
		if err := insertIdentity(ctx, tx, id, identity); err != nil {
			return Account{}, err
		}
	} else if err != nil {
		return Account{}, fmt.Errorf("lookup platform identity: %w", err)
	} else {
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET email = ?, name = ?, avatar_url = ? WHERE id = ?", identity.Email, identity.Name, identity.AvatarURL, id); err != nil {
			return Account{}, fmt.Errorf("refresh account profile: %w", err)
		}
		if err := refreshIdentity(ctx, tx, identity); err != nil {
			return Account{}, err
		}
	}
	if err := claimAdministrator(ctx, tx, id, identity); err != nil {
		return Account{}, err
	}
	account, err := scanAccount(tx.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE id = ?", id))
	if err != nil {
		return Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, fmt.Errorf("commit account resolution: %w", err)
	}
	return account, nil
}

func (s *Store) LinkIdentity(ctx context.Context, accountID string, identity providers.Identity) error {
	if err := validateIdentity(identity); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin identity linking: %w", err)
	}
	defer tx.Rollback()
	if err := requireAccount(ctx, tx, accountID); err != nil {
		return err
	}
	var owner string
	err = tx.QueryRowContext(ctx, "SELECT account_id FROM account_identities WHERE provider = ? AND provider_id = ?", identity.Provider, identity.ID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		if err := insertIdentity(ctx, tx, accountID, identity); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("lookup linked identity: %w", err)
	} else if owner != accountID {
		return ErrIdentityConflict
	} else if err := refreshIdentity(ctx, tx, identity); err != nil {
		return err
	}
	if err := claimAdministrator(ctx, tx, accountID, identity); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit identity linking: %w", err)
	}
	return nil
}

func (s *Store) ListIdentities(ctx context.Context, accountID string) ([]LinkedIdentity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider, provider_id, email, email_verified, name, linked_at FROM account_identities WHERE account_id = ? ORDER BY linked_at, provider, provider_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list linked identities: %w", err)
	}
	defer rows.Close()
	identities := make([]LinkedIdentity, 0)
	for rows.Next() {
		var identity LinkedIdentity
		var timestamp int64
		if err := rows.Scan(&identity.Provider, &identity.ID, &identity.Email, &identity.EmailVerified, &identity.Name, &timestamp); err != nil {
			return nil, fmt.Errorf("read linked identity: %w", err)
		}
		identity.LinkedAt = time.Unix(0, timestamp).UTC()
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read linked identities: %w", err)
	}
	return identities, nil
}

type userCursor struct {
	CreatedAt int64  `json:"created_at"`
	ID        string `json:"id"`
}

func (s *Store) ListUsers(ctx context.Context, actorID, cursor string, limit int) (UserPage, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return UserPage{}, fmt.Errorf("%w: user page limit must be between 1 and 100", ErrInvalid)
	}
	var position userCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return UserPage{}, fmt.Errorf("%w: invalid user cursor", ErrInvalid)
		}
		if err := json.Unmarshal(raw, &position); err != nil || position.ID == "" || position.CreatedAt <= 0 {
			return UserPage{}, fmt.Errorf("%w: invalid user cursor", ErrInvalid)
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return UserPage{}, fmt.Errorf("begin user listing: %w", err)
	}
	defer tx.Rollback()
	var admin bool
	err = tx.QueryRowContext(ctx, "SELECT is_admin FROM accounts WHERE id = ?", actorID).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !admin {
		return UserPage{}, ErrForbidden
	}
	if err != nil {
		return UserPage{}, fmt.Errorf("authorize user listing: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id, a.email, a.name, a.created_at, (SELECT COUNT(*) FROM apps WHERE owner_id = a.id AND system = 0) FROM accounts a WHERE a.created_at > ? OR (a.created_at = ? AND a.id > ?) ORDER BY a.created_at, a.id LIMIT ?`, position.CreatedAt, position.CreatedAt, position.ID, limit+1)
	if err != nil {
		return UserPage{}, fmt.Errorf("list platform users: %w", err)
	}
	defer rows.Close()
	page := UserPage{Users: make([]UserSummary, 0)}
	for rows.Next() {
		var user UserSummary
		var timestamp int64
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &timestamp, &user.AppCount); err != nil {
			return UserPage{}, fmt.Errorf("read platform user: %w", err)
		}
		user.CreatedAt = time.Unix(0, timestamp).UTC()
		page.Users = append(page.Users, user)
	}
	if err := rows.Err(); err != nil {
		return UserPage{}, fmt.Errorf("read platform users: %w", err)
	}
	if len(page.Users) > limit {
		page.Users = page.Users[:limit]
		last := page.Users[limit-1]
		raw, _ := json.Marshal(userCursor{CreatedAt: last.CreatedAt.UnixNano(), ID: last.ID})
		next := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &next
	}
	return page, nil
}

func requireAccount(ctx context.Context, tx *sql.Tx, id string) error {
	var exists int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM accounts WHERE id = ?", id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check account existence: %w", err)
	}
	return nil
}

func scanAccount(row rowScanner) (Account, error) {
	var account Account
	var timestamp int64
	if err := row.Scan(&account.ID, &account.Email, &account.Name, &account.AvatarURL, &account.IsAdmin, &timestamp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("read platform account: %w", err)
	}
	account.CreatedAt = time.Unix(0, timestamp).UTC()
	return account, nil
}

func validateIdentity(identity providers.Identity) error {
	if identity.ID == "" || len(identity.ID) > 512 || !utf8.ValidString(identity.ID) {
		return fmt.Errorf("%w: stable provider identity is required", ErrInvalid)
	}
	switch identity.Provider {
	case "microsoft", "google", "github":
	default:
		return fmt.Errorf("%w: unsupported identity provider", ErrInvalid)
	}
	return nil
}

func insertIdentity(ctx context.Context, tx *sql.Tx, accountID string, identity providers.Identity) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_identities (provider,provider_id,account_id,email,email_verified,name,linked_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, identity.Provider, identity.ID, accountID, identity.Email, identity.EmailVerified, identity.Name, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("link platform identity: %w", err)
	}
	return nil
}

func refreshIdentity(ctx context.Context, tx *sql.Tx, identity providers.Identity) error {
	if _, err := tx.ExecContext(ctx, "UPDATE account_identities SET email = ?, email_verified = ?, name = ? WHERE provider = ? AND provider_id = ?", identity.Email, identity.EmailVerified, identity.Name, identity.Provider, identity.ID); err != nil {
		return fmt.Errorf("refresh linked identity: %w", err)
	}
	return nil
}

func claimAdministrator(ctx context.Context, tx *sql.Tx, accountID string, identity providers.Identity) error {
	// Bootstrap trusts the normalized verification assertion from our provider adapters.
	// Google exposes email_verified; GitHub exposes verified in authenticated email records.
	// Microsoft email claims do not establish verification or authorization:
	// https://developers.google.com/identity/openid-connect/openid-connect#an-id-tokens-payload
	// https://docs.github.com/en/rest/users/emails#list-email-addresses-for-the-authenticated-user
	// https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference
	if identity.Provider != "google" && identity.Provider != "github" || identity.Email == nil || identity.EmailVerified == nil || !*identity.EmailVerified {
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE platform_state SET claimed_account_id = ? WHERE id = 1 AND claimed_account_id IS NULL AND initial_admin_email = ?`, accountID, strings.ToLower(*identity.Email))
	if err != nil {
		return fmt.Errorf("claim initial administrator: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count administrator claims: %w", err)
	}
	if count == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE accounts SET is_admin = 1 WHERE id = ?", accountID); err != nil {
		return fmt.Errorf("grant initial administrator: %w", err)
	}
	return nil
}
