package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/providers"
)

func (f *fixture) providerRequest(body io.Reader) *http.Request {
	f.t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/auth/providers", body)
	r.Header.Set("Content-Type", "application/json")
	r.SetBasicAuth(f.app.App.ID, f.app.Secret)
	return r
}

func (f *fixture) discoverRequest(r *http.Request) *httptest.ResponseRecorder {
	f.t.Helper()
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, r)
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Request-ID") == "" {
		f.t.Fatalf("discovery omitted common response headers: %v", response.Header())
	}
	if len(response.Result().Cookies()) != 0 || response.Header().Get("Location") != "" {
		f.t.Fatalf("discovery started a browser flow: %v", response.Header())
	}
	return response
}

func (f *fixture) discover(redirectURI string) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(map[string]string{"redirect_uri": redirectURI})
	if err != nil {
		f.t.Fatal(err)
	}
	return f.discoverRequest(f.providerRequest(bytes.NewReader(body)))
}

func updateDiscoveryApp(t *testing.T, f *fixture, modify func(*apps.AppInput)) {
	t.Helper()
	input := apps.AppInput{Name: f.app.App.Name, Enabled: true, RedirectURIs: slices.Clone(f.app.App.RedirectURIs), Providers: slices.Clone(f.app.App.Providers)}
	modify(&input)
	app, err := f.store.Update(t.Context(), f.ownerID, f.app.App.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	f.app.App = app
}

func assertDiscoveryProviders(t *testing.T, response *httptest.ResponseRecorder, names ...string) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("discovery = %d %s: %s", response.Code, response.Header().Get("Content-Type"), response.Body)
	}
	if names == nil {
		names = []string{}
	}
	want, err := json.Marshal(map[string][]string{"providers": names})
	if err != nil {
		t.Fatal(err)
	}
	if response.Body.String() != string(want)+"\n" {
		t.Fatalf("discovery = %s, want %s", response.Body, want)
	}
}

func TestProviderDiscoveryLiveIntersection(t *testing.T) {
	f := newFixture(t, nil)
	callback := f.app.App.RedirectURIs[0]
	assertDiscoveryProviders(t, f.discover(callback), "google", "github")

	microsoft := &testProvider{name: "microsoft", requests: make(map[string]providers.AuthorizationRequest)}
	f.h.providers["microsoft"] = microsoft
	for _, provider := range []*testProvider{microsoft, f.google, f.github} {
		provider.startHook = func() { t.Error("discovery invoked provider authorization") }
	}
	updateDiscoveryApp(t, f, func(input *apps.AppInput) { input.Providers = []string{"github", "google", "microsoft"} })
	assertDiscoveryProviders(t, f.discover(callback), "microsoft", "google", "github")
	updateDiscoveryApp(t, f, func(input *apps.AppInput) { input.Providers = []string{"github"} })
	assertDiscoveryProviders(t, f.discover(callback), "github")
	delete(f.h.providers, "github")
	assertDiscoveryProviders(t, f.discover(callback))
	updateDiscoveryApp(t, f, func(input *apps.AppInput) { input.Providers = []string{"google", "github"} })
	assertDiscoveryProviders(t, f.discover(callback), "google")
	clear(f.h.providers)
	assertDiscoveryProviders(t, f.discover(callback))
	for _, provider := range []*testProvider{microsoft, f.google, f.github} {
		if provider.calls.Load() != 0 || len(provider.requests) != 0 {
			t.Fatal("discovery probed an OAuth provider")
		}
	}
	f.h.cache.mu.Lock()
	defer f.h.cache.mu.Unlock()
	if len(f.h.cache.transactions) != 0 || len(f.h.cache.results) != 0 || f.h.cache.transactionBytes != 0 || f.h.cache.resultBytes != 0 || f.h.cache.activeCallbacks != 0 {
		t.Fatal("discovery allocated one-time authentication state")
	}
}

func TestProviderDiscoveryDoesNotConsumeExistingState(t *testing.T) {
	f := newFixture(t, nil)
	code := f.complete(f.start("existing-result"))
	pending := f.start("existing-transaction")
	for range 3 {
		assertDiscoveryProviders(t, f.discover(f.app.App.RedirectURIs[0]), "google", "github")
	}
	if response := f.retrieve(code, &f.app); response.Code != http.StatusOK {
		t.Fatalf("discovery disturbed existing result: %d %s", response.Code, response.Body)
	}
	f.complete(pending)
}

func TestProviderDiscoveryExactCallback(t *testing.T) {
	f := newFixture(t, nil)
	const callback = "http://Product.example:8080/callback?existing=preserved&return=%2Fhome"
	updateDiscoveryApp(t, f, func(input *apps.AppInput) { input.RedirectURIs = []string{callback} })
	assertDiscoveryProviders(t, f.discover(callback), "google", "github")
	for _, callback := range []string{
		"https://Product.example:8080/callback?existing=preserved&return=%2Fhome",
		"http://Product.example/callback?existing=preserved&return=%2Fhome",
		"http://product.example:8080/callback?existing=preserved&return=%2Fhome",
		"http://Product.example:8080/callback/?existing=preserved&return=%2Fhome",
		"http://Product.example:8080/callback?return=%2Fhome&existing=preserved",
		"http://Product.example:8080/callback?existing=preserved&return=%2fhome",
		"http://Product.example:8080/callback?existing=preserved&return=/home",
		"http://Product.example:8080/callback?existing=preserved&return=%2Fhome#fragment",
		"javascript:alert(1)",
		"%zz",
	} {
		t.Run(callback, func(t *testing.T) { assertCode(t, f.discover(callback), http.StatusBadRequest, "invalid_request") })
	}
	updateDiscoveryApp(t, f, func(input *apps.AppInput) { input.RedirectURIs = []string{"https://product.example/new-callback"} })
	assertCode(t, f.discover(callback), http.StatusBadRequest, "invalid_request")
	assertDiscoveryProviders(t, f.discover(f.app.App.RedirectURIs[0]), "google", "github")
}

func TestProviderDiscoveryLiveCredentials(t *testing.T) {
	for _, change := range []string{"disabled", "revoked", "reset", "deleted"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, nil)
			callback := f.app.App.RedirectURIs[0]
			assertDiscoveryProviders(t, f.discover(callback), "google", "github")
			switch change {
			case "disabled":
				updateDiscoveryApp(t, f, func(input *apps.AppInput) { input.Enabled = false })
			case "revoked":
				if err := f.store.RevokeSecret(t.Context(), f.ownerID, f.app.App.ID, f.app.SecretID); err != nil {
					t.Fatal(err)
				}
			case "reset":
				issued, err := f.store.ResetSecret(t.Context(), f.ownerID, f.app.App.ID, f.app.SecretID)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					f.app.Secret = issued.Secret
					assertDiscoveryProviders(t, f.discover(callback), "google", "github")
				}()
			case "deleted":
				if err := f.store.Delete(t.Context(), f.ownerID, f.app.App.ID); err != nil {
					t.Fatal(err)
				}
			}
			response := f.discover(callback)
			assertCode(t, response, http.StatusUnauthorized, "invalid_app")
			if response.Header().Get("WWW-Authenticate") != `Basic realm="uniauth"` {
				t.Fatalf("invalid App omitted Basic challenge: %v", response.Header())
			}
		})
	}
}

func TestProviderDiscoveryRequiresSingleBasicCredential(t *testing.T) {
	f := newFixture(t, nil)
	for _, name := range []string{"missing", "bearer", "malformed", "duplicate", "unknown App", "empty ID", "empty secret", "long ID", "long secret", "wrong secret"} {
		t.Run(name, func(t *testing.T) {
			r := f.providerRequest(strings.NewReader(`{}`))
			switch name {
			case "missing":
				r.Header.Del("Authorization")
			case "bearer":
				r.Header.Set("Authorization", "Bearer private-token")
			case "malformed":
				r.Header.Set("Authorization", "Basic not-base64")
			case "duplicate":
				r.Header.Add("Authorization", r.Header.Get("Authorization"))
			case "unknown App":
				r.SetBasicAuth("unknown", f.app.Secret)
			case "empty ID":
				r.SetBasicAuth("", f.app.Secret)
			case "empty secret":
				r.SetBasicAuth(f.app.App.ID, "")
			case "long ID":
				r.SetBasicAuth(strings.Repeat("a", 129), f.app.Secret)
			case "long secret":
				r.SetBasicAuth(f.app.App.ID, strings.Repeat("s", 513))
			case "wrong secret":
				r.SetBasicAuth(f.app.App.ID, "wrong")
			}
			response := f.discoverRequest(r)
			assertCode(t, response, http.StatusUnauthorized, "invalid_app")
			if response.Header().Get("WWW-Authenticate") != `Basic realm="uniauth"` {
				t.Fatalf("invalid credential omitted Basic challenge: %v", response.Header())
			}
		})
	}
}

func TestProviderDiscoveryStrictBoundedJSON(t *testing.T) {
	f := newFixture(t, nil)
	valid, err := json.Marshal(map[string]string{"redirect_uri": f.app.App.RedirectURIs[0]})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "primitive": "1", "missing field": `{}`,
		"null callback": `{"redirect_uri":null}`, "numeric callback": `{"redirect_uri":1}`, "array callback": `{"redirect_uri":[]}`,
		"object callback": `{"redirect_uri":{}}`, "empty callback": `{"redirect_uri":""}`, "wrong case": `{"Redirect_URI":"value"}`,
		"unknown": `{"unknown":true,"redirect_uri":"value"}`, "duplicate": `{"redirect_uri":"one","redirect_uri":"two"}`,
		"escaped duplicate": `{"redirect_uri":"one","redirect\u005furi":"two"}`, "trailing JSON": string(valid) + ` {}`,
		"trailing text": string(valid) + ` private`, "bad key": `{"redirect_uri":`, "bad object": `{"redirect_uri":"value"]`,
		"invalid UTF-8": "{\"redirect_uri\":\"\xff\"}", "oversized value": `{"redirect_uri":"` + strings.Repeat("a", 2049) + `"}`,
		"oversized body": string(valid) + strings.Repeat(" ", 16<<10), "bad JSON key": `{"redirect_uri":"one", :"two"}`,
	} {
		t.Run(name, func(t *testing.T) {
			assertCode(t, f.discoverRequest(f.providerRequest(strings.NewReader(body))), http.StatusBadRequest, "invalid_request")
		})
	}
	for _, contentType := range []string{"", "text/plain", "application/json;broken"} {
		t.Run("content type "+contentType, func(t *testing.T) {
			r := f.providerRequest(bytes.NewReader(valid))
			r.Header.Set("Content-Type", contentType)
			assertCode(t, f.discoverRequest(r), http.StatusBadRequest, "invalid_request")
		})
	}
	const prefix = "https://product.example/"
	callback := prefix + strings.Repeat("a", 2048-len(prefix))
	updateDiscoveryApp(t, f, func(input *apps.AppInput) { input.RedirectURIs = []string{callback} })
	escaped := `{"redirect_uri":"` + strings.ReplaceAll(callback, "a", `\u0061`) + `"}`
	r := f.providerRequest(strings.NewReader(escaped))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	assertDiscoveryProviders(t, f.discoverRequest(r), "google", "github")
}

type discoveryReadHook struct {
	io.Reader
	hook func()
	err  error
}

func (r *discoveryReadHook) Read(p []byte) (int, error) {
	if r.hook != nil {
		r.hook()
		r.hook = nil
	}
	if r.err != nil {
		return 0, r.err
	}
	return r.Reader.Read(p)
}

func TestProviderDiscoveryCancellationAndClosure(t *testing.T) {
	for _, name := range []string{"canceled request", "closed store", "closed handler", "cancel while reading", "close while reading", "cancel during read failure", "close during read failure", "body read failure"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			body, err := json.Marshal(map[string]string{"redirect_uri": f.app.App.RedirectURIs[0]})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader := &discoveryReadHook{Reader: bytes.NewReader(body)}
			status, code := http.StatusInternalServerError, "internal_error"
			switch name {
			case "canceled request":
				cancel()
			case "closed store":
				if err := f.store.Close(); err != nil {
					t.Fatal(err)
				}
			case "closed handler":
				if err := f.h.Close(); err != nil {
					t.Fatal(err)
				}
				status, code = http.StatusServiceUnavailable, "service_unavailable"
			case "cancel while reading", "cancel during read failure":
				reader.hook = cancel
				if name == "cancel during read failure" {
					reader.err = context.Canceled
				}
			case "close while reading", "close during read failure":
				reader.hook = func() {
					if err := f.h.Close(); err != nil {
						t.Error(err)
					}
				}
				status, code = http.StatusServiceUnavailable, "service_unavailable"
				if name == "close during read failure" {
					reader.err = context.Canceled
				}
			case "body read failure":
				reader.err = errors.New("private body transport failure")
				status, code = http.StatusBadRequest, "invalid_request"
			}
			assertCode(t, f.discoverRequest(f.providerRequest(reader).WithContext(ctx)), status, code)
		})
	}
}

func TestProviderDiscoveryPreservesReadError(t *testing.T) {
	cause := errors.New("private transport failure")
	_, err := providerRedirectURI(&discoveryReadHook{err: cause})
	if !errors.Is(err, cause) {
		t.Fatalf("provider request lost its read error: %T", err)
	}
	var logs bytes.Buffer
	f := newFixture(t, func(config *Config) { config.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := f.providerRequest(&discoveryReadHook{hook: cancel, err: cause}).WithContext(ctx)
	assertCode(t, f.discoverRequest(r), http.StatusInternalServerError, "internal_error")
	if strings.Contains(logs.String(), cause.Error()) || !strings.Contains(logs.String(), `"error_cause":"canceled"`) {
		t.Fatal("provider request failed to sanitize and classify a canceled read")
	}
}

func TestProviderDiscoveryLoggingIsSanitized(t *testing.T) {
	var logs bytes.Buffer
	f := newFixture(t, func(config *Config) { config.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	assertDiscoveryProviders(t, f.discover(f.app.App.RedirectURIs[0]), "google", "github")
	requestID := ""
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["phase"] == "provider_discovery" && entry["app_id"] == f.app.App.ID {
			requestID, _ = entry["request_id"].(string)
		}
	}
	if requestID == "" {
		t.Fatalf("discovery omitted safe App and request correlation: %s", &logs)
	}
	r := f.providerRequest(strings.NewReader(`{"redirect_uri":"https://private.example/PRIVATE_CALLBACK?secret=PRIVATE_BODY"}`))
	r.URL.RawQuery = "private=PRIVATE_QUERY"
	assertCode(t, f.discoverRequest(r), http.StatusBadRequest, "invalid_request")
	r = f.providerRequest(strings.NewReader(`{}`))
	r.SetBasicAuth("PRIVATE_ID", "PRIVATE_SECRET")
	assertCode(t, f.discoverRequest(r), http.StatusUnauthorized, "invalid_app")
	for _, private := range []string{f.app.Secret, f.app.App.RedirectURIs[0], "PRIVATE_CALLBACK", "PRIVATE_BODY", "PRIVATE_QUERY", "PRIVATE_ID", "PRIVATE_SECRET"} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("discovery logged private request data")
		}
	}
	writer := &discoveryErrorWriter{header: make(http.Header)}
	body, err := json.Marshal(map[string]string{"redirect_uri": f.app.App.RedirectURIs[0]})
	if err != nil {
		t.Fatal(err)
	}
	f.mux.ServeHTTP(writer, f.providerRequest(bytes.NewReader(body)))
	if strings.Contains(logs.String(), "PRIVATE_RESPONSE_ERROR") || !strings.Contains(logs.String(), `"error_category":"response_write_failed"`) {
		t.Fatalf("response error was not sanitized: %s", &logs)
	}
}

type discoveryErrorWriter struct{ header http.Header }

func (w *discoveryErrorWriter) Header() http.Header { return w.header }
func (*discoveryErrorWriter) WriteHeader(int)       {}
func (*discoveryErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("PRIVATE_RESPONSE_ERROR")
}
