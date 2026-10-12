package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Identity contains verified identifiers and complete upstream identity sources.
// OAuth credentials are never included in Sources.
type Identity struct {
	Provider string                     `json:"provider"`
	Subject  string                     `json:"subject"`
	Sources  map[string]json.RawMessage `json:"sources"`
}

type AuthorizationRequest struct {
	State        string
	Nonce        string
	CodeVerifier string
}

type ExchangeRequest struct {
	Code          string
	ExpectedNonce string
	CodeVerifier  string
}

type Provider interface {
	Name() string
	AuthorizationURL(AuthorizationRequest) (string, error)
	Exchange(context.Context, ExchangeRequest) (Identity, error)
}

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

// Error exposes a safe operation label while retaining the underlying cause.
type Error struct {
	Operation string
	Cause     error
}

func (e *Error) Error() string { return "provider " + e.Operation + " failed" }
func (e *Error) Unwrap() error { return e.Cause }

func failure(operation string, cause error) error {
	return &Error{Operation: operation, Cause: cause}
}

func validateConfig(config Config) error {
	if config.ClientID == "" || config.ClientSecret == "" {
		return errors.New("provider client ID and secret are required")
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.Host == "" || redirect.User != nil || redirect.Fragment != "" || (redirect.Scheme != "https" && redirect.Scheme != "http") {
		return errors.New("provider redirect URL is invalid")
	}
	if config.HTTPClient == nil || config.HTTPClient.Timeout <= 0 || config.HTTPClient.Timeout > time.Minute {
		return errors.New("provider HTTP client needs a timeout between zero and one minute")
	}
	return nil
}

func validateAuthorization(request AuthorizationRequest, oidc bool) error {
	if request.State == "" || !validVerifier(request.CodeVerifier) || (oidc && request.Nonce == "") {
		return errors.New("invalid authorization request")
	}
	return nil
}

func validateExchange(request ExchangeRequest, oidc bool) error {
	if request.Code == "" || !validVerifier(request.CodeVerifier) || (oidc && request.ExpectedNonce == "") {
		return errors.New("invalid exchange request")
	}
	return nil
}

func validVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	return true
}
