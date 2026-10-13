package apps

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("app or credential not found")
	ErrRevoked      = errors.New("app credential already revoked")
	ErrInvalid      = errors.New("invalid application configuration")
	ErrUnauthorized = errors.New("invalid application credentials")
)

type Config struct {
	Path              string
	AllowLoopbackHTTP bool
}

type AppInput struct {
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	RedirectURIs []string `json:"redirect_uris"`
	Providers    []string `json:"providers"`
}

type Input = AppInput

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
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type CreatedApp struct {
	App      App    `json:"app"`
	Secret   string `json:"secret"`
	SecretID string `json:"secret_id"`
}

type IssuedSecret struct {
	ID        string    `json:"id"`
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
}
