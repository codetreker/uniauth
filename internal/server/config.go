package server

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"unicode"
)

type Credentials struct {
	ClientID     string
	ClientSecret string
}

type Config struct {
	PublicURL         string
	ListenAddr        string
	DatabasePath      string
	InitialAdminEmail string
	AllowLoopbackHTTP bool
	Microsoft         Credentials
	Google            Credentials
	GitHub            Credentials
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		PublicURL:         getenv("UNIAUTH_PUBLIC_URL"),
		ListenAddr:        getenv("UNIAUTH_LISTEN_ADDR"),
		DatabasePath:      getenv("UNIAUTH_DATABASE_PATH"),
		InitialAdminEmail: getenv("UNIAUTH_INITIAL_ADMIN_EMAIL"),
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "uniauth.db"
	}
	switch getenv("UNIAUTH_ALLOW_LOOPBACK_HTTP") {
	case "", "false":
	case "true":
		cfg.AllowLoopbackHTTP = true
	default:
		return Config{}, errors.New("UNIAUTH_ALLOW_LOOPBACK_HTTP must be true or false")
	}
	cfg.Microsoft = credentialPair(getenv, "MICROSOFT")
	cfg.Google = credentialPair(getenv, "GOOGLE")
	cfg.GitHub = credentialPair(getenv, "GITHUB")
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	if cfg.Microsoft.ClientID == "" && cfg.Google.ClientID == "" && cfg.GitHub.ClientID == "" {
		return Config{}, errors.New("at least one OAuth provider must be configured")
	}
	return cfg, nil
}

func credentialPair(getenv func(string) string, name string) Credentials {
	return Credentials{getenv("UNIAUTH_" + name + "_CLIENT_ID"), getenv("UNIAUTH_" + name + "_CLIENT_SECRET")}
}

func (cfg Config) validate() error {
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(cfg.PublicURL, "#") || (u.Path != "" && u.Path != "/") {
		return errors.New("UNIAUTH_PUBLIC_URL must contain only an absolute origin")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback && cfg.AllowLoopbackHTTP) {
		return errors.New("UNIAUTH_PUBLIC_URL requires HTTPS or explicitly enabled loopback HTTP")
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddr); err != nil {
		return fmt.Errorf("UNIAUTH_LISTEN_ADDR must be a host:port address: %w", err)
	}
	if cfg.DatabasePath == "" {
		return errors.New("UNIAUTH_DATABASE_PATH is required")
	}
	address, err := mail.ParseAddress(cfg.InitialAdminEmail)
	if err != nil || len(cfg.InitialAdminEmail) > 254 || address.Address != cfg.InitialAdminEmail || strings.IndexFunc(cfg.InitialAdminEmail, unicode.IsSpace) >= 0 {
		return errors.New("UNIAUTH_INITIAL_ADMIN_EMAIL must be a plain email address of at most 254 bytes without whitespace")
	}
	for _, entry := range []struct {
		name        string
		credentials Credentials
	}{{"MICROSOFT", cfg.Microsoft}, {"GOOGLE", cfg.Google}, {"GITHUB", cfg.GitHub}} {
		c := entry.credentials
		if (c.ClientID == "") != (c.ClientSecret == "") {
			return fmt.Errorf("UNIAUTH_%s_CLIENT_ID and UNIAUTH_%s_CLIENT_SECRET must be configured together", entry.name, entry.name)
		}
		if c.ClientID != strings.TrimSpace(c.ClientID) || c.ClientSecret != strings.TrimSpace(c.ClientSecret) {
			return fmt.Errorf("UNIAUTH_%s credentials must not contain surrounding whitespace", entry.name)
		}
	}
	return nil
}
