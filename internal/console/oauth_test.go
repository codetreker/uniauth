package console

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/auth"
	"github.com/codetreker/uniauth/internal/providers"
)

func TestOAuthPublicProvidersAndUnavailableSelection(t *testing.T) {
	e := newTestConsole(t, nil)
	response := e.request("GET", "/console/api/providers", "", nil)
	requireStatus(t, response, 200)
	if !strings.Contains(response.Body.String(), `"id":"google","available":false`) {
		t.Fatal(response.Body.String())
	}
	for _, body := range []string{`{}`, `null`, `[]`, `{"provider":"google"}`, `{"provider":null}`, `{"provider":"github","identity":{"id":"admin"}}`, `{"provider":"github"} {}`} {
		requireStatus(t, e.request("POST", "/console/api/login", body, nil), 400)
	}
	requireStatus(t, e.request("GET", "/console/api/login", "", nil), 405)
	requireStatus(t, e.request("POST", "/console/api/identities/link", `{"provider":"github"}`, nil), 401)
	e.login(t)
	if !e.account.IsAdmin || e.account.Email == nil || *e.account.Email != "admin@example.com" {
		t.Fatalf("verified bootstrap failed: %+v", e.account)
	}
	identities := decodeResponse[struct{ Identities []apps.LinkedIdentity }](t, e.request("GET", "/console/api/identities", "", nil))
	if len(identities.Identities) != 1 || identities.Identities[0].Provider != "github" {
		t.Fatalf("missing linked identity %+v", identities)
	}
}

func TestOAuthBrowserBindingParsingReplayAndExpiry(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(consoleFlow, string) (string, []*http.Cookie)
	}{
		{"missing cookie", func(f consoleFlow, location string) (string, []*http.Cookie) { return location, nil }},
		{"wrong cookie", func(f consoleFlow, location string) (string, []*http.Cookie) {
			cookie := *f.pending
			cookie.Value = randomToken()
			return location, []*http.Cookie{&cookie}
		}},
		{"duplicate cookie", func(f consoleFlow, location string) (string, []*http.Cookie) {
			return location, []*http.Cookie{f.pending, f.pending}
		}},
		{"missing state", func(f consoleFlow, location string) (string, []*http.Cookie) {
			u, _ := url.Parse(location)
			q := u.Query()
			q.Del("state")
			u.RawQuery = q.Encode()
			return u.String(), []*http.Cookie{f.pending}
		}},
		{"duplicate state", func(f consoleFlow, location string) (string, []*http.Cookie) {
			return location + "&state=" + f.state, []*http.Cookie{f.pending}
		}},
		{"duplicate code", func(f consoleFlow, location string) (string, []*http.Cookie) {
			return location + "&code=another", []*http.Cookie{f.pending}
		}},
		{"ambiguous error", func(f consoleFlow, location string) (string, []*http.Cookie) {
			return location + "&error=denied", []*http.Cookie{f.pending}
		}},
		{"unknown parameter", func(f consoleFlow, location string) (string, []*http.Cookie) {
			return location + "&email=private@example.com", []*http.Cookie{f.pending}
		}},
		{"invalid query", func(f consoleFlow, location string) (string, []*http.Cookie) {
			return location + "&invalid=%XX", []*http.Cookie{f.pending}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestConsole(t, nil)
			flow := e.startFlow(t, "github", "/console/api/login")
			location := e.gatewayCallback(t, flow, "github", "admin")
			invalid, cookies := tc.mutate(flow, location)
			response := e.consoleCallback(t, invalid, cookies...)
			if !strings.HasPrefix(response.Header().Get("Location"), "/console/?login_error=") {
				t.Fatalf("invalid callback succeeded: %v", response.Header())
			}
			requireStatus(t, e.request("GET", "/console/api/session", "", nil), 401)
			e.acceptLogin(t, e.consoleCallback(t, location, flow.pending))
			replay := e.consoleCallback(t, location, flow.pending, e.cookie)
			if replay.Header().Get("Location") != "/console/?login_error=login_unavailable" {
				t.Fatal("replay accepted")
			}
			requireStatus(t, e.request("GET", "/console/api/session", "", nil), 200)
		})
	}
	t.Run("expiry boundary", func(t *testing.T) {
		e := newTestConsole(t, func(c *Config) { c.PendingTTL = time.Minute })
		flow := e.startFlow(t, "github", "/console/api/login")
		location := e.gatewayCallback(t, flow, "github", "admin")
		e.now = e.now.Add(time.Minute)
		response := e.consoleCallback(t, location, flow.pending)
		if response.Header().Get("Location") != "/console/?login_error=login_unavailable" {
			t.Fatal("expired login accepted")
		}
		if len(e.handler.pending) != 0 {
			t.Fatal("expired login retained")
		}
	})
	t.Run("method", func(t *testing.T) {
		e := newTestConsole(t, nil)
		requireStatus(t, e.request("POST", "/console/auth/callback", "", nil), 405)
	})
}

func TestOAuthProviderErrorsResultOwnershipAndConcurrentConsumption(t *testing.T) {
	t.Run("upstream failure", func(t *testing.T) {
		e := newTestConsole(t, nil)
		flow := e.startFlow(t, "github", "/console/api/login")
		location := e.gatewayCallback(t, flow, "github", "failure")
		reply := e.consoleCallback(t, location, flow.pending)
		if reply.Header().Get("Location") != "/console/?login_error=provider_failed" || len(e.handler.pending) != 0 {
			t.Fatalf("provider error not consumed: %s", reply.Header().Get("Location"))
		}
	})
	t.Run("unavailable result", func(t *testing.T) {
		e := newTestConsole(t, nil)
		flow := e.startFlow(t, "github", "/console/api/login")
		reply := e.consoleCallback(t, e.handler.origin+"/console/auth/callback?"+url.Values{"state": {flow.state}, "code": {randomToken()}}.Encode(), flow.pending)
		if reply.Header().Get("Location") != "/console/?login_error=login_unavailable" {
			t.Fatal(reply.Header())
		}
	})
	t.Run("different provider", func(t *testing.T) {
		e := newTestConsole(t, nil)
		github := e.startFlow(t, "github", "/console/api/login")
		githubLocation := e.gatewayCallback(t, github, "github", "admin")
		microsoft := e.startFlow(t, "microsoft", "/console/api/login")
		u, _ := url.Parse(githubLocation)
		q := u.Query()
		q.Set("state", microsoft.state)
		u.RawQuery = q.Encode()
		response := e.consoleCallback(t, u.String(), microsoft.pending)
		if response.Header().Get("Location") != "/console/?login_error=login_failed" {
			t.Fatal("provider mismatch accepted")
		}
		requireStatus(t, e.request("GET", "/console/api/session", "", nil), 401)
	})
	t.Run("once concurrently", func(t *testing.T) {
		e := newTestConsole(t, nil)
		flow := e.startFlow(t, "github", "/console/api/login")
		location := e.gatewayCallback(t, flow, "github", "admin")
		var wg sync.WaitGroup
		results := make(chan *httptest.ResponseRecorder, 12)
		for range 12 {
			wg.Go(func() {
				req := httptest.NewRequest("GET", location, nil)
				req.AddCookie(flow.pending)
				reply := httptest.NewRecorder()
				e.mux.ServeHTTP(reply, req)
				results <- reply
			})
		}
		wg.Wait()
		close(results)
		accepted := 0
		for reply := range results {
			if reply.Header().Get("Location") == "/console/" {
				accepted++
			}
		}
		if accepted != 1 || len(e.handler.sessions) != 1 {
			t.Fatalf("concurrent claims accepted %d sessions%d", accepted, len(e.handler.sessions))
		}
	})
}

func TestOAuthCapacityRotationAndLifecycle(t *testing.T) {
	e := newTestConsole(t, func(c *Config) {
		c.MaxSessions = 1
		c.MaxPending = 2
		c.PendingTTL = time.Minute
		c.SessionTTL = time.Minute
	})
	e.login(t)
	old := e.cookie
	// Rotation uses the initiating session record even when Strict cookies are
	// omitted by the cross-site return to the product callback.
	flow := e.startFlow(t, "github", "/console/api/login")
	location := e.gatewayCallback(t, flow, "github", "admin")
	e.acceptLogin(t, e.consoleCallback(t, location, flow.pending))
	if old.Value == e.cookie.Value {
		t.Fatal("login did not rotate session")
	}
	current := e.cookie
	e.cookie = old
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), 401)
	e.cookie = current
	e.cookie = nil
	e.csrf = ""
	flow = e.startFlow(t, "github", "/console/api/login")
	location = e.gatewayCallback(t, flow, "github", "other")
	reply := e.consoleCallback(t, location, flow.pending)
	if reply.Header().Get("Location") != "/console/?login_error=session_capacity" {
		t.Fatal("capacity bypassed")
	}
	e.now = e.now.Add(time.Minute)
	e.login(t)
	e.startFlow(t, "github", "/console/api/login")
	e.startFlow(t, "github", "/console/api/login")
	requireStatus(t, e.request("POST", "/console/api/login", `{"provider":"github"}`, nil), 503)
	e.now = e.now.Add(time.Minute)
	requireStatus(t, e.request("POST", "/console/api/login", `{"provider":"github"}`, nil), 200)
	if err := e.handler.Close(); err != nil {
		t.Fatal(err)
	}
	if len(e.handler.sessions) != 0 || len(e.handler.pending) != 0 {
		t.Fatal("close retained transient state")
	}
	requireStatus(t, e.request("GET", "/console/api/providers", "", nil), 503)
	if err := e.handler.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthLinkingLiveSessionAndIdentityCollision(t *testing.T) {
	for _, mode := range []string{"success without Strict cookie", "success with Strict cookie", "logout", "expiry", "mismatched user", "duplicate Strict cookie", "identity collision"} {
		t.Run(mode, func(t *testing.T) {
			e := newTestConsole(t, func(c *Config) { c.SessionTTL = time.Minute })
			e.login(t)
			adminCookie, adminCSRF, adminID := e.cookie, e.csrf, e.account.ID
			if mode == "identity collision" {
				e.cookie = nil
				e.csrf = ""
				other := e.startFlow(t, "microsoft", "/console/api/login")
				location := e.gatewayCallback(t, other, "microsoft", "other")
				e.acceptLogin(t, e.consoleCallback(t, location, other.pending))
				e.cookie, e.csrf = adminCookie, adminCSRF
			}
			flow := e.startFlow(t, "microsoft", "/console/api/identities/link")
			code := "linked"
			if mode == "identity collision" {
				code = "other"
			}
			location := e.gatewayCallback(t, flow, "microsoft", code)
			cookies := []*http.Cookie{flow.pending}
			switch mode {
			case "success with Strict cookie":
				cookies = append(cookies, adminCookie)
			case "logout":
				requireStatus(t, e.request("POST", "/console/api/logout", "", nil), 204)
			case "expiry":
				e.now = e.now.Add(time.Minute)
			case "mismatched user":
				e.cookie = nil
				e.csrf = ""
				other := e.startFlow(t, "github", "/console/api/login")
				otherLocation := e.gatewayCallback(t, other, "github", "other")
				e.acceptLogin(t, e.consoleCallback(t, otherLocation, other.pending))
				cookies = append(cookies, e.cookie)
			case "duplicate Strict cookie":
				cookies = append(cookies, adminCookie, adminCookie)
			}
			reply := e.consoleCallback(t, location, cookies...)
			success := strings.HasPrefix(mode, "success")
			if success && reply.Header().Get("Location") != "/console/" {
				t.Fatal(reply.Header())
			}
			if !success && !strings.Contains(reply.Header().Get("Location"), "login_error=") {
				t.Fatal("invalid link accepted")
			}
			identities, err := e.store.ListIdentities(context.Background(), adminID)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if success {
				want = 2
			}
			if len(identities) != want {
				t.Fatalf("identity set %+v", identities)
			}
			replay := e.consoleCallback(t, location, flow.pending)
			if replay.Header().Get("Location") != "/console/?login_error=login_unavailable" {
				t.Fatal("link replay accepted")
			}
			if success {
				e.cookie = nil
				e.csrf = ""
				later := e.startFlow(t, "microsoft", "/console/api/login")
				laterLocation := e.gatewayCallback(t, later, "microsoft", "linked")
				e.acceptLogin(t, e.consoleCallback(t, laterLocation, later.pending))
				if e.account.ID != adminID || !e.account.IsAdmin {
					t.Fatal("linked login changed account")
				}
			}
		})
	}
}

func TestOAuthTenancyAllManagementRoutesAndAdminPrivacy(t *testing.T) {
	e := newTestConsole(t, nil)
	e.login(t)
	adminCookie, adminCSRF, adminID := e.cookie, e.csrf, e.account.ID
	created := decodeResponse[apps.CreatedApp](t, e.request("POST", "/console/api/apps", createAppBody, nil))
	e.cookie = nil
	e.csrf = ""
	flow := e.startFlow(t, "github", "/console/api/login")
	location := e.gatewayCallback(t, flow, "github", "member")
	e.acceptLogin(t, e.consoleCallback(t, location, flow.pending))
	if e.account.IsAdmin {
		t.Fatal("member promoted")
	}
	memberCookie, memberCSRF, memberID := e.cookie, e.csrf, e.account.ID
	paths := []struct{ method, path, body string }{
		{"GET", "/console/api/apps/" + created.App.ID, ""}, {"PUT", "/console/api/apps/" + created.App.ID, appBody}, {"DELETE", "/console/api/apps/" + created.App.ID, ""},
		{"GET", "/console/api/apps/" + created.App.ID + "/secrets", ""}, {"POST", "/console/api/apps/" + created.App.ID + "/secrets", secretBody},
		{"PUT", "/console/api/apps/" + created.App.ID + "/secrets/" + created.SecretID, secretBody}, {"DELETE", "/console/api/apps/" + created.App.ID + "/secrets/" + created.SecretID, ""},
		{"POST", "/console/api/apps/" + created.App.ID + "/secrets/" + created.SecretID + "/reset", ""},
	}
	for _, request := range paths {
		requireStatus(t, e.request(request.method, request.path, request.body, nil), 404)
	}
	requireStatus(t, e.request("GET", "/console/api/users", "", nil), 403)
	memberApp := decodeResponse[apps.CreatedApp](t, e.request("POST", "/console/api/apps", strings.Replace(createAppBody, "Product A", "Private member app", 1), nil))
	list := decodeResponse[struct{ Apps []apps.App }](t, e.request("GET", "/console/api/apps", "", nil))
	if len(list.Apps) != 1 || list.Apps[0].ID != memberApp.App.ID {
		t.Fatal("tenant list leaked apps")
	}
	e.cookie, e.csrf = adminCookie, adminCSRF
	requireStatus(t, e.request("GET", "/console/api/apps/"+memberApp.App.ID, "", nil), 404)
	list = decodeResponse[struct{ Apps []apps.App }](t, e.request("GET", "/console/api/apps", "", nil))
	if len(list.Apps) != 1 || list.Apps[0].ID != created.App.ID {
		t.Fatal("admin apps leaked")
	}
	for _, request := range paths {
		path := strings.ReplaceAll(request.path, created.App.ID, memberApp.App.ID)
		path = strings.ReplaceAll(path, created.SecretID, memberApp.SecretID)
		requireStatus(t, e.request(request.method, path, request.body, nil), 404)
	}
	systemPath := "/console/api/apps/" + e.handler.config.AppID
	for _, request := range []struct{ method, path, body string }{{"GET", systemPath, ""}, {"PUT", systemPath, appBody}, {"DELETE", systemPath, ""}, {"GET", systemPath + "/secrets", ""}, {"POST", systemPath + "/secrets", secretBody}, {"POST", systemPath + "/secrets/missing/reset", ""}} {
		requireStatus(t, e.request(request.method, request.path, request.body, nil), 404)
	}
	response := e.request("GET", "/console/api/users?limit=1", "", nil)
	requireStatus(t, response, 200)
	page := decodeResponse[apps.UserPage](t, response)
	if len(page.Users) != 1 || page.NextCursor == nil {
		t.Fatalf("pagination missing %+v", page)
	}
	next := e.request("GET", "/console/api/users?limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", nil)
	requireStatus(t, next, 200)
	page2 := decodeResponse[apps.UserPage](t, next)
	if len(page2.Users) != 1 || page2.Users[0].ID == page.Users[0].ID {
		t.Fatal("page repeated")
	}
	for _, user := range append(page.Users, page2.Users...) {
		if user.AppCount != 1 || (user.ID != adminID && user.ID != memberID) {
			t.Fatalf("counts %+v", user)
		}
	}
	var raw struct{ Users []map[string]json.RawMessage }
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, user := range raw.Users {
		if len(user) != 5 {
			t.Fatalf("admin user details leaked %+v", user)
		}
		for key := range user {
			if key != "id" && key != "name" && key != "email" && key != "created_at" && key != "app_count" {
				t.Fatalf("field leaked %s", key)
			}
		}
	}
	for _, private := range []string{memberApp.App.ID, memberApp.Secret, created.Secret, "Private member app", "origin", "avatar_url", "is_admin"} {
		if strings.Contains(response.Body.String()+next.Body.String(), private) {
			t.Fatalf("private admin data %s", private)
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=01", "limit=x", "limit=1&limit=2", "cursor=x&cursor=y", "extra=x", "cursor=%XX", "cursor=" + strings.Repeat("x", 513), "cursor=not-valid"} {
		requireStatus(t, e.request("GET", "/console/api/users?"+query, "", nil), 400)
	}
	e.cookie, e.csrf = memberCookie, memberCSRF
	requireStatus(t, e.request("GET", "/console/api/session", "", nil), 200)
}

func TestOAuthCallbackLogsAreSanitizedAndStorageErrorsDoNotLeak(t *testing.T) {
	var output bytes.Buffer
	e := newTestConsole(t, func(c *Config) { c.Logger = slog.New(slog.NewJSONHandler(&output, nil)) })
	flow := e.startFlow(t, "github", "/console/api/login")
	location := e.gatewayCallback(t, flow, "github", "admin")
	e.acceptLogin(t, e.consoleCallback(t, location, flow.pending))
	for _, value := range []string{flow.state, flow.pending.Value, e.cookie.Value, e.csrf, "admin@example.com", "Admin", "private", "origin"} {
		if strings.Contains(output.String(), value) {
			t.Fatalf("callback log leak %q: %s", value, output.String())
		}
	}
	output.Reset()
	flow = e.startFlow(t, "github", "/console/api/login")
	location = e.gatewayCallback(t, flow, "github", "other")
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	reply := e.consoleCallback(t, location, flow.pending)
	if reply.Header().Get("Location") != "/console/?login_error=login_failed" {
		t.Fatal(reply.Header())
	}
	if strings.Contains(output.String(), "database") || strings.Contains(output.String(), "SQL") {
		t.Fatal("storage error exposed")
	}
}

func TestOAuthConfigurationValidationAndLoopbackCookies(t *testing.T) {
	e := newTestConsole(t, nil)
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"nil gateway", func(c *Config) { c.Gateway = nil }}, {"missing app", func(c *Config) { c.AppID = "" }}, {"missing secret", func(c *Config) { c.AppSecret = "" }},
		{"URL", func(c *Config) { c.PublicURL = "https://auth.example:%xx" }}, {"host", func(c *Config) { c.PublicURL = "https:///" }}, {"userinfo", func(c *Config) { c.PublicURL = "https://admin@auth.example" }},
		{"path", func(c *Config) { c.PublicURL = "https://auth.example/path" }}, {"query", func(c *Config) { c.PublicURL = "https://auth.example?x=y" }}, {"empty query", func(c *Config) { c.PublicURL = "https://auth.example?" }}, {"fragment", func(c *Config) { c.PublicURL = "https://auth.example#" }},
		{"public HTTP", func(c *Config) { c.PublicURL = "http://auth.example"; c.AllowLoopbackHTTP = true }}, {"implicit loopback", func(c *Config) { c.PublicURL = "http://localhost:8080" }},
		{"session lifetime", func(c *Config) { c.SessionTTL = -time.Second }}, {"long session", func(c *Config) { c.SessionTTL = 25 * time.Hour }}, {"session capacity", func(c *Config) { c.MaxSessions = -1 }}, {"large sessions", func(c *Config) { c.MaxSessions = 100001 }},
		{"pending lifetime", func(c *Config) { c.PendingTTL = -time.Second }}, {"long pending", func(c *Config) { c.PendingTTL = 11 * time.Minute }}, {"pending capacity", func(c *Config) { c.MaxPending = -1 }}, {"large pending", func(c *Config) { c.MaxPending = 100001 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := e.handler.config
			tc.change(&cfg)
			if _, err := New(e.store, []string{"github"}, cfg); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	if _, err := New(nil, []string{"github"}, e.handler.config); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := New(e.store, nil, e.handler.config); err == nil {
		t.Fatal("no providers accepted")
	}
	if _, err := New(e.store, []string{"other"}, e.handler.config); err == nil {
		t.Fatal("unknown provider accepted")
	}
	loopback := newTestConsole(t, func(c *Config) { c.PublicURL = "http://127.0.0.1:8080"; c.AllowLoopbackHTTP = true })
	flow := loopback.startFlow(t, "github", "/console/api/login")
	if flow.pending.Secure || strings.HasPrefix(flow.pending.Name, "__Host-") {
		t.Fatal("invalid loopback pending cookie")
	}
	location := loopback.gatewayCallback(t, flow, "github", "admin")
	loopback.acceptLogin(t, loopback.consoleCallback(t, location, flow.pending))
	if loopback.cookie.Secure || loopback.cookie.Name != "uniauth_console" || loopback.cookie.Domain != "" {
		t.Fatal("invalid loopback session")
	}
}

func TestOAuthMicrosoftCannotBootstrapAndConfiguredGoogleCanSignIn(t *testing.T) {
	e := newTestConsole(t, nil)
	microsoft := e.startFlow(t, "microsoft", "/console/api/login")
	location := e.gatewayCallback(t, microsoft, "microsoft", "admin")
	e.acceptLogin(t, e.consoleCallback(t, location, microsoft.pending))
	microsoftID := e.account.ID
	if e.account.IsAdmin {
		t.Fatal("unknown Microsoft verification granted admin")
	}
	e.cookie = nil
	e.csrf = ""
	e.login(t)
	if !e.account.IsAdmin || e.account.ID == microsoftID {
		t.Fatal("equal email auto-merged accounts or failed verified bootstrap")
	}
	// Provider availability is deployment-owned; a configured Google adapter
	// uses the same confidential Console App and one-time handover path.
	consoleApp, err := e.store.ProvisionConsole(context.Background(), e.handler.origin+"/console/auth/callback", []string{"microsoft", "google", "github"})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := auth.New(context.Background(), auth.Config{PublicURL: e.handler.origin, TransactionTTL: 10 * time.Minute, CallbackTimeout: time.Second, CleanupInterval: time.Minute, MaxTransactions: 100, MaxTransactionBytes: 1 << 20, MaxResults: 100, MaxResultBytes: 1 << 20, Now: e.handler.config.Now, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, e.store, map[string]providers.Provider{"microsoft": consoleProvider("microsoft"), "google": consoleProvider("google"), "github": consoleProvider("github")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	cfg := e.handler.config
	cfg.Gateway, cfg.AppID, cfg.AppSecret = gateway, consoleApp.App.ID, consoleApp.Secret
	handler, err := New(e.store, []string{"microsoft", "google", "github"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	e.handler = handler
	e.mux = http.NewServeMux()
	handler.Register(e.mux)
	gateway.Register(e.mux)
	e.cookie = nil
	e.csrf = ""
	google := e.startFlow(t, "google", "/console/api/login")
	location = e.gatewayCallback(t, google, "google", "member")
	e.acceptLogin(t, e.consoleCallback(t, location, google.pending))
	if e.account.IsAdmin {
		t.Fatal("public Google registration became admin")
	}
	response := e.request("GET", "/console/api/providers", "", nil)
	if strings.Count(response.Body.String(), `"available":true`) != 3 {
		t.Fatal("configured provider omitted")
	}
}
