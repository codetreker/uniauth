package console

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/auth"
)

type Config struct {
	PublicURL         string
	Gateway           *auth.Handler
	AppID             string
	AppSecret         string
	AllowLoopbackHTTP bool
	SessionTTL        time.Duration
	MaxSessions       int
	PendingTTL        time.Duration
	MaxPending        int
	Now               func() time.Time
	Logger            *slog.Logger
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
	if cfg.Gateway == nil || cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, errors.New("console requires a gateway and application credentials")
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
	if cfg.PendingTTL == 0 {
		cfg.PendingTTL = 10 * time.Minute
	}
	if cfg.PendingTTL < time.Second || cfg.PendingTTL > 10*time.Minute {
		return nil, errors.New("console pending login lifetime must be between one second and ten minutes")
	}
	if cfg.MaxPending == 0 {
		cfg.MaxPending = 1000
	}
	if cfg.MaxPending < 1 || cfg.MaxPending > 100000 {
		return nil, errors.New("console pending login capacity must be between one and 100000")
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
	if len(available) == 0 {
		return nil, errors.New("console requires at least one available OAuth provider")
	}
	cookieName := "__Host-uniauth_console"
	if u.Scheme == "http" {
		cookieName = "uniauth_console"
	}
	return &Handler{
		store: store, config: cfg, origin: origin,
		cookieName: cookieName, secure: u.Scheme == "https", available: available,
		sessions: make(map[[32]byte]consoleSession), pending: make(map[string]pendingLogin),
	}, nil
}
