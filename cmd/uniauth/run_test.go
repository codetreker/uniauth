package main

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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/providers"
	"github.com/codetreker/uniauth/internal/server"
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCommandSelectionAndOutputErrors(t *testing.T) {
	for _, args := range [][]string{{"bad"}, {"serve", "extra"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard, func(string) string { return "" }); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	for _, help := range []string{"help", "-h", "--help"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{help}, &out, io.Discard, func(string) string { return "" }); err != nil || !strings.Contains(out.String(), "UNIAUTH_INITIAL_ADMIN_EMAIL") || !strings.Contains(out.String(), "OAuth provider") {
			t.Fatalf("help: %v %s", err, out.String())
		}
	}
	if err := run(context.Background(), []string{"help"}, failedWriter{}, io.Discard, func(string) string { return "" }); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("help write failure: %v", err)
	}
	if err := Run(context.Background(), []string{"help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestServeConfigurationStartupAndCancellation(t *testing.T) {
	getenv := func(string) string { return "" }
	if err := run(context.Background(), nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("missing config accepted")
	}
	values := map[string]string{"UNIAUTH_PUBLIC_URL": "http://localhost:8080", "UNIAUTH_ALLOW_LOOPBACK_HTTP": "true", "UNIAUTH_INITIAL_ADMIN_EMAIL": "admin@example.com", "UNIAUTH_GITHUB_CLIENT_ID": "client", "UNIAUTH_GITHUB_CLIENT_SECRET": "secret", "UNIAUTH_LISTEN_ADDR": "127.0.0.1:0", "UNIAUTH_DATABASE_PATH": filepath.Join(t.TempDir(), "apps.db")}
	getenv = func(key string) string { return values[key] }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"serve"}, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("canceled startup accepted")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	values["UNIAUTH_LISTEN_ADDR"] = listener.Addr().String()
	if err := run(context.Background(), []string{"serve"}, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("occupied listener accepted")
	}
	values["UNIAUTH_INITIAL_ADMIN_EMAIL"] = "Admin <admin@example.com>"
	if err := run(context.Background(), nil, io.Discard, io.Discard, getenv); err == nil {
		t.Fatal("invalid initial administrator email accepted")
	}
}

type cancelOnStart struct {
	cancel context.CancelFunc
	log    bytes.Buffer
}

func (w *cancelOnStart) Write(body []byte) (int, error) {
	n, err := w.log.Write(body)
	if bytes.Contains(body, []byte("HTTP service started")) {
		w.cancel()
	}
	return n, err
}

func TestServeSuccessAndSignalShutdown(t *testing.T) {
	values := map[string]string{"UNIAUTH_PUBLIC_URL": "http://localhost:8080", "UNIAUTH_ALLOW_LOOPBACK_HTTP": "true", "UNIAUTH_INITIAL_ADMIN_EMAIL": "admin@example.com", "UNIAUTH_GITHUB_CLIENT_ID": "client", "UNIAUTH_GITHUB_CLIENT_SECRET": "secret", "UNIAUTH_LISTEN_ADDR": "127.0.0.1:0", "UNIAUTH_DATABASE_PATH": filepath.Join(t.TempDir(), "apps.db")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &cancelOnStart{cancel: cancel}
	if err := run(ctx, nil, io.Discard, output, func(name string) string { return values[name] }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.log.String(), "HTTP service started") {
		t.Fatal("service never started")
	}
}

func TestRejectedBindPreservesRunningConsole(t *testing.T) {
	httpServer := httptest.NewUnstartedServer(nil)
	t.Cleanup(httpServer.Close)
	origin := "http://" + httpServer.Listener.Addr().String()
	path := filepath.Join(t.TempDir(), "apps.db")
	values := serveEnvironment(path, httpServer.Listener.Addr().String(), origin)
	values["UNIAUTH_MICROSOFT_CLIENT_ID"], values["UNIAUTH_MICROSOFT_CLIENT_SECRET"] = "client", "secret"
	cfg, err := server.LoadConfig(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	service, err := server.New(t.Context(), cfg, map[string]providers.Provider{
		"github": cliOAuthProvider{"github"}, "microsoft": cliOAuthProvider{"microsoft"},
	}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		httpServer.Close()
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	httpServer.Config.Handler = service.Handler()
	httpServer.Start()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, target, body, csrf string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, origin+target, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
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
	pendingLogin := beginCLIConsoleFlow(t, request, "/console/api/login", "github", "")
	before := consoleStartupSnapshot(t, path)
	var diagnostics bytes.Buffer
	err = run(t.Context(), []string{"serve"}, io.Discard, &diagnostics, func(name string) string { return values[name] })
	assertBindFailure(t, err)
	if strings.Contains(diagnostics.String(), "HTTP service started") {
		t.Fatal("rejected startup reported a listening service")
	}
	if !reflect.DeepEqual(before, consoleStartupSnapshot(t, path)) {
		t.Error("rejected startup replaced the running Console credential or configuration")
	}
	finishCLIConsoleFlow(t, request, pendingLogin, "github")
	response := request("GET", "/console/api/session", "", "")
	defer response.Body.Close()
	var session struct {
		CSRF string `json:"csrf_token"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil || response.StatusCode != http.StatusOK || session.CSRF == "" || session.User.ID == "" {
		t.Fatalf("running Console login failed after rejected startup: status=%d decode=%v", response.StatusCode, err)
	}
	pendingLink := beginCLIConsoleFlow(t, request, "/console/api/identities/link", "microsoft", session.CSRF)
	values["UNIAUTH_INITIAL_ADMIN_EMAIL"] = "changed-admin@example.com"
	values["UNIAUTH_PUBLIC_URL"] = "http://localhost:9999"
	delete(values, "UNIAUTH_MICROSOFT_CLIENT_ID")
	delete(values, "UNIAUTH_MICROSOFT_CLIENT_SECRET")
	err = run(t.Context(), []string{"serve"}, io.Discard, io.Discard, func(name string) string { return values[name] })
	assertBindFailure(t, err)
	if !reflect.DeepEqual(before, consoleStartupSnapshot(t, path)) {
		t.Error("rejected startup changed pending bootstrap, App callback, providers, or private credential")
	}
	finishCLIConsoleFlow(t, request, pendingLink, "microsoft")
	response = request("GET", "/console/api/identities", "", "")
	defer response.Body.Close()
	var linked struct {
		Identities []struct {
			Provider string `json:"provider"`
		} `json:"identities"`
	}
	if err := json.NewDecoder(response.Body).Decode(&linked); err != nil || response.StatusCode != http.StatusOK || len(linked.Identities) != 2 {
		t.Fatalf("running Console link failed after rejected startup: status=%d decode=%v identities=%d", response.StatusCode, err, len(linked.Identities))
	}
	freshLogin := beginCLIConsoleFlow(t, request, "/console/api/login", "github", "")
	finishCLIConsoleFlow(t, request, freshLogin, "github")
	response = request("GET", "/console/api/session", "", "")
	defer response.Body.Close()
	var refreshed struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(response.Body).Decode(&refreshed); err != nil || response.StatusCode != http.StatusOK || refreshed.User.ID != session.User.ID {
		t.Fatalf("fresh Console login failed after rejected startup: status=%d decode=%v", response.StatusCode, err)
	}
}

func TestFailedBindLeavesDatabaseUntouched(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, historical := range []bool{false, true} {
		name := "absent database"
		if historical {
			name = "historical database"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "apps.db")
			if historical {
				createHistoricalCLIDatabase(t, path)
			}
			values := serveEnvironment(path, listener.Addr().String(), "http://localhost:8080")
			err := run(t.Context(), nil, io.Discard, io.Discard, func(name string) string { return values[name] })
			assertBindFailure(t, err)
			if !historical {
				for _, suffix := range []string{"", "-wal", "-shm"} {
					if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("rejected startup created a database file %q: %v", suffix, err)
					}
				}
				return
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var version, apps, secrets int
			for _, query := range []struct {
				statement string
				value     *int
			}{
				{"SELECT MAX(version_id) FROM goose_db_version", &version},
				{"SELECT COUNT(*) FROM apps WHERE id = 'existing-app'", &apps},
				{"SELECT COUNT(*) FROM app_secrets WHERE app_id = 'existing-app'", &secrets},
			} {
				if err := db.QueryRow(query.statement).Scan(query.value); err != nil {
					t.Fatal(err)
				}
			}
			if version != 2 || apps != 1 || secrets != 1 {
				t.Fatalf("rejected startup migrated existing data: version=%d apps=%d secrets=%d", version, apps, secrets)
			}
		})
	}
}

func TestInitializationFailureReleasesListener(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "database failure"
		if canceled {
			name = "canceled initialization"
		}
		t.Run(name, func(t *testing.T) {
			available, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := available.Addr().String()
			if err := available.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "missing", "apps.db")
			ctx := t.Context()
			if canceled {
				path = filepath.Join(t.TempDir(), "apps.db")
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			values := serveEnvironment(path, address, "http://localhost:8080")
			var diagnostics bytes.Buffer
			err = run(ctx, nil, io.Discard, &diagnostics, func(name string) string { return values[name] })
			if err == nil || !strings.Contains(err.Error(), "initialize service") || canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("initialization error cause lost: %v", err)
			}
			if !canceled {
				var cause interface{ Code() int }
				if !errors.As(err, &cause) {
					t.Fatalf("database initialization error cause lost: %v", err)
				}
			}
			if strings.Contains(diagnostics.String(), "HTTP service started") {
				t.Fatal("failed initialization reported a listening service")
			}
			replacement, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("failed initialization retained listener: %v", err)
			}
			if err := replacement.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func serveEnvironment(path, address, origin string) map[string]string {
	return map[string]string{
		"UNIAUTH_PUBLIC_URL": origin, "UNIAUTH_ALLOW_LOOPBACK_HTTP": "true", "UNIAUTH_INITIAL_ADMIN_EMAIL": "admin@example.com",
		"UNIAUTH_GITHUB_CLIENT_ID": "client", "UNIAUTH_GITHUB_CLIENT_SECRET": "secret", "UNIAUTH_LISTEN_ADDR": address, "UNIAUTH_DATABASE_PATH": path,
	}
}

func assertBindFailure(t *testing.T, err error) {
	t.Helper()
	var cause *net.OpError
	if err == nil || !strings.Contains(err.Error(), "listen for HTTP requests") || !errors.As(err, &cause) || cause.Op != "listen" {
		t.Fatalf("occupied listener error cause lost: %v", err)
	}
}

type cliOAuthProvider struct{ name string }

func (p cliOAuthProvider) Name() string { return p.name }
func (p cliOAuthProvider) AuthorizationURL(request providers.AuthorizationRequest) (string, error) {
	return "https://provider.example/authorize?state=" + url.QueryEscape(request.State), nil
}
func (p cliOAuthProvider) Exchange(_ context.Context, request providers.ExchangeRequest) (providers.Identity, error) {
	if request.Code != "provider-code" {
		return providers.Identity{}, errors.New("unexpected fixture authorization code")
	}
	email, verified := "member@example.com", true
	identity := providers.Identity{Provider: p.name, ID: p.name + ":member", Email: &email, Origin: map[string]json.RawMessage{}}
	if p.name == "github" {
		identity.EmailVerified = &verified
	}
	return identity, nil
}

func beginCLIConsoleFlow(t *testing.T, request func(string, string, string, string) *http.Response, endpoint, provider, csrf string) string {
	t.Helper()
	response := request("POST", endpoint, `{"provider":"`+provider+`"}`, csrf)
	defer response.Body.Close()
	var started struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil || response.StatusCode != http.StatusOK || started.AuthorizationURL == "" {
		t.Fatalf("begin Console OAuth: status=%d decode=%v", response.StatusCode, err)
	}
	return started.AuthorizationURL
}

func finishCLIConsoleFlow(t *testing.T, request func(string, string, string, string) *http.Response, authorization, provider string) {
	t.Helper()
	location, err := url.Parse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	response := request("GET", location.RequestURI(), "", "")
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("authorize running Console: %d", response.StatusCode)
	}
	upstream, err := url.Parse(response.Header.Get("Location"))
	if err != nil || upstream.Query().Get("state") == "" {
		t.Fatalf("upstream OAuth state missing: %v", err)
	}
	response = request("GET", "/auth/callback/"+provider+"?code=provider-code&state="+url.QueryEscape(upstream.Query().Get("state")), "", "")
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("complete upstream OAuth: %d", response.StatusCode)
	}
	callback, err := url.Parse(response.Header.Get("Location"))
	if err != nil || callback.Path != "/console/auth/callback" {
		t.Fatalf("unexpected product callback: %v", err)
	}
	response = request("GET", callback.RequestURI(), "", "")
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/console/" {
		t.Fatalf("live Console callback failed after rejected startup: status=%d redirect=%s", response.StatusCode, response.Header.Get("Location"))
	}
}

func consoleStartupSnapshot(t *testing.T, path string) []any {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var email, appID, callbacks, providerNames, secretID string
	var claimed sql.NullString
	var updated, created int64
	var digest []byte
	if err := db.QueryRow(`SELECT s.initial_admin_email,s.claimed_account_id,a.id,a.redirect_uris,a.providers,a.updated_at,c.id,c.digest,c.created_at
		FROM platform_state s JOIN apps a ON a.id = s.console_app_id JOIN app_secrets c ON c.app_id = a.id WHERE s.id = 1`).Scan(
		&email, &claimed, &appID, &callbacks, &providerNames, &updated, &secretID, &digest, &created); err != nil {
		t.Fatal(err)
	}
	return []any{email, claimed, appID, callbacks, providerNames, updated, secretID, digest, created}
}

func createHistoricalCLIDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, version_id INTEGER NOT NULL, is_applied BOOLEAN NOT NULL, tstamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO goose_db_version (version_id,is_applied) VALUES (0,1),(1,1),(2,1)`,
		`CREATE TABLE apps (id TEXT PRIMARY KEY NOT NULL,name TEXT NOT NULL,enabled INTEGER NOT NULL,redirect_uris TEXT NOT NULL,providers TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
		`CREATE TABLE app_secrets (id TEXT PRIMARY KEY NOT NULL,app_id TEXT NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL,digest BLOB NOT NULL,created_at INTEGER NOT NULL,revoked_at INTEGER)`,
		`INSERT INTO apps VALUES ('existing-app','Existing',1,'["https://product.example/callback"]','["github"]',1,1)`,
		`INSERT INTO app_secrets VALUES ('existing-secret','existing-app','Backend','',zeroblob(32),1,NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}
