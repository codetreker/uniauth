package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
)

const ResultTTL = 5 * time.Minute

var (
	ErrUnavailable = errors.New("authentication state is unavailable")
	ErrCapacity    = errors.New("authentication capacity is exhausted")
	ErrClosed      = errors.New("authentication service is closed")
)

type Config struct {
	PublicURL           string
	AllowLoopbackHTTP   bool
	TransactionTTL      time.Duration
	CallbackTimeout     time.Duration
	CleanupInterval     time.Duration
	MaxTransactions     int
	MaxTransactionBytes int64
	MaxResults          int
	MaxResultBytes      int64
	Logger              *slog.Logger
	Now                 func() time.Time
}

func (c Config) validate() (bool, error) {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return false, fmt.Errorf("parse authentication public URL: %w", err)
	}
	if u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false, errors.New("authentication public URL must be an origin")
	}
	secure := u.Scheme == "https"
	if !secure && !(c.AllowLoopbackHTTP && u.Scheme == "http" && loopback(u.Hostname())) {
		return false, errors.New("authentication public URL requires HTTPS or enabled loopback HTTP")
	}
	if c.TransactionTTL <= 0 || c.CallbackTimeout <= 0 || c.CleanupInterval <= 0 || c.MaxTransactions <= 0 || c.MaxTransactionBytes <= 0 || c.MaxResults <= 0 || c.MaxResultBytes <= 0 {
		return false, errors.New("authentication durations and capacities must be positive")
	}
	return secure, nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
