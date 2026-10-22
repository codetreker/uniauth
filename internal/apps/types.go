package apps

import (
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("app or credential not found")
	ErrRevoked          = errors.New("app credential already revoked")
	ErrInvalid          = errors.New("invalid application configuration")
	ErrForbidden        = errors.New("platform administrator required")
	ErrIdentityConflict = errors.New("identity belongs to another account")
	ErrUnauthorized     = errors.New("invalid application credentials")
)

type Config struct {
	Path string
}

type AppInput struct {
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	RedirectURIs []string `json:"redirect_uris"`
	Providers    []string `json:"providers"`
}

type Input = AppInput

type SecretInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CreateAppInput struct {
	AppInput
	InitialSecret *SecretInput `json:"initial_secret,omitempty"`
}

type App struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Enabled      bool      `json:"enabled"`
	RedirectURIs []string  `json:"redirect_uris"`
	Providers    []string  `json:"providers"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Secret struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type CreatedApp struct {
	App      App    `json:"app"`
	Secret   string `json:"secret,omitempty"`
	SecretID string `json:"secret_id,omitempty"`
}

type IssuedSecret struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Secret      string    `json:"secret"`
	CreatedAt   time.Time `json:"created_at"`
}

type Account struct {
	ID        string    `json:"id"`
	Email     *string   `json:"email"`
	Name      *string   `json:"name"`
	AvatarURL *string   `json:"avatar_url"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

type LinkedIdentity struct {
	Provider      string    `json:"provider"`
	ID            string    `json:"id"`
	Email         *string   `json:"email"`
	EmailVerified *bool     `json:"email_verified"`
	Name          *string   `json:"name"`
	LinkedAt      time.Time `json:"linked_at"`
}

type UserSummary struct {
	ID        string    `json:"id"`
	Email     *string   `json:"email"`
	Name      *string   `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	AppCount  int       `json:"app_count"`
}

type UserPage struct {
	Users      []UserSummary `json:"users"`
	NextCursor *string       `json:"next_cursor"`
}
