package console

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/codetreker/uniauth/internal/apps"
)

type Config struct {
	PublicURL           string
	PasswordHash        string
	AllowLoopbackHTTP   bool
	SessionTTL          time.Duration
	MaxSessions         int
	LoginInterval       time.Duration
	PasswordConcurrency int
	Now                 func() time.Time
	Logger              *slog.Logger
}

func New(store *apps.Store, availableProviders []string, cfg Config) (*Handler, error) {
	if store == nil {
		return nil, errors.New("console requires an application store")
	}
	u, err := url.Parse(cfg.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("parse console public URL: %w", err)
	}
	if u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(cfg.PublicURL, "#") || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("console public URL must contain only an origin")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback && cfg.AllowLoopbackHTTP) {
		return nil, errors.New("console public URL requires HTTPS or explicitly enabled loopback HTTP")
	}
	origin, err := configuredOrigin(u)
	if err != nil {
		return nil, fmt.Errorf("canonicalize console public URL origin: %w", err)
	}
	if err := validatePasswordHash(cfg.PasswordHash); err != nil {
		return nil, err
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 8 * time.Hour
	}
	if cfg.SessionTTL < time.Second || cfg.SessionTTL > 24*time.Hour {
		return nil, errors.New("console session lifetime must be between one second and 24 hours")
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = 1000
	}
	if cfg.MaxSessions < 1 || cfg.MaxSessions > 100000 {
		return nil, errors.New("console session capacity must be between one and 100000")
	}
	if cfg.LoginInterval == 0 {
		cfg.LoginInterval = time.Second
	}
	if cfg.LoginInterval < time.Millisecond || cfg.LoginInterval > time.Minute {
		return nil, errors.New("console login interval must be between one millisecond and one minute")
	}
	if cfg.PasswordConcurrency == 0 {
		cfg.PasswordConcurrency = 2
	}
	if cfg.PasswordConcurrency < 1 || cfg.PasswordConcurrency > 4 {
		return nil, errors.New("console password concurrency must be between one and four")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	available := make(map[string]bool, len(availableProviders))
	for _, provider := range availableProviders {
		switch provider {
		case "microsoft", "google", "github":
			available[provider] = true
		default:
			return nil, fmt.Errorf("unknown available console provider %q", provider)
		}
	}
	cookieName := "__Host-uniauth_admin"
	if u.Scheme == "http" {
		cookieName = "uniauth_admin"
	}
	return &Handler{
		store: store, config: cfg, origin: origin,
		cookieName: cookieName, secure: u.Scheme == "https", available: available,
		sessions: make(map[[32]byte]adminSession), passwordSlots: make(chan struct{}, cfg.PasswordConcurrency),
		loginTokens: loginBurst, lastLoginRefill: cfg.Now(),
	}, nil
}

func validatePasswordHash(hash string) error {
	if len(hash) > 512 {
		return errors.New("console password hash exceeds the maximum length")
	}
	params, salt, key, err := argon2id.DecodeHash(hash)
	if err != nil {
		return fmt.Errorf("decode console Argon2id password hash: %w", err)
	}
	canonical := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", params.Memory, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	if canonical != hash || params.Memory < 19456 || params.Memory > 131072 || params.Iterations < 1 || params.Iterations > 10 ||
		params.Parallelism < 1 || params.Parallelism > 16 || len(salt) < 16 || len(salt) > 64 || len(key) < 32 || len(key) > 64 {
		return errors.New("console password hash must use canonical Argon2id with bounded secure parameters")
	}
	return nil
}
