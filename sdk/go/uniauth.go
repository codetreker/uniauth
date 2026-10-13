// Package uniauth connects confidential product backends to the UniAuth login
// gateway. Products own browser-bound login state, users, and login sessions.
package uniauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Provider string

const (
	Microsoft Provider = "microsoft"
	Google    Provider = "google"
	GitHub    Provider = "github"
)

type Config struct {
	BaseURL           string
	AppID             string
	AppSecret         string
	AllowLoopbackHTTP bool
	// HTTPClient is copied; its transport, cookie jar, and timeout remain in use.
	// Redirects are disabled on the copy. A nil client selects a 30-second timeout.
	HTTPClient *http.Client
}

// Client is safe for concurrent use when its supplied HTTP transport and jar are.
type Client struct {
	baseURL           url.URL
	appID             string
	appSecret         string
	allowLoopbackHTTP bool
	httpClient        http.Client
}

// New requires an HTTPS origin and valid App credentials. Loopback HTTP requires
// explicit opt-in for both gateway and callback URLs.
func New(config Config) (*Client, error) {
	baseURL, err := validateBaseURL(config.BaseURL, config.AllowLoopbackHTTP)
	if err != nil {
		return nil, err
	}
	if !validValue(config.AppID, 128) || strings.Contains(config.AppID, ":") {
		return nil, errors.New("uniauth: App ID requires 1 to 128 UTF-8 bytes without a colon")
	}
	if !validValue(config.AppSecret, 512) {
		return nil, errors.New("uniauth: App Secret requires 1 to 512 UTF-8 bytes")
	}
	httpClient := http.Client{Timeout: 30 * time.Second}
	if config.HTTPClient != nil {
		httpClient = *config.HTTPClient
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{baseURL: *baseURL, appID: config.AppID, appSecret: config.AppSecret, allowLoopbackHTTP: config.AllowLoopbackHTTP, httpClient: httpClient}, nil
}

// GenerateState returns 32 cryptographically random bytes encoded as unpadded
// URL-safe Base64. The product must bind it to the initiating browser.
func GenerateState() string {
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}

type AuthorizationRequest struct {
	Provider    Provider
	RedirectURI string
	State       string
}

// AuthorizationURL validates the request locally and preserves RedirectURI as
// supplied. Registration and provider availability are checked by the gateway.
func (c *Client) AuthorizationURL(request AuthorizationRequest) (string, error) {
	if !supportedProvider(request.Provider) {
		return "", errors.New("uniauth: unsupported provider")
	}
	if err := validateRedirectURI(request.RedirectURI, c.allowLoopbackHTTP); err != nil {
		return "", err
	}
	if !validValue(request.State, 2048) {
		return "", errors.New("uniauth: state requires 1 to 2048 UTF-8 bytes")
	}
	u := c.baseURL
	u.Path = "/auth/authorize"
	u.RawQuery = url.Values{"app_id": {c.appID}, "provider": {string(request.Provider)}, "redirect_uri": {request.RedirectURI}, "state": {request.State}}.Encode()
	return u.String(), nil
}

// Identity validates only the stable envelope. Sources retain each source's JSON
// without converting numbers or imposing provider-specific field requirements.
type Identity struct {
	Provider Provider                   `json:"provider"`
	Subject  string                     `json:"subject"`
	Sources  map[string]json.RawMessage `json:"sources"`
	// RawJSON contains the complete response bytes, including unknown envelope fields.
	RawJSON json.RawMessage `json:"-"`
}

// Retrieve makes one POST and never follows redirects or retries. The product
// must atomically consume its browser-bound pending state before calling it.
// The gateway consumes a valid result before responding; a lost response requires
// a new login. Context, transport, and body errors retain their original causes.
func (c *Client) Retrieve(ctx context.Context, code string) (*Identity, error) {
	if ctx == nil {
		return nil, errors.New("uniauth: retrieval context is required")
	}
	if !validValue(code, 512) {
		return nil, errors.New("uniauth: result code requires 1 to 512 UTF-8 bytes")
	}
	payload, _ := json.Marshal(struct {
		Code string `json:"code"`
	}{Code: code})
	u := c.baseURL
	u.Path = "/auth/result"
	// A streaming reader leaves GetBody nil. Even transports that use replayable
	// requests cannot replay this one-time claim after a partial network failure.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), io.NopCloser(strings.NewReader(string(payload))))
	if err != nil {
		return nil, &RequestError{Cause: err}
	}
	request.SetBasicAuth(c.appID, c.appSecret)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, &RequestError{Cause: err}
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if cause := errors.Join(readErr, closeErr); cause != nil {
		return nil, &ProtocolError{StatusCode: response.StatusCode, RequestID: response.Header.Get("X-Request-ID"), Reason: "response_body", Cause: cause}
	}
	return parseResponse(response.StatusCode, response.Header, body)
}
