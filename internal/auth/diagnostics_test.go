package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/providers"
)

func TestCallbackCorrelatesProviderDiagnosticsAndSanitizesOAuthFailure(t *testing.T) {
	var logs bytes.Buffer
	f := newFixture(t, func(config *Config) { config.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": "https://accounts.google.com", "authorization_endpoint": "https://accounts.google.com/authorize", "token_endpoint": "https://accounts.google.com/token", "jwks_uri": "https://accounts.google.com/keys", "userinfo_endpoint": "https://accounts.google.com/userinfo"})
		case "/token":
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "PRIVATE_UPSTREAM_DETAIL"})
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
		}
	}))
	t.Cleanup(upstream.Close)
	client := upstream.Client()
	client.Timeout = time.Second
	endpoint, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.Transport = diagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
		copy := request.Clone(request.Context())
		copy.URL.Scheme, copy.URL.Host = endpoint.Scheme, endpoint.Host
		return http.DefaultTransport.RoundTrip(copy)
	})
	provider, err := providers.NewGoogle(context.Background(), providers.Config{ClientID: "PRIVATE_CLIENT_ID", ClientSecret: "PRIVATE_CLIENT_SECRET", RedirectURL: "https://auth.example/auth/callback/google", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	f.google.exchange = provider.Exchange
	flow := f.start("product-state")
	response := f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"PRIVATE_AUTHORIZATION_CODE"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil || location.Query().Get("error") != "provider_failed" {
		t.Fatalf("provider failure changed: %v %v", location, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	var providerFailure, authFailure map[string]any
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record["component"] == "provider" && record["phase"] == "completed" {
			providerFailure = record
		}
		if record["component"] == "auth" && record["phase"] == "provider_exchange" {
			authFailure = record
		}
	}
	if providerFailure == nil || authFailure == nil {
		t.Fatalf("missing correlated failure records: %s", &logs)
	}
	for _, record := range []map[string]any{providerFailure, authFailure} {
		if record["request_id"] != response.Header().Get("X-Request-ID") || record["operation_id"] == "" || record["operation_id"] != authFailure["operation_id"] || record["app_id"] != f.app.App.ID || record["provider"] != "google" {
			t.Fatalf("provider diagnostics lost gateway correlation: %v", record)
		}
	}
	if providerFailure["status"] != float64(401) || providerFailure["error_type"] != "oauth_response" || strings.Contains(logs.String(), "PRIVATE_") || strings.Contains(logs.String(), upstream.URL) {
		t.Fatalf("unsafe or incomplete OAuth diagnostics: %s", &logs)
	}
}

type diagnosticRoundTripper func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
