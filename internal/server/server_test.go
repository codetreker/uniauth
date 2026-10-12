package server

import (
	"bytes"
	"context"
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
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/providers"
)

var passwordHash string
var passwordOnce sync.Once

func configForTest(t *testing.T) Config {
	t.Helper()
	passwordOnce.Do(func() {
		var err error
		passwordHash, err = argon2id.CreateHash("test password", &argon2id.Params{Memory: 19456, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
		if err != nil {
			panic(err)
		}
	})
	return Config{PublicURL: "http://localhost:8080", ListenAddr: "127.0.0.1:0", DatabasePath: filepath.Join(t.TempDir(), "apps.db"), AdminPasswordHash: passwordHash, AllowLoopbackHTTP: true}
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
	return providers.Identity{Provider: "github", Subject: "github:123", Sources: map[string]json.RawMessage{"profile": json.RawMessage(`{"id":123,"unknown":null,"enabled":false}`)}}, nil
}

func TestAdministratorProductFlow(t *testing.T) {
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
	res := request("POST", "/console/api/login", `{"password":"test password"}`, "")
	if res.StatusCode != 200 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res = request("GET", "/console/api/providers", "", "")
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Contains(data, []byte(`"id":"github","available":true`)) {
		t.Fatalf("provider status: %s", data)
	}
	res = request("POST", "/console/api/apps", `{"name":"Product","enabled":true,"redirect_uris":["https://product.example/callback?context=x"],"providers":["github"]}`, login.CSRF)
	if res.StatusCode != 201 {
		t.Fatalf("create app: %d", res.StatusCode)
	}
	var created apps.CreatedApp
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
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
	if identity.Subject != "github:123" || string(identity.Sources["profile"]) != `{"id":123,"unknown":null,"enabled":false}` {
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
	service, err := New(context.Background(), configForTest(t), map[string]providers.Provider{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/healthz", 200}, {"/readyz", 200}, {"/", 302}, {"/unknown", 404}, {"/console/api/unknown", 404}} {
		r := httptest.NewRequest("GET", tc.path+"?code=private-code", nil)
		r.Header.Set("Cookie", "private-cookie")
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		if w.Header().Get("X-Request-ID") == "" {
			t.Fatal("missing request ID")
		}
	}
	if strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "unknown?code") {
		t.Fatalf("sensitive logs: %s", logs.String())
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
	invalid.AdminPasswordHash = "invalid"
	if _, err := New(context.Background(), invalid, nil, quietLogger()); err == nil {
		t.Fatal("invalid administrator hash accepted")
	}
	service, err := New(context.Background(), cfg, nil, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.GitHub = Credentials{"client", "secret"}
	service, err = New(context.Background(), cfg, nil, quietLogger())
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

func TestServeGracefulShutdown(t *testing.T) {
	service := newTestService(t, map[string]providers.Provider{})
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
	service := newTestService(t, map[string]providers.Provider{})
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
	for _, path := range []string{"/auth/authorize", "/auth/callback/github", "/auth/result", "/console/api/apps", "/console/index.html"} {
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
