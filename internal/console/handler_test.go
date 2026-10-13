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
	"sync"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
)

const testPassword = "correct horse battery staple"

type testConsole struct {
	handler *Handler
	mux     *http.ServeMux
	store   *apps.Store
	now     time.Time
	cookie  *http.Cookie
	csrf    string
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
	cfg := Config{PublicURL: "https://auth.example", Password: testPassword, Now: func() time.Time { return env.now }, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	if change != nil {
		change(&cfg)
	}
	env.handler, err = New(store, []string{"microsoft", "github"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	env.mux = http.NewServeMux()
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

func (e *testConsole) login(t *testing.T) {
	t.Helper()
	response := e.request("POST", "/console/api/login", `{"password":"correct horse battery staple"}`, nil)
	requireStatus(t, response, http.StatusOK)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie: %v", cookies)
	}
	e.cookie = cookies[0]
	e.csrf = decodeResponse[map[string]string](t, response)["csrf_token"]
	if len(e.cookie.Value) != 43 || len(e.csrf) != 43 {
		t.Fatal("session and CSRF tokens must be 256-bit values")
	}
}

const appBody = `{"name":"Product A","enabled":true,"redirect_uris":["https://product.example/callback"],"providers":["microsoft","github"]}`
const createAppBody = `{"name":"Product A","enabled":true,"redirect_uris":["https://product.example/callback"],"providers":["microsoft","github"],"initial_secret":{"name":"Production server","description":"Primary deployment credential"}}`
const secretBody = `{"name":"Backup server","description":"Standby deployment credential"}`

func TestApplicationLifecycleAndCredentialIsolation(t *testing.T) {
	e := newTestConsole(t, nil)
	requireStatus(t, e.request("GET", "/console/api/apps", "", nil), http.StatusUnauthorized)
	e.login(t)
	if e.cookie.Name != "__Host-uniauth_admin" || !e.cookie.Secure || !e.cookie.HttpOnly || e.cookie.Domain != "" || e.cookie.Path != "/" || e.cookie.SameSite != http.SameSiteStrictMode {
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

func TestOriginCSRFAndStrictInput(t *testing.T) {
	e := newTestConsole(t, nil)
	for _, origin := range []string{"", "null", "https://evil.example", "https://auth.example.evil.test", "https://auth.example/"} {
		requireStatus(t, e.request("POST", "/console/api/login", `{"password":"correct horse battery staple"}`, map[string]string{"Origin": origin}), http.StatusForbidden)
	}
	for _, body := range []string{`{`, `{}`, `null`, `[]`, `{"password":"correct horse battery staple","extra":true}`, `{"password":"correct horse battery staple"} {}`} {
		requireStatus(t, e.request("POST", "/console/api/login", body, nil), http.StatusBadRequest)
	}
	requireStatus(t, e.request("POST", "/console/api/login", `{"password":"wrong"}`, nil), http.StatusUnauthorized)
	requireStatus(t, e.request("POST", "/console/api/login", `{"password":"x"}`, map[string]string{"Content-Type": "text/plain"}), http.StatusUnsupportedMediaType)
	requireStatus(t, e.request("POST", "/console/api/login", `{"password":"`+strings.Repeat("x", 1025)+`"}`, nil), http.StatusBadRequest)
	requireStatus(t, e.request("POST", "/console/api/login", `{"password":"`+strings.Repeat("x", 65536)+`"}`, nil), http.StatusBadRequest)
	e.login(t)
	for _, path := range []string{"/console/api/apps", "/console/api/logout", "/console/api/apps/missing/secrets"} {
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
	for _, body := range []string{appBody, strings.TrimSuffix(appBody, "}") + `,"initial_secret":null}`, strings.TrimSuffix(appBody, "}") + `,"initial_secret":[]}`} {
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
		if decodeResponse[map[string]string](t, response)["csrf_token"] != initialCSRF || len(response.Result().Cookies()) != 0 {
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
	replacement, err := New(e.store, nil, e.handler.config)
	if err != nil {
		t.Fatal(err)
	}
	e.mux = http.NewServeMux()
	replacement.Register(e.mux)
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), http.StatusUnauthorized)
}

func TestSessionCapacityReauthenticationAndExpiryRecovery(t *testing.T) {
	e := newTestConsole(t, func(cfg *Config) { cfg.MaxSessions = 1; cfg.SessionTTL = time.Minute })
	e.login(t)
	oldCookie := e.cookie
	e.login(t)
	if oldCookie.Value == e.cookie.Value {
		t.Fatal("reauthentication did not rotate the session")
	}
	currentCookie := e.cookie
	e.cookie = oldCookie
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), http.StatusUnauthorized)
	e.cookie = nil
	requireStatus(t, e.request("POST", "/console/api/login", `{"password":"correct horse battery staple"}`, nil), http.StatusServiceUnavailable)
	e.cookie = currentCookie
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), http.StatusOK)
	e.cookie = nil
	e.now = e.now.Add(time.Minute)
	e.login(t)
	if len(e.handler.sessions) != 1 {
		t.Fatalf("expired entries were retained: %d", len(e.handler.sessions))
	}
}

func TestLoginUsesExactConfiguredPassword(t *testing.T) {
	const password = "  $直接-password  "
	e := newTestConsole(t, func(cfg *Config) { cfg.Password = password })
	for _, wrong := range []string{strings.TrimSpace(password), "  $直接-password ", "  $直接-Password  ", password + "\r\n"} {
		body, err := json.Marshal(map[string]string{"password": wrong})
		if err != nil {
			t.Fatal(err)
		}
		response := e.request("POST", "/console/api/login", string(body), nil)
		requireStatus(t, response, http.StatusUnauthorized)
		if len(response.Result().Cookies()) != 0 {
			t.Fatal("incorrect password issued a session")
		}
	}
	body, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		t.Fatal(err)
	}
	response := e.request("POST", "/console/api/login", string(body), nil)
	requireStatus(t, response, http.StatusOK)
	if len(response.Result().Cookies()) != 1 || decodeResponse[map[string]string](t, response)["csrf_token"] == "" {
		t.Fatal("configured password did not issue a session and CSRF token")
	}
}

func TestConfiguredPasswordIsUsableInConsole(t *testing.T) {
	e := newTestConsole(t, nil)
	cases := []struct {
		name     string
		password string
		valid    bool
	}{
		{"one byte", " ", true},
		{"1024 bytes", strings.Repeat("界", 341) + "$", true},
		{"LF", "password\n", false},
		{"CR", "password\r", false},
		{"CRLF", "password\r\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{PublicURL: "https://auth.example", Password: tc.password}
			handler, err := New(e.store, nil, cfg)
			if !tc.valid {
				if err == nil {
					t.Fatal("multiline administrator password accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("usable administrator password rejected: %v", err)
			}
			mux := http.NewServeMux()
			handler.Register(mux)
			body, err := json.Marshal(map[string]string{"password": tc.password})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", cfg.PublicURL+"/console/api/login", bytes.NewReader(body))
			req.Header.Set("Origin", cfg.PublicURL)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			requireStatus(t, response, http.StatusOK)
			if len(response.Result().Cookies()) != 1 {
				t.Fatal("usable administrator password did not issue a session")
			}
		})
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newTestConsole(t, nil)
	for range loginBurst {
		requireStatus(t, e.request("POST", "/console/api/login", `{"password":"wrong"}`, nil), http.StatusUnauthorized)
	}
	requireStatus(t, e.request("POST", "/console/api/login", `{"password":"wrong"}`, nil), http.StatusTooManyRequests)
	e.now = e.now.Add(time.Second)
	requireStatus(t, e.request("POST", "/console/api/login", `{"password":"wrong"}`, nil), http.StatusUnauthorized)
	e.now = e.now.Add(time.Second)
	e.login(t)
}

func TestConcurrentLoginEnforcesSessionCapacity(t *testing.T) {
	e := newTestConsole(t, func(cfg *Config) { cfg.MaxSessions = 1 })
	responses := make(chan *httptest.ResponseRecorder, loginBurst)
	var workers sync.WaitGroup
	for range loginBurst {
		workers.Go(func() {
			responses <- e.request("POST", "/console/api/login", `{"password":"correct horse battery staple"}`, nil)
		})
	}
	workers.Wait()
	close(responses)
	var accepted int
	for response := range responses {
		switch response.Code {
		case http.StatusOK:
			accepted++
		case http.StatusServiceUnavailable:
		default:
			t.Fatalf("unexpected concurrent login status %d: %s", response.Code, response.Body.String())
		}
	}
	if accepted != 1 || len(e.handler.sessions) != 1 {
		t.Fatalf("concurrent logins bypassed capacity: accepted=%d stored=%d", accepted, len(e.handler.sessions))
	}
}

func TestLoopbackCookieAndConfigurationValidation(t *testing.T) {
	e := newTestConsole(t, func(cfg *Config) { cfg.PublicURL = "http://127.0.0.1:8080"; cfg.AllowLoopbackHTTP = true })
	e.login(t)
	if e.cookie.Secure || strings.HasPrefix(e.cookie.Name, "__Host-") || e.cookie.Domain != "" {
		t.Fatalf("invalid loopback cookie: %+v", e.cookie)
	}
	validConfig := Config{PublicURL: "https://auth.example", Password: testPassword}
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"invalid URL", func(cfg *Config) { cfg.PublicURL = "https://auth.example:%xx" }},
		{"missing host", func(cfg *Config) { cfg.PublicURL = "https:///" }},
		{"userinfo", func(cfg *Config) { cfg.PublicURL = "https://admin@auth.example" }},
		{"path", func(cfg *Config) { cfg.PublicURL = "https://auth.example/path" }},
		{"query", func(cfg *Config) { cfg.PublicURL = "https://auth.example?x=y" }},
		{"empty query", func(cfg *Config) { cfg.PublicURL = "https://auth.example?" }},
		{"fragment", func(cfg *Config) { cfg.PublicURL = "https://auth.example#fragment" }},
		{"empty fragment", func(cfg *Config) { cfg.PublicURL = "https://auth.example#" }},
		{"public HTTP", func(cfg *Config) { cfg.PublicURL = "http://auth.example"; cfg.AllowLoopbackHTTP = true }},
		{"implicit loopback", func(cfg *Config) { cfg.PublicURL = "http://localhost:8080" }},
		{"missing password", func(cfg *Config) { cfg.Password = "" }},
		{"large password", func(cfg *Config) { cfg.Password = strings.Repeat("x", 1025) }},
		{"negative lifetime", func(cfg *Config) { cfg.SessionTTL = -time.Second }},
		{"unbounded lifetime", func(cfg *Config) { cfg.SessionTTL = 25 * time.Hour }},
		{"negative capacity", func(cfg *Config) { cfg.MaxSessions = -1 }},
		{"unbounded capacity", func(cfg *Config) { cfg.MaxSessions = 100001 }},
		{"negative login interval", func(cfg *Config) { cfg.LoginInterval = -time.Second }},
		{"excessive login interval", func(cfg *Config) { cfg.LoginInterval = time.Hour }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig
			tt.change(&cfg)
			if _, err := New(e.store, nil, cfg); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
	if _, err := New(nil, nil, validConfig); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := New(e.store, []string{"other"}, validConfig); err == nil {
		t.Fatal("unknown available provider accepted")
	}
	for _, origin := range []string{"http://localhost:8080", "http://[::1]:8080", "https://auth.example/"} {
		cfg := validConfig
		cfg.PublicURL, cfg.AllowLoopbackHTTP = origin, true
		if _, err := New(e.store, nil, cfg); err != nil {
			t.Fatalf("valid origin %s rejected: %v", origin, err)
		}
	}
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
