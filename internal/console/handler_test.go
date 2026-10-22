package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/auth"
	"github.com/codetreker/uniauth/internal/providers"
	"net/url"
)

type testConsole struct {
	handler *Handler
	mux     *http.ServeMux
	store   *apps.Store
	now     time.Time
	cookie  *http.Cookie
	csrf    string
	account apps.Account
	gateway *auth.Handler
}

func newTestConsole(t *testing.T, change func(*Config)) *testConsole {
	t.Helper()
	if err := os.MkdirAll("../../.tmp", 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("../../.tmp", "console-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store, err := apps.Open(context.Background(), apps.Config{Path: filepath.Join(dir, "apps.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	env := &testConsole{store: store, now: time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)}
	cfg := Config{PublicURL: "https://auth.example", Now: func() time.Time { return env.now }, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	if change != nil {
		change(&cfg)
	}
	if err := store.InitializePlatform(context.Background(), "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(cfg.PublicURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = configuredOrigin(parsed)
	if err != nil {
		t.Fatal(err)
	}
	consoleApp, err := store.ProvisionConsole(context.Background(), strings.TrimSuffix(cfg.PublicURL, "/")+"/console/auth/callback", []string{"microsoft", "github"})
	if err != nil {
		t.Fatal(err)
	}
	env.gateway, err = auth.New(context.Background(), auth.Config{PublicURL: cfg.PublicURL, AllowLoopbackHTTP: cfg.AllowLoopbackHTTP, TransactionTTL: 10 * time.Minute, CallbackTimeout: time.Second, CleanupInterval: time.Minute, MaxTransactions: 1000, MaxTransactionBytes: 1 << 20, MaxResults: 1000, MaxResultBytes: 1 << 20, Now: cfg.Now, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, store, map[string]providers.Provider{"github": consoleProvider("github"), "microsoft": consoleProvider("microsoft")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.gateway.Close() })
	cfg.Gateway, cfg.AppID, cfg.AppSecret = env.gateway, consoleApp.App.ID, consoleApp.Secret
	env.handler, err = New(store, []string{"microsoft", "github"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.handler.Close() })
	env.mux = http.NewServeMux()
	env.gateway.Register(env.mux)
	env.handler.Register(env.mux)
	return env
}

func (e *testConsole) request(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, e.handler.origin+path, strings.NewReader(body))
	if e.cookie != nil {
		req.AddCookie(e.cookie)
	}
	if method != "GET" {
		req.Header.Set("Origin", e.handler.origin)
		req.Header.Set("Content-Type", "application/json")
		if e.csrf != "" {
			req.Header.Set("X-CSRF-Token", e.csrf)
		}
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	e.mux.ServeHTTP(response, req)
	return response
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status %d, want %d: %s", response.Code, status, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response missing no-store: %v", response.Header())
	}
	if status >= 400 {
		var envelope struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error.Code == "" || envelope.Error.Message == "" {
			t.Fatalf("missing error envelope: %s", response.Body.String())
		}
	}
}

func decodeResponse[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

type consoleProvider string

func (p consoleProvider) Name() string { return string(p) }
func (p consoleProvider) AuthorizationURL(request providers.AuthorizationRequest) (string, error) {
	return "https://provider.example/?state=" + url.QueryEscape(request.State), nil
}
func (p consoleProvider) Exchange(_ context.Context, request providers.ExchangeRequest) (providers.Identity, error) {
	if request.Code == "failure" {
		return providers.Identity{}, errors.New("private upstream error")
	}
	email, name, verified := "admin@example.com", "Admin", true
	id := request.Code
	if id != "admin" {
		email = "member@example.com"
		name = "Member"
	}
	var verification *bool = &verified
	if p == "microsoft" {
		verification = nil
	}
	return providers.Identity{Provider: string(p), ID: string(p) + ":" + id, Email: &email, EmailVerified: verification, Name: &name, Origin: map[string]json.RawMessage{"profile": json.RawMessage(`{"private":"upstream"}`)}}, nil
}

type consoleFlow struct {
	pending      *http.Cookie
	gateway      *http.Cookie
	gatewayState string
	state        string
}

func (e *testConsole) startFlow(t *testing.T, provider, path string) consoleFlow {
	t.Helper()
	response := e.request("POST", path, `{"provider":"`+provider+`"}`, nil)
	requireStatus(t, response, 200)
	return e.authorizeFlow(t, response)
}
func (e *testConsole) authorizeFlow(t *testing.T, response *httptest.ResponseRecorder) consoleFlow {
	t.Helper()
	authorization := decodeResponse[map[string]string](t, response)["authorization_url"]
	pending := response.Result().Cookies()
	if len(pending) != 1 || pending[0].SameSite != http.SameSiteLaxMode || !pending[0].HttpOnly {
		t.Fatalf("invalid pending cookies %+v", pending)
	}
	parsed, err := url.Parse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", authorization, nil)
	reply := httptest.NewRecorder()
	e.mux.ServeHTTP(reply, req)
	requireStatus(t, reply, 302)
	redirect, err := url.Parse(reply.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cookies := reply.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing gateway cookie")
	}
	return consoleFlow{pending: pending[0], gateway: cookies[0], gatewayState: redirect.Query().Get("state"), state: parsed.Query().Get("state")}
}
func (e *testConsole) gatewayCallback(t *testing.T, flow consoleFlow, provider, code string) string {
	t.Helper()
	req := httptest.NewRequest("GET", e.handler.origin+"/auth/callback/"+provider+"?"+url.Values{"state": {flow.gatewayState}, "code": {code}}.Encode(), nil)
	req.AddCookie(flow.gateway)
	response := httptest.NewRecorder()
	e.mux.ServeHTTP(response, req)
	requireStatus(t, response, 302)
	return response.Header().Get("Location")
}
func (e *testConsole) consoleCallback(t *testing.T, location string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", location, nil)
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}
	response := httptest.NewRecorder()
	e.mux.ServeHTTP(response, req)
	requireStatus(t, response, 303)
	return response
}
func (e *testConsole) acceptLogin(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Location") != "/console/" {
		t.Fatalf("login failed: %s", response.Header().Get("Location"))
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == e.handler.cookieName {
			e.cookie = cookie
		}
	}
	session := decodeResponse[struct {
		CSRFToken string       `json:"csrf_token"`
		User      apps.Account `json:"user"`
	}](t, e.request("GET", "/console/api/session", "", nil))
	e.csrf, e.account = session.CSRFToken, session.User
	if e.cookie == nil || len(e.cookie.Value) != 43 || len(e.csrf) != 43 {
		t.Fatal("missing session tokens")
	}
}
func (e *testConsole) login(t *testing.T) {
	t.Helper()
	flow := e.startFlow(t, "github", "/console/api/login")
	location := e.gatewayCallback(t, flow, "github", "admin")
	e.acceptLogin(t, e.consoleCallback(t, location, flow.pending, e.cookie))
}

const appBody = `{"name":"Product A","enabled":true,"redirect_uris":["https://product.example/callback"],"providers":["microsoft","github"]}`
const createAppBody = `{"name":"Product A","enabled":true,"redirect_uris":["https://product.example/callback"],"providers":["microsoft","github"],"initial_secret":{"name":"Production server","description":"Primary deployment credential"}}`
const secretBody = `{"name":"Backup server","description":"Standby deployment credential"}`

func TestApplicationLifecycleAndCredentialIsolation(t *testing.T) {
	e := newTestConsole(t, nil)
	requireStatus(t, e.request("GET", "/console/api/apps", "", nil), http.StatusUnauthorized)
	e.login(t)
	if e.cookie.Name != "__Host-uniauth_console" || !e.cookie.Secure || !e.cookie.HttpOnly || e.cookie.Domain != "" || e.cookie.Path != "/" || e.cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("insecure session cookie: %+v", e.cookie)
	}
	response := e.request("POST", "/console/api/apps", createAppBody, nil)
	requireStatus(t, response, http.StatusCreated)
	created := decodeResponse[apps.CreatedApp](t, response)
	if created.App.ID == "" || created.Secret == "" || created.SecretID == "" {
		t.Fatal("creation did not return the initial credential")
	}
	if _, err := e.store.Authenticate(context.Background(), created.App.ID, created.Secret); err != nil {
		t.Fatal(err)
	}
	appPath := "/console/api/apps/" + created.App.ID
	for _, path := range []string{"/console/api/apps", appPath, appPath + "/secrets"} {
		response := e.request("GET", path, "", nil)
		requireStatus(t, response, http.StatusOK)
		if strings.Contains(response.Body.String(), created.Secret) || strings.Contains(response.Body.String(), `"secret":`) {
			t.Fatalf("credential was disclosed again: %s", response.Body.String())
		}
	}
	list := decodeResponse[struct{ Apps []apps.App }](t, e.request("GET", "/console/api/apps", "", nil))
	if len(list.Apps) != 1 || list.Apps[0].ID != created.App.ID {
		t.Fatalf("app list mismatch: %+v", list)
	}
	response = e.request("POST", appPath+"/secrets", secretBody, nil)
	requireStatus(t, response, http.StatusCreated)
	issued := decodeResponse[apps.IssuedSecret](t, response)
	if issued.Secret == created.Secret || issued.ID == created.SecretID {
		t.Fatal("rotation did not issue an independent credential")
	}
	if issued.Name != "Backup server" || issued.Description != "Standby deployment credential" {
		t.Fatalf("issued credential metadata mismatch: %+v", issued)
	}
	initial := decodeResponse[struct{ Secrets []apps.Secret }](t, e.request("GET", appPath+"/secrets", "", nil))
	if len(initial.Secrets) != 2 {
		t.Fatalf("credential list mismatch: %+v", initial)
	}
	for _, secret := range initial.Secrets {
		if secret.ID == created.SecretID && (secret.Name != "Production server" || secret.Description != "Primary deployment credential") {
			t.Fatalf("initial credential metadata missing: %+v", secret)
		}
	}
	response = e.request("PUT", appPath+"/secrets/"+issued.ID, `{"name":"Standby east","description":"  Rotation memo\n第二行  "}`, nil)
	requireStatus(t, response, http.StatusOK)
	edited := decodeResponse[apps.Secret](t, response)
	if edited.ID != issued.ID || edited.Name != "Standby east" || edited.Description != "  Rotation memo\n第二行  " || !edited.CreatedAt.Equal(issued.CreatedAt) || edited.RevokedAt != nil {
		t.Fatalf("metadata edit changed credential lifecycle: %+v", edited)
	}
	if strings.Contains(response.Body.String(), issued.Secret) || strings.Contains(response.Body.String(), `"secret":`) || strings.Contains(response.Body.String(), "digest") {
		t.Fatalf("metadata edit disclosed credential material: %s", response.Body.String())
	}
	if _, err := e.store.Authenticate(context.Background(), created.App.ID, issued.Secret); err != nil {
		t.Fatalf("metadata edit changed authentication: %v", err)
	}
	response = e.request("DELETE", appPath+"/secrets/"+created.SecretID, "", nil)
	requireStatus(t, response, http.StatusNoContent)
	response = e.request("PUT", appPath+"/secrets/"+created.SecretID, `{"name":"Retired primary"}`, nil)
	requireStatus(t, response, http.StatusOK)
	retired := decodeResponse[apps.Secret](t, response)
	if retired.Name != "Retired primary" || retired.Description != "" || retired.RevokedAt == nil {
		t.Fatalf("revoked credential metadata edit mismatch: %+v", retired)
	}
	if _, err := e.store.Authenticate(context.Background(), created.App.ID, created.Secret); !errors.Is(err, apps.ErrUnauthorized) {
		t.Fatalf("revoked secret authenticated: %v", err)
	}
	if _, err := e.store.Authenticate(context.Background(), created.App.ID, issued.Secret); err != nil {
		t.Fatalf("remaining credential stopped working: %v", err)
	}
	metadata := decodeResponse[struct{ Secrets []apps.Secret }](t, e.request("GET", appPath+"/secrets", "", nil))
	if len(metadata.Secrets) != 2 {
		t.Fatalf("secret list mismatch: %+v", metadata)
	}
	var revoked bool
	for _, secret := range metadata.Secrets {
		revoked = revoked || (secret.ID == created.SecretID && secret.RevokedAt != nil)
		if secret.ID == issued.ID && (secret.Name != edited.Name || secret.Description != edited.Description) {
			t.Fatalf("edited metadata was not persisted: %+v", secret)
		}
	}
	if !revoked {
		t.Fatal("revocation metadata missing")
	}
	response = e.request("PUT", appPath, strings.Replace(appBody, `"enabled":true`, `"enabled":false`, 1), nil)
	requireStatus(t, response, http.StatusOK)
	if decodeResponse[apps.App](t, response).Enabled {
		t.Fatal("app disable was not persisted")
	}
	if _, err := e.store.Authenticate(context.Background(), created.App.ID, issued.Secret); !errors.Is(err, apps.ErrUnauthorized) {
		t.Fatalf("disabled app authenticated: %v", err)
	}
	response = e.request("GET", "/console/api/providers", "", nil)
	requireStatus(t, response, http.StatusOK)
	if response.Body.String() != "{\"providers\":[{\"id\":\"microsoft\",\"available\":true},{\"id\":\"google\",\"available\":false},{\"id\":\"github\",\"available\":true}]}\n" {
		t.Fatalf("provider availability mismatch: %s", response.Body.String())
	}
	adminCookie := e.cookie
	e.cookie = nil
	req := httptest.NewRequest("GET", "https://auth.example/console/api/apps", nil)
	req.SetBasicAuth(created.App.ID, issued.Secret)
	response = httptest.NewRecorder()
	e.mux.ServeHTTP(response, req)
	requireStatus(t, response, http.StatusUnauthorized)
	e.cookie = adminCookie
	requireStatus(t, e.request("DELETE", appPath, "", nil), http.StatusNoContent)
	for _, path := range []string{appPath, appPath + "/secrets"} {
		requireStatus(t, e.request("GET", path, "", nil), http.StatusNotFound)
	}
	requireStatus(t, e.request("DELETE", appPath, "", nil), http.StatusNotFound)
}

func TestSecretResetEndpoint(t *testing.T) {
	e := newTestConsole(t, nil)
	e.login(t)
	response := e.request("POST", "/console/api/apps", createAppBody, nil)
	requireStatus(t, response, http.StatusCreated)
	created := decodeResponse[apps.CreatedApp](t, response)
	response = e.request("POST", "/console/api/apps", createAppBody, nil)
	requireStatus(t, response, http.StatusCreated)
	otherApp := decodeResponse[apps.CreatedApp](t, response)
	appPath := "/console/api/apps/" + created.App.ID
	response = e.request("POST", appPath+"/secrets", secretBody, nil)
	requireStatus(t, response, http.StatusCreated)
	other := decodeResponse[apps.IssuedSecret](t, response)
	response = e.request("PUT", appPath+"/secrets/"+created.SecretID, `{"name":"Updated primary","description":"Current owner memo"}`, nil)
	requireStatus(t, response, http.StatusOK)
	updated := decodeResponse[apps.Secret](t, response)
	resetPath := appPath + "/secrets/" + created.SecretID + "/reset"

	adminCookie := e.cookie
	e.cookie = nil
	requireStatus(t, e.request("POST", resetPath, "", nil), http.StatusUnauthorized)
	e.cookie = adminCookie
	for _, headers := range []map[string]string{
		{"Origin": "https://evil.example"},
		{"X-CSRF-Token": ""},
		{"X-CSRF-Token": "wrong"},
	} {
		requireStatus(t, e.request("POST", resetPath, "", headers), http.StatusForbidden)
	}
	requireStatus(t, e.request("GET", resetPath, "", nil), http.StatusMethodNotAllowed)
	requireStatus(t, e.request("POST", appPath+"/secrets/"+created.SecretID, "", nil), http.StatusMethodNotAllowed)
	for _, path := range []string{
		"/console/api/apps/" + otherApp.App.ID + "/secrets/" + created.SecretID + "/reset",
		appPath + "/secrets/missing/reset",
		"/console/api/apps/missing/secrets/" + created.SecretID + "/reset",
	} {
		requireStatus(t, e.request("POST", path, "", nil), http.StatusNotFound)
	}
	if _, err := e.store.Authenticate(t.Context(), created.App.ID, created.Secret); err != nil {
		t.Fatalf("rejected reset changed the credential: %v", err)
	}

	response = e.request("POST", resetPath, "", nil)
	requireStatus(t, response, http.StatusCreated)
	replacement := decodeResponse[apps.IssuedSecret](t, response)
	if replacement.ID == "" || replacement.Secret == "" || replacement.ID == created.SecretID || replacement.Secret == created.Secret || replacement.CreatedAt.IsZero() || replacement.Name != updated.Name || replacement.Description != updated.Description {
		t.Fatalf("reset did not issue a fresh credential: %+v", replacement)
	}
	if _, err := e.store.Authenticate(t.Context(), created.App.ID, created.Secret); !errors.Is(err, apps.ErrUnauthorized) {
		t.Fatalf("reset credential authenticated: %v", err)
	}
	for _, credential := range []string{replacement.Secret, other.Secret} {
		if _, err := e.store.Authenticate(t.Context(), created.App.ID, credential); err != nil {
			t.Fatalf("active credential stopped working: %v", err)
		}
	}
	if _, err := e.store.Authenticate(t.Context(), otherApp.App.ID, otherApp.Secret); err != nil {
		t.Fatalf("another application's credential stopped working: %v", err)
	}
	metadataResponse := e.request("GET", appPath+"/secrets", "", nil)
	requireStatus(t, metadataResponse, http.StatusOK)
	metadata := decodeResponse[struct{ Secrets []apps.Secret }](t, metadataResponse)
	if len(metadata.Secrets) != 3 || strings.Contains(metadataResponse.Body.String(), replacement.Secret) {
		t.Fatalf("secret metadata leaked or missing: %s", metadataResponse.Body.String())
	}
	for _, secret := range metadata.Secrets {
		if secret.ID == created.SecretID && (secret.Name != updated.Name || secret.Description != updated.Description || secret.RevokedAt == nil) {
			t.Fatalf("reset target metadata changed: %+v", secret)
		}
		if secret.ID == replacement.ID && (secret.Name != updated.Name || secret.Description != updated.Description || secret.RevokedAt != nil) {
			t.Fatalf("replacement metadata mismatch: %+v", secret)
		}
		if secret.ID == other.ID && (secret.Name != other.Name || secret.Description != other.Description || secret.RevokedAt != nil) {
			t.Fatalf("other credential changed: %+v", secret)
		}
	}
	response = e.request("POST", resetPath, "", nil)
	requireStatus(t, response, http.StatusConflict)
	if got := decodeResponse[struct{ Error struct{ Code string } }](t, response).Error.Code; got != "already_revoked" {
		t.Fatalf("unexpected reset conflict code %q", got)
	}
	metadataResponse = e.request("GET", appPath+"/secrets", "", nil)
	metadata = decodeResponse[struct{ Secrets []apps.Secret }](t, metadataResponse)
	if len(metadata.Secrets) != 3 {
		t.Fatalf("conflicting reset issued another credential: %+v", metadata.Secrets)
	}
}

func TestOriginCSRFAndStrictInput(t *testing.T) {
	e := newTestConsole(t, nil)
	for _, origin := range []string{"", "null", "https://evil.example", "https://auth.example.evil.test", "https://auth.example/"} {
		requireStatus(t, e.request("POST", "/console/api/login", `{"provider":"github"}`, map[string]string{"Origin": origin}), http.StatusForbidden)
	}
	for _, body := range []string{`{`, `{}`, `null`, `[]`, `{"provider":"github","extra":true}`, `{"provider":"github"} {}`} {
		requireStatus(t, e.request("POST", "/console/api/login", body, nil), http.StatusBadRequest)
	}
	requireStatus(t, e.request("POST", "/console/api/login", `{"provider":"unavailable"}`, nil), http.StatusBadRequest)
	requireStatus(t, e.request("POST", "/console/api/login", `{"provider":"github"}`, map[string]string{"Content-Type": "text/plain"}), http.StatusUnsupportedMediaType)
	requireStatus(t, e.request("POST", "/console/api/login", `{"provider":"`+strings.Repeat("x", 1025)+`"}`, nil), http.StatusBadRequest)
	requireStatus(t, e.request("POST", "/console/api/login", `{"provider":"`+strings.Repeat("x", 65536)+`"}`, nil), http.StatusBadRequest)
	e.login(t)
	for _, path := range []string{"/console/api/apps", "/console/api/logout", "/console/api/apps/missing/secrets", "/console/api/apps/missing/secrets/missing/reset"} {
		for _, token := range []string{"", "wrong"} {
			requireStatus(t, e.request("POST", path, appBody, map[string]string{"X-CSRF-Token": token}), http.StatusForbidden)
		}
		requireStatus(t, e.request("POST", path, appBody, map[string]string{"Origin": "https://evil.example"}), http.StatusForbidden)
	}
	requireStatus(t, e.request("POST", "/console/api/apps", `{}`, nil), http.StatusBadRequest)
	requireStatus(t, e.request("POST", "/console/api/apps", appBody, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}), http.StatusUnsupportedMediaType)
	for _, path := range []string{"/console/api/apps/missing", "/console/api/apps/missing/secrets"} {
		requireStatus(t, e.request("GET", path, "", nil), http.StatusNotFound)
	}
	requireStatus(t, e.request("PUT", "/console/api/apps/missing", appBody, nil), http.StatusNotFound)
	requireStatus(t, e.request("DELETE", "/console/api/apps/missing/secrets/missing", "", nil), http.StatusNotFound)
	requireStatus(t, e.request("POST", "/console/api/apps/missing/secrets", secretBody, nil), http.StatusNotFound)
	requireStatus(t, e.request("PATCH", "/console/api/apps", "", nil), http.StatusMethodNotAllowed)
	requireStatus(t, e.request("GET", "/console/api/unknown", "", nil), http.StatusNotFound)
	for _, header := range []string{"Origin", "X-CSRF-Token"} {
		req := httptest.NewRequest("POST", "https://auth.example/console/api/apps", strings.NewReader(appBody))
		req.AddCookie(e.cookie)
		req.Header.Set("Origin", e.handler.origin)
		req.Header.Set("X-CSRF-Token", e.csrf)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add(header, req.Header.Get(header))
		response := httptest.NewRecorder()
		e.mux.ServeHTTP(response, req)
		requireStatus(t, response, http.StatusForbidden)
	}
}

func TestCreateAppWithoutInitialSecret(t *testing.T) {
	e := newTestConsole(t, nil)
	e.login(t)
	body := strings.Replace(appBody, "https://product.example/callback", "http://product.example:8080/callback?return=%2Fhome", 1)
	response := e.request("POST", "/console/api/apps", body, nil)
	requireStatus(t, response, http.StatusCreated)
	fields := decodeResponse[map[string]json.RawMessage](t, response)
	if len(fields) != 1 || fields["app"] == nil {
		t.Fatalf("credentialless creation disclosed unexpected fields: %s", response.Body)
	}
	created := decodeResponse[apps.CreatedApp](t, response)
	appPath := "/console/api/apps/" + created.App.ID
	response = e.request("GET", appPath+"/secrets", "", nil)
	requireStatus(t, response, http.StatusOK)
	if response.Body.String() != "{\"secrets\":[]}\n" {
		t.Fatalf("credentialless application has credentials: %s", response.Body)
	}
	if _, err := e.store.Authenticate(t.Context(), created.App.ID, ""); !errors.Is(err, apps.ErrUnauthorized) {
		t.Fatalf("credentialless authentication = %v", err)
	}
	response = e.request("POST", appPath+"/secrets", secretBody, nil)
	requireStatus(t, response, http.StatusCreated)
	issued := decodeResponse[apps.IssuedSecret](t, response)
	if _, err := e.store.Authenticate(t.Context(), created.App.ID, issued.Secret); err != nil {
		t.Fatalf("later credential authentication = %v", err)
	}
	response = e.request("PUT", appPath, body, nil)
	requireStatus(t, response, http.StatusOK)
	updated := decodeResponse[apps.App](t, response)
	if len(updated.RedirectURIs) != 1 || updated.RedirectURIs[0] != "http://product.example:8080/callback?return=%2Fhome" {
		t.Fatalf("HTTP redirect changed: %+v", updated.RedirectURIs)
	}
}

func TestCredentialMetadataValidationAndOwnership(t *testing.T) {
	e := newTestConsole(t, nil)
	e.login(t)
	response := e.request("POST", "/console/api/apps", createAppBody, nil)
	requireStatus(t, response, http.StatusCreated)
	created := decodeResponse[apps.CreatedApp](t, response)
	appPath := "/console/api/apps/" + created.App.ID
	secretPath := appPath + "/secrets/" + created.SecretID
	invalid := []string{
		``, `{`, `{}`, `null`, `[]`, `{"name":null}`, `{"name":5}`, `{"name":""}`,
		`{"name":" Leading"}`, `{"name":"Trailing "}`, `{"name":"\nMultiline"}`, `{"name":"With\rreturn"}`,
		`{"name":"Allowed","description":null}`, `{"name":"Allowed","description":true}`,
		`{"name":"Allowed","description":3}`, `{"name":"Allowed","description":[]}`,
		`{"name":"Allowed","description":{}}`, `{"name":"Allowed","extra":true}`, `{"name":"Allowed"} {}`,
	}
	for _, value := range []apps.SecretInput{
		{Name: strings.Repeat("x", 129)},
		{Name: strings.Repeat("界", 43)},
		{Name: "\u2003Leading"},
		{Name: "Trailing\u2003"},
		{Name: "Allowed", Description: strings.Repeat("界", 1366)},
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		invalid = append(invalid, string(encoded))
	}
	for index, body := range invalid {
		t.Run(fmt.Sprintf("invalid-%d", index), func(t *testing.T) {
			requireStatus(t, e.request("POST", appPath+"/secrets", body, nil), http.StatusBadRequest)
			requireStatus(t, e.request("PUT", secretPath, body, nil), http.StatusBadRequest)
			if body != "" && body != "{" && body != "null" && body != "[]" && body != `{"name":"Allowed"} {}` {
				createBody := strings.TrimSuffix(appBody, "}") + `,"initial_secret":` + body + `}`
				requireStatus(t, e.request("POST", "/console/api/apps", createBody, nil), http.StatusBadRequest)
			}
		})
	}
	for _, body := range []string{strings.TrimSuffix(appBody, "}") + `,"initial_secret":null}`, strings.TrimSuffix(appBody, "}") + `,"initial_secret":[]}`} {
		requireStatus(t, e.request("POST", "/console/api/apps", body, nil), http.StatusBadRequest)
	}
	requireStatus(t, e.request("PUT", appPath, createAppBody, nil), http.StatusBadRequest)
	for _, request := range []struct{ method, path string }{{"POST", appPath + "/secrets"}, {"PUT", secretPath}} {
		requireStatus(t, e.request(request.method, request.path, secretBody, map[string]string{"Content-Type": "text/plain"}), http.StatusUnsupportedMediaType)
		requireStatus(t, e.request(request.method, request.path, secretBody, map[string]string{"Origin": "https://evil.example"}), http.StatusForbidden)
		requireStatus(t, e.request(request.method, request.path, secretBody, map[string]string{"X-CSRF-Token": "wrong"}), http.StatusForbidden)
		cookie := e.cookie
		e.cookie = nil
		requireStatus(t, e.request(request.method, request.path, secretBody, nil), http.StatusUnauthorized)
		e.cookie = cookie
	}
	for _, metadata := range []apps.SecretInput{
		{Name: "x"},
		{Name: strings.Repeat("界", 42) + "ab", Description: strings.Repeat("界", 1365) + "$"},
		{Name: "Multiline memo", Description: " \n\r\t Unicode说明 $ "},
	} {
		body, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		response := e.request("PUT", secretPath, string(body), nil)
		requireStatus(t, response, http.StatusOK)
		updated := decodeResponse[apps.Secret](t, response)
		if updated.Name != metadata.Name || updated.Description != metadata.Description {
			t.Fatalf("valid metadata was altered: %+v want %+v", updated, metadata)
		}
	}
	otherResponse := e.request("POST", "/console/api/apps", createAppBody, nil)
	requireStatus(t, otherResponse, http.StatusCreated)
	other := decodeResponse[apps.CreatedApp](t, otherResponse)
	for _, path := range []string{
		"/console/api/apps/" + other.App.ID + "/secrets/" + created.SecretID,
		appPath + "/secrets/missing",
		"/console/api/apps/missing/secrets/" + created.SecretID,
	} {
		requireStatus(t, e.request("PUT", path, secretBody, nil), http.StatusNotFound)
	}
	if _, err := e.store.Authenticate(context.Background(), created.App.ID, created.Secret); err != nil {
		t.Fatalf("invalid edits changed authentication: %v", err)
	}
	metadata := decodeResponse[struct{ Secrets []apps.Secret }](t, e.request("GET", appPath+"/secrets", "", nil))
	if len(metadata.Secrets) != 1 || metadata.Secrets[0].Name != "Multiline memo" || metadata.Secrets[0].Description != " \n\r\t Unicode说明 $ " {
		t.Fatalf("invalid or wrong-owner requests mutated metadata: %+v", metadata)
	}
}

func TestSessionsAcrossTabsExpiryLogoutAndRestart(t *testing.T) {
	e := newTestConsole(t, func(cfg *Config) { cfg.SessionTTL = time.Minute })
	e.login(t)
	initialCookie, initialCSRF := e.cookie, e.csrf
	for range 3 {
		response := e.request("GET", "/console/api/session", "", nil)
		requireStatus(t, response, http.StatusOK)
		if decodeResponse[struct {
			CSRFToken string `json:"csrf_token"`
		}](t, response).CSRFToken != initialCSRF || len(response.Result().Cookies()) != 0 {
			t.Fatal("opening another tab changed the administrator session")
		}
	}
	e.now = e.now.Add(time.Minute - time.Nanosecond)
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), http.StatusOK)
	e.now = e.now.Add(time.Nanosecond)
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), http.StatusUnauthorized)
	e.login(t)
	newCookie, newCSRF := e.cookie, e.csrf
	e.cookie, e.csrf = initialCookie, initialCSRF
	requireStatus(t, e.request("GET", "/console/api/apps", "", nil), http.StatusUnauthorized)
	e.cookie, e.csrf = newCookie, newCSRF
	response := e.request("POST", "/console/api/logout", "", nil)
	requireStatus(t, response, http.StatusNoContent)
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Value != "" {
		t.Fatalf("logout did not clear cookie: %v", cookies)
	}
	requireStatus(t, e.request("GET", "/console/api/apps", "", nil), http.StatusUnauthorized)
	e.now = e.now.Add(time.Second)
	e.login(t)
	replacement, err := New(e.store, []string{"github"}, e.handler.config)
	if err != nil {
		t.Fatal(err)
	}
	e.mux = http.NewServeMux()
	replacement.Register(e.mux)
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), http.StatusUnauthorized)
}

func TestStoreFailureIsSanitized(t *testing.T) {
	e := newTestConsole(t, nil)
	e.login(t)
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, req := range []struct{ method, path, body string }{
		{"GET", "/console/api/apps", ""},
		{"POST", "/console/api/apps", createAppBody},
		{"GET", "/console/api/apps/missing", ""},
		{"PUT", "/console/api/apps/missing", appBody},
		{"DELETE", "/console/api/apps/missing", ""},
		{"GET", "/console/api/apps/missing/secrets", ""},
		{"POST", "/console/api/apps/missing/secrets", secretBody},
		{"PUT", "/console/api/apps/missing/secrets/missing", secretBody},
		{"DELETE", "/console/api/apps/missing/secrets/missing", ""},
		{"POST", "/console/api/apps/missing/secrets/missing/reset", ""},
	} {
		t.Run(fmt.Sprintf("%s %s", req.method, req.path), func(t *testing.T) {
			response := e.request(req.method, req.path, req.body, nil)
			requireStatus(t, response, http.StatusInternalServerError)
			if bytes.Contains(response.Body.Bytes(), []byte("database")) || bytes.Contains(response.Body.Bytes(), []byte("sql")) {
				t.Fatalf("internal database detail disclosed: %s", response.Body.String())
			}
		})
	}
}
