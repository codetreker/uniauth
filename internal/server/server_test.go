package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/providers"
)

func configForTest(t *testing.T) Config {
	t.Helper()
	return Config{PublicURL: "http://localhost:8080", ListenAddr: "127.0.0.1:0", DatabasePath: filepath.Join(t.TempDir(), "apps.db"), InitialAdminEmail: "admin@example.com", AllowLoopbackHTTP: true}
}
func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }
func newTestService(t *testing.T, registry map[string]providers.Provider) *Service {
	t.Helper()
	service, err := New(context.Background(), configForTest(t), registry, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	return service
}

type fakeProvider struct {
	entered chan struct{}
	release chan struct{}
	request providers.AuthorizationRequest
}

func (p *fakeProvider) Name() string { return "github" }
func (p *fakeProvider) AuthorizationURL(request providers.AuthorizationRequest) (string, error) {
	p.request = request
	return "https://provider.example/authorize?state=" + url.QueryEscape(request.State), nil
}
func (p *fakeProvider) Exchange(ctx context.Context, request providers.ExchangeRequest) (providers.Identity, error) {
	if request.Code != "provider-code" || request.CodeVerifier != p.request.CodeVerifier {
		return providers.Identity{}, errors.New("incorrect exchange request")
	}
	if p.entered != nil {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return providers.Identity{}, ctx.Err()
		}
	}
	email, verified := "admin@example.com", true
	return providers.Identity{Provider: "github", ID: "github:123", Email: &email, EmailVerified: &verified, Origin: map[string]json.RawMessage{"profile": json.RawMessage(`{"id":123,"unknown":null,"enabled":false}`)}}, nil
}

func TestConsolePlatformAndProductFlow(t *testing.T) {
	provider := &fakeProvider{}
	service := newTestService(t, map[string]providers.Provider{"github": provider})
	ts := httptest.NewServer(service.Handler())
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, csrf string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "http://localhost:8080")
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := request("POST", "/console/api/login", `{"password":"retired-password"}`, "")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("password login remains available: %d", res.StatusCode)
	}
	res.Body.Close()
	login := performConsoleLogin(t, request, provider, "http://localhost:8080")
	res = request("GET", "/console/api/apps", "", "")
	var initialApps struct {
		Apps []apps.App `json:"apps"`
	}
	if err := json.NewDecoder(res.Body).Decode(&initialApps); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(initialApps.Apps) != 0 {
		t.Fatalf("system application exposed: %+v", initialApps.Apps)
	}
	res = request("GET", "/console/api/providers", "", "")
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Contains(data, []byte(`"id":"github","available":true`)) {
		t.Fatalf("provider status: %s", data)
	}
	res = request("POST", "/console/api/apps", `{"name":"Product","enabled":true,"redirect_uris":["https://product.example/callback?context=x"],"providers":["github"],"initial_secret":{"name":"Server test","description":"Protocol fixture"}}`, login.CSRF)
	if res.StatusCode != 201 {
		t.Fatalf("create app: %d", res.StatusCode)
	}
	var created apps.CreatedApp
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res = request("GET", "/console/api/users", "", "")
	var users apps.UserPage
	if err := json.NewDecoder(res.Body).Decode(&users); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(users.Users) != 1 || users.Users[0].AppCount != 1 || users.Users[0].ID != login.User.ID {
		t.Fatalf("administrator summary: status=%d users=%+v", res.StatusCode, users)
	}
	q := url.Values{"app_id": {created.App.ID}, "provider": {"github"}, "redirect_uri": {"https://product.example/callback?context=x"}, "state": {"product-state"}}
	res = request("GET", "/auth/authorize?"+q.Encode(), "", "")
	if res.StatusCode != 302 {
		t.Fatalf("authorize: %d", res.StatusCode)
	}
	res.Body.Close()
	res = request("GET", "/auth/callback/github?code=provider-code&state="+url.QueryEscape(provider.request.State), "", "")
	if res.StatusCode != 302 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("callback: %d %s", res.StatusCode, body)
	}
	res.Body.Close()
	callback, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if callback.Query().Get("state") != "product-state" || callback.Query().Get("context") != "x" || callback.Query().Get("code") == "" {
		t.Fatalf("callback location: %v", callback)
	}
	claim := func(secret string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+"/auth/result", strings.NewReader(`{"code":"`+callback.Query().Get("code")+`"}`))
		req.SetBasicAuth(created.App.ID, secret)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res = claim("wrong-secret")
	if res.StatusCode != 401 {
		t.Fatalf("wrong credentials: %d", res.StatusCode)
	}
	res.Body.Close()
	res = claim(created.Secret)
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("claim: %d %s", res.StatusCode, body)
	}
	var identity providers.Identity
	if err := json.NewDecoder(res.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if identity.ID != "github:123" || string(identity.Origin["profile"]) != `{"id":123,"unknown":null,"enabled":false}` {
		t.Fatalf("identity lost: %+v", identity)
	}
	res = claim(created.Secret)
	if res.StatusCode != 410 {
		t.Fatalf("duplicate claim: %d", res.StatusCode)
	}
	res.Body.Close()
	res = request("POST", "/console/api/logout", "", login.CSRF)
	if res.StatusCode != 204 {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestHealthRoutingAndSanitizedLogs(t *testing.T) {
	var logs bytes.Buffer
	service, err := New(context.Background(), configForTest(t), map[string]providers.Provider{"github": &fakeProvider{}}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/healthz", 200}, {"/readyz", 200}, {"/", 302}, {"/unknown", 404}, {"/console/api/unknown", 404}, {"/console/auth/callback", 303}} {
		r := httptest.NewRequest("GET", tc.path+"?code=private-code&state=private-state&email=private@example.com", nil)
		r.Header.Set("Cookie", "private-cookie")
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		if w.Header().Get("X-Request-ID") == "" {
			t.Fatal("missing request ID")
		}
		if strings.Contains(w.Header().Get("Location"), "private") {
			t.Fatal("callback redirect disclosed fields")
		}
	}
	if strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "unknown?code") {
		t.Fatalf("sensitive logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"operation":"console_callback"`) {
		t.Fatal("console callback was not classified")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 {
		t.Fatalf("closed readiness: %d", w.Code)
	}
}

func TestStartupCleanupAndLiveRegistry(t *testing.T) {
	cfg := configForTest(t)
	if _, err := New(context.Background(), cfg, nil, nil); err == nil {
		t.Fatal("missing logger accepted")
	}
	invalid := cfg
	invalid.InitialAdminEmail = "Admin <admin@example.com>"
	if _, err := New(context.Background(), invalid, nil, quietLogger()); err == nil {
		t.Fatal("invalid initial administrator email accepted")
	}
	if _, err := New(context.Background(), cfg, nil, quietLogger()); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("zero-provider startup: %v", err)
	}
	if _, err := New(context.Background(), cfg, map[string]providers.Provider{}, quietLogger()); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("empty injected registry: %v", err)
	}
	cfg.GitHub = Credentials{"client", "secret"}
	service, err := New(context.Background(), cfg, nil, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := New(context.Background(), cfg, map[string]providers.Provider{"invalid": &fakeProvider{}}, quietLogger()); err == nil {
		t.Fatal("invalid registry accepted")
	}
	cfg.DatabasePath = filepath.Join(t.TempDir(), "missing", "apps.db")
	if _, err := New(context.Background(), cfg, nil, quietLogger()); err == nil {
		t.Fatal("invalid database path accepted")
	}
}

func TestConsoleInitializationFailureAllowsStartupRecovery(t *testing.T) {
	cfg := configForTest(t)
	cfg.PublicURL = "https://auth.example:65536"
	registry := map[string]providers.Provider{"github": &fakeProvider{}}
	if _, err := New(context.Background(), cfg, registry, quietLogger()); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("console initialization did not reject invalid browser port: %v", err)
	}
	cfg.PublicURL = "http://localhost:8080"
	service, err := New(context.Background(), cfg, registry, quietLogger())
	if err != nil {
		t.Fatalf("startup resources prevented recovery: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConsoleProvisioningAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	cfg := configForTest(t)
	cfg.PublicURL += "/"
	first, err := New(ctx, cfg, map[string]providers.Provider{"github": &fakeProvider{}}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	consoleApp := provisionedConsoleApp(t, first, cfg.PublicURL)
	if !consoleApp.Enabled || len(consoleApp.RedirectURIs) != 1 || consoleApp.RedirectURIs[0] != "http://localhost:8080/console/auth/callback" || len(consoleApp.Providers) != 1 || consoleApp.Providers[0] != "github" {
		t.Fatalf("initial console configuration: %+v", consoleApp)
	}
	email, verified := cfg.InitialAdminEmail, true
	owner, err := first.store.ResolveAccount(ctx, providers.Identity{Provider: "github", ID: "github:owner", Email: &email, EmailVerified: &verified, Origin: map[string]json.RawMessage{}})
	if err != nil || !owner.IsAdmin {
		t.Fatalf("claim administrator: %+v %v", owner, err)
	}
	product, err := first.store.Create(ctx, owner.ID, apps.CreateAppInput{
		AppInput:      apps.AppInput{Name: "Persistent product", Enabled: true, RedirectURIs: []string{"https://product.example/callback"}, Providers: []string{"github"}},
		InitialSecret: apps.SecretInput{Name: "Backend", Description: "Restart fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := consoleCredentialDigest(t, cfg.DatabasePath, consoleApp.ID)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.PublicURL = "https://new-auth.example"
	microsoft, err := providers.NewMicrosoft(ctx, providers.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: cfg.PublicURL + "/auth/callback/microsoft", HTTPClient: &http.Client{Timeout: 20 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(ctx, cfg, map[string]providers.Provider{"microsoft": microsoft, "github": &fakeProvider{}}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	updated := provisionedConsoleApp(t, second, cfg.PublicURL)
	if updated.ID != consoleApp.ID || len(updated.RedirectURIs) != 1 || updated.RedirectURIs[0] != cfg.PublicURL+"/console/auth/callback" || strings.Join(updated.Providers, ",") != "github,microsoft" {
		t.Fatalf("console configuration did not follow deployment: %+v", updated)
	}
	after := consoleCredentialDigest(t, cfg.DatabasePath, updated.ID)
	if bytes.Equal(before, after) {
		t.Fatal("private credential did not rotate on restart")
	}
	if _, err := second.store.GetOwned(ctx, owner.ID, updated.ID); !errors.Is(err, apps.ErrNotFound) {
		t.Fatalf("system app exposed to owner: %v", err)
	}
	if _, err := second.store.Authenticate(ctx, product.App.ID, product.Secret); err != nil {
		t.Fatalf("ordinary product credential changed on restart: %v", err)
	}
	if persisted, err := second.store.Account(ctx, owner.ID); err != nil || !persisted.IsAdmin {
		t.Fatalf("administrator authority changed on restart: %+v %v", persisted, err)
	}
}

func provisionedConsoleApp(t *testing.T, service *Service, origin string) apps.App {
	t.Helper()
	r := httptest.NewRequest("POST", "/console/api/login", strings.NewReader(`{"provider":"github"}`))
	r.Header.Set("Origin", strings.TrimSuffix(origin, "/"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("start console login: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	location, err := url.Parse(response.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.store.Get(context.Background(), location.Query().Get("app_id"))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func consoleCredentialDigest(t *testing.T, path, appID string) []byte {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM app_secrets WHERE app_id = ?", appID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("private credential count: %d %v", count, err)
	}
	var digest []byte
	if err := db.QueryRow("SELECT digest FROM app_secrets WHERE app_id = ?", appID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestServeGracefulShutdown(t *testing.T) {
	service := newTestService(t, map[string]providers.Provider{"github": &fakeProvider{}})
	entered, release := make(chan struct{}), make(chan struct{})
	service.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("completed"))
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx, listener) }()
	requestDone := make(chan error, 1)
	go func() {
		res, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			data, readErr := io.ReadAll(res.Body)
			res.Body.Close()
			if readErr != nil {
				err = readErr
			}
			if string(data) != "completed" {
				err = errors.New("response lost during shutdown")
			}
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not accepted")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("shutdown did not drain: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func TestServeListenerFailureClosesService(t *testing.T) {
	service := newTestService(t, map[string]providers.Provider{"github": &fakeProvider{}})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if err := service.Serve(context.Background(), listener); err == nil {
		t.Fatal("closed listener did not fail")
	}
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 {
		t.Fatalf("SQLite remained open: %d", w.Code)
	}
}

func TestMiddlewarePanicAndMethods(t *testing.T) {
	var logs bytes.Buffer
	handler := accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("private-password") }), slog.New(slog.NewJSONHandler(&logs, nil)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("PRIVATE", "/secret", nil))
	if w.Code != 500 || strings.Contains(logs.String(), "private-password") || strings.Contains(logs.String(), "PRIVATE") {
		t.Fatalf("panic handling: %d %s", w.Code, logs.String())
	}
	for _, path := range []string{"/auth/authorize", "/auth/callback/github", "/auth/result", "/console/auth/callback", "/console/api/apps", "/console/index.html"} {
		if operation(path) == "unknown" {
			t.Fatalf("unclassified %s", path)
		}
	}
}

func TestRegistryCancellationAndMicrosoftConfiguration(t *testing.T) {
	cfg := configForTest(t)
	cfg.Microsoft = Credentials{"client", "secret"}
	registry, err := buildRegistry(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	location, err := registry["microsoft"].AuthorizationURL(providers.AuthorizationRequest{State: "upstream-state", Nonce: "nonce", CodeVerifier: strings.Repeat("v", 43)})
	if err != nil || !strings.HasPrefix(location, "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize") {
		t.Fatalf("Microsoft personal endpoint: %s %v", location, err)
	}
	parsed, _ := url.Parse(location)
	if parsed.Query().Get("redirect_uri") != "http://localhost:8080/auth/callback/microsoft" {
		t.Fatalf("configured origin ignored: %s", location)
	}
	cfg.Google = Credentials{"client", "secret"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildRegistry(ctx, cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("provider startup cause lost: %v", err)
	}
	if _, err := New(context.Background(), configForTest(t), map[string]providers.Provider{"google": &fakeProvider{}}, quietLogger()); err == nil {
		t.Fatal("mismatched registry provider accepted")
	}
}

func TestMiddlewarePreservesStreamingAndSanitizesTransportErrors(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("chunk"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
	}), logger)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if !w.Flushed || w.Body.String() != "chunk" {
		t.Fatal("middleware broke streaming")
	}
	message := []byte("Authorization: secret; callback?code=private")
	if count, err := (transportLog{logger}).Write(message); err != nil || count != len(message) {
		t.Fatalf("transport diagnostic write: %d %v", count, err)
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "private") {
		t.Fatalf("transport diagnostic leaked: %s", logs.String())
	}
}

type consoleSessionResult struct {
	CSRF string       `json:"csrf_token"`
	User apps.Account `json:"user"`
}

func performConsoleLogin(t *testing.T, request func(string, string, string, string) *http.Response, provider *fakeProvider, publicURL string) consoleSessionResult {
	t.Helper()
	res := request("POST", "/console/api/login", `{"provider":"github"}`, "")
	if res.StatusCode != 200 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	var started struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	authorizationURL, err := url.Parse(started.AuthorizationURL)
	if err != nil || authorizationURL.Path != "/auth/authorize" || authorizationURL.Query().Get("redirect_uri") != strings.TrimSuffix(publicURL, "/")+"/console/auth/callback" {
		t.Fatalf("console gateway integration: %s %v", started.AuthorizationURL, err)
	}
	res = request("GET", authorizationURL.RequestURI(), "", "")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("console authorize: %d", res.StatusCode)
	}
	res.Body.Close()
	res = request("GET", "/auth/callback/github?code=provider-code&state="+url.QueryEscape(provider.request.State), "", "")
	if res.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("console upstream callback: %d %s", res.StatusCode, body)
	}
	res.Body.Close()
	consoleCallback, err := url.Parse(res.Header.Get("Location"))
	if err != nil || consoleCallback.Path != "/console/auth/callback" {
		t.Fatalf("console product callback: %s %v", res.Header.Get("Location"), err)
	}
	res = request("GET", consoleCallback.RequestURI(), "", "")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/console/" {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("console callback: %d %s %s", res.StatusCode, res.Header.Get("Location"), body)
	}
	res.Body.Close()
	res = request("GET", "/console/api/session", "", "")
	var login consoleSessionResult
	if err := json.NewDecoder(res.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if login.CSRF == "" || !login.User.IsAdmin {
		t.Fatalf("initial administrator was not claimed: %+v", login)
	}
	return login
}

func TestConsoleDeploymentOriginCallbackContract(t *testing.T) {
	cfg := configForTest(t)
	cfg.PublicURL = "http://LOCALHOST:08080/"
	provider := &fakeProvider{}
	service, err := New(context.Background(), cfg, map[string]providers.Provider{"github": provider}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ts := httptest.NewServer(service.Handler())
	defer ts.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, csrf string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "http://localhost:8080")
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	performConsoleLogin(t, request, provider, cfg.PublicURL)
}
