package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/providers"
)

type testProvider struct {
	name       string
	mu         sync.Mutex
	requests   map[string]providers.AuthorizationRequest
	exchange   func(context.Context, providers.ExchangeRequest) (providers.Identity, error)
	startError error
	startHook  func()
	calls      atomic.Int32
}

func (p *testProvider) Name() string { return p.name }
func (p *testProvider) AuthorizationURL(request providers.AuthorizationRequest) (string, error) {
	if p.startHook != nil {
		p.startHook()
	}
	if p.startError != nil {
		return "", p.startError
	}
	p.mu.Lock()
	p.requests[request.State] = request
	p.mu.Unlock()
	digest := sha256.Sum256([]byte(request.CodeVerifier))
	query := url.Values{"state": {request.State}, "nonce": {request.Nonce}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}}
	return "https://provider.example/authorize?" + query.Encode(), nil
}
func (p *testProvider) Exchange(ctx context.Context, request providers.ExchangeRequest) (providers.Identity, error) {
	p.calls.Add(1)
	if p.exchange != nil {
		return p.exchange(ctx, request)
	}
	return providers.Identity{Provider: p.name, Subject: p.name + ":123", Sources: map[string]json.RawMessage{"profile": json.RawMessage(`{"id":123,"unknown":{"nested":true},"email":null,"verified":false}`)}}, nil
}

type fixture struct {
	t              *testing.T
	h              *Handler
	mux            *http.ServeMux
	store          *apps.Store
	app, other     apps.CreatedApp
	google, github *testProvider
	clock          atomic.Int64
}

func newFixture(t *testing.T, modify func(*Config)) *fixture {
	t.Helper()
	f := &fixture{t: t}
	f.clock.Store(time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC).UnixNano())
	store, err := apps.Open(context.Background(), apps.Config{Path: filepath.Join(t.TempDir(), "apps.db")})
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	input := apps.AppInput{Name: "Product", Enabled: true, RedirectURIs: []string{"https://product.example/callback?existing=preserved"}, Providers: []string{"google", "github"}}
	f.app, err = store.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "Other"
	f.other, err = store.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	f.google = &testProvider{name: "google", requests: make(map[string]providers.AuthorizationRequest)}
	f.github = &testProvider{name: "github", requests: make(map[string]providers.AuthorizationRequest)}
	config := Config{PublicURL: "https://auth.example", TransactionTTL: 10 * time.Minute, CallbackTimeout: time.Second, CleanupInterval: time.Hour, MaxTransactions: 64, MaxTransactionBytes: 1 << 20, MaxResults: 64, MaxResultBytes: 16 << 20, Now: func() time.Time { return time.Unix(0, f.clock.Load()) }, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	if modify != nil {
		modify(&config)
	}
	f.h, err = New(context.Background(), config, store, map[string]providers.Provider{"google": f.google, "github": f.github})
	if err != nil {
		t.Fatal(err)
	}
	f.mux = http.NewServeMux()
	f.h.Register(f.mux)
	t.Cleanup(func() {
		if err := f.h.Close(); err != nil {
			t.Error(err)
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *fixture) call(method, path string, body io.Reader, cookies []*http.Cookie, credentials *apps.CreatedApp) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if method == "POST" && path == "/auth/result" {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if credentials != nil {
		request.SetBasicAuth(credentials.App.ID, credentials.Secret)
	}
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	return response
}

type flow struct {
	state        string
	cookie       *http.Cookie
	productState string
}

func (f *fixture) start(productState string) flow {
	f.t.Helper()
	query := url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {productState}}
	response := f.call("GET", "/auth/authorize?"+query.Encode(), nil, nil, nil)
	if response.Code != 302 {
		f.t.Fatalf("start = %d: %s", response.Code, response.Body)
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		f.t.Fatalf("cookies: %v", cookies)
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || !strings.HasPrefix(cookie.Name, "__Host-") {
		f.t.Fatalf("insecure flow cookie: %#v", cookie)
	}
	state := location.Query().Get("state")
	f.google.mu.Lock()
	request := f.google.requests[state]
	f.google.mu.Unlock()
	if state == productState || request.Nonce == "" || len(request.CodeVerifier) != 43 || location.Query().Get("code_challenge_method") != "S256" {
		f.t.Fatal("upstream security values were not generated independently")
	}
	return flow{state: state, cookie: cookie, productState: productState}
}
func (f *fixture) complete(flow flow) string {
	f.t.Helper()
	response := f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"upstream-secret-code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
	if response.Code != 302 {
		f.t.Fatalf("callback = %d: %s", response.Code, response.Body)
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	if location.Scheme != "https" || location.Host != "product.example" || location.Query().Get("state") != flow.productState || location.Query().Get("existing") != "preserved" || location.Query().Get("code") == "" {
		f.t.Fatalf("incorrect product redirect: %s", location)
	}
	return location.Query().Get("code")
}
func (f *fixture) retrieve(code string, credentials *apps.CreatedApp) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(map[string]string{"code": code})
	return f.call("POST", "/auth/result", bytes.NewReader(payload), nil, credentials)
}
func assertCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != code {
		t.Fatalf("error = %s, want %s", response.Body, code)
	}
}

func TestBrowserBindingsAndProductHandover(t *testing.T) {
	f := newFixture(t, nil)
	first := f.start("product-state + & ?")
	second := f.start("second-tab")
	if first.cookie.Name == second.cookie.Name || first.cookie.Value == second.cookie.Value || first.state == second.state {
		t.Fatal("parallel first logins share browser state")
	}
	path := "/auth/callback/google?" + url.Values{"state": {first.state}, "code": {"provider-code"}}.Encode()
	for _, cookies := range [][]*http.Cookie{nil, {second.cookie}, {{Name: first.cookie.Name, Value: "modified"}}, {first.cookie, first.cookie}} {
		assertCode(t, f.call("GET", path, nil, cookies, nil), 410, "transaction_unavailable")
	}
	assertCode(t, f.call("GET", strings.Replace(path, "/google?", "/github?", 1), nil, []*http.Cookie{first.cookie}, nil), 410, "transaction_unavailable")
	assertCode(t, f.call("GET", path+"&state=modified", nil, []*http.Cookie{first.cookie}, nil), 400, "invalid_request")
	code := f.complete(first)
	assertCode(t, f.call("GET", path, nil, []*http.Cookie{first.cookie}, nil), 410, "transaction_unavailable")
	secondCode := f.complete(second)
	bad := f.app
	bad.Secret = "incorrect"
	assertCode(t, f.retrieve(code, &bad), 401, "invalid_app")
	assertCode(t, f.retrieve(code, &f.other), 410, "result_unavailable")
	assertCode(t, f.call("POST", "/auth/result", strings.NewReader(`{"code":"`+code+`","extra":true}`), nil, &f.app), 400, "invalid_request")
	response := f.retrieve(code, &f.app)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("result = %d: %s", response.Code, response.Body)
	}
	var identity providers.Identity
	if err := json.Unmarshal(response.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "google:123" || !bytes.Contains(identity.Sources["profile"], []byte(`"verified":false`)) || !bytes.Contains(identity.Sources["profile"], []byte(`"email":null`)) || !bytes.Contains(identity.Sources["profile"], []byte(`"unknown"`)) {
		t.Fatalf("identity fields lost: %s", response.Body)
	}
	assertCode(t, f.retrieve(code, &f.app), 410, "result_unavailable")
	if f.retrieve(secondCode, &f.app).Code != 200 {
		t.Fatal("second tab result disappeared")
	}
}

func TestConcurrentResultClaims(t *testing.T) {
	f := newFixture(t, nil)
	code := f.complete(f.start("product-state"))
	var successful atomic.Int32
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			response := f.retrieve(code, &f.app)
			switch response.Code {
			case 200:
				successful.Add(1)
			case 410:
			default:
				t.Errorf("claim = %d: %s", response.Code, response.Body)
			}
		})
	}
	wg.Wait()
	if successful.Load() != 1 {
		t.Fatalf("successful claims = %d", successful.Load())
	}
}

func TestExactExpiryAndCreationDeadline(t *testing.T) {
	f := newFixture(t, nil)
	flow := f.start("product-state")
	f.clock.Add(int64(8 * time.Minute))
	code := f.complete(flow)
	f.clock.Add(int64(ResultTTL - time.Nanosecond))
	if f.retrieve(code, &f.app).Code != 200 {
		t.Fatal("result TTL started before identity completion")
	}
	code = f.complete(f.start("new-product-state"))
	f.clock.Add(int64(ResultTTL))
	assertCode(t, f.retrieve(code, &f.app), 410, "result_unavailable")
	flow = f.start("expired-transaction")
	f.clock.Add(int64(f.h.config.TransactionTTL))
	assertCode(t, f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil), 410, "transaction_unavailable")
}

func TestCallbackSingleClaimAndActiveCapacity(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.MaxTransactions = 1 })
	started, release := make(chan providers.ExchangeRequest, 1), make(chan struct{})
	f.google.exchange = func(ctx context.Context, request providers.ExchangeRequest) (providers.Identity, error) {
		started <- request
		select {
		case <-release:
			return providers.Identity{Provider: "google", Subject: "google:123", Sources: map[string]json.RawMessage{"claims": json.RawMessage(`{"sub":"123"}`)}}, nil
		case <-ctx.Done():
			return providers.Identity{}, ctx.Err()
		}
	}
	flow := f.start("product-state")
	path := "/auth/callback/google?" + url.Values{"state": {flow.state}, "code": {"provider-code"}}.Encode()
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() { completed <- f.call("GET", path, nil, []*http.Cookie{flow.cookie}, nil) }()
	request := <-started
	f.google.mu.Lock()
	authRequest := f.google.requests[flow.state]
	f.google.mu.Unlock()
	if request.ExpectedNonce != authRequest.Nonce || request.CodeVerifier != authRequest.CodeVerifier || request.Code != "provider-code" {
		t.Fatal("callback security values were not delegated exactly")
	}
	assertCode(t, f.call("GET", path, nil, []*http.Cookie{flow.cookie}, nil), 410, "transaction_unavailable")
	query := url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"another"}}
	assertCode(t, f.call("GET", "/auth/authorize?"+query.Encode(), nil, nil, nil), 503, "capacity_exceeded")
	close(release)
	if response := <-completed; response.Code != 302 {
		t.Fatalf("callback = %d", response.Code)
	}
	if f.google.calls.Load() != 1 {
		t.Fatalf("exchanges = %d", f.google.calls.Load())
	}
}

func TestProviderErrorsAndSerialization(t *testing.T) {
	for _, kind := range []string{"denied", "exchange", "invalid-source", "invalid-identity"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, nil)
			flow := f.start("product-state")
			query := url.Values{"state": {flow.state}, "code": {"provider-code"}}
			switch kind {
			case "denied":
				query.Del("code")
				query.Set("error", "access_denied")
				query.Set("error_description", "upstream-private-message")
			case "exchange":
				f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
					return providers.Identity{}, errors.New("upstream-private-message")
				}
			case "invalid-source":
				f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
					return providers.Identity{Provider: "google", Subject: "google:123", Sources: map[string]json.RawMessage{"claims": json.RawMessage("invalid")}}, nil
				}
			case "invalid-identity":
				f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
					return providers.Identity{Provider: "github", Subject: "github:123", Sources: map[string]json.RawMessage{}}, nil
				}
			}
			response := f.call("GET", "/auth/callback/google?"+query.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
			if response.Code != 302 {
				t.Fatalf("callback = %d: %s", response.Code, response.Body)
			}
			location, _ := url.Parse(response.Header().Get("Location"))
			want := "provider_failed"
			if kind == "denied" {
				want = "provider_denied"
			}
			if location.Query().Get("error") != want || location.Query().Get("code") != "" || location.Query().Get("state") != "product-state" || strings.Contains(response.Header().Get("Location"), "upstream-private-message") {
				t.Fatalf("unsafe failure redirect: %s", location)
			}
			if len(f.h.cache.results) != 0 {
				t.Fatal("failed provider published an identity result")
			}
		})
	}
}

func TestLargeUnknownSourceAndCapacity(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.MaxResults = 1; c.MaxResultBytes = 2 << 20 })
	large := `{"unknown":"` + strings.Repeat("z", 1<<20) + `","null":null,"flag":false}`
	f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
		return providers.Identity{Provider: "google", Subject: "google:123", Sources: map[string]json.RawMessage{"profile": json.RawMessage(large)}}, nil
	}
	code := f.complete(f.start("first"))
	second := f.start("second")
	response := f.call("GET", "/auth/callback/google?"+url.Values{"state": {second.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{second.cookie}, nil)
	location, _ := url.Parse(response.Header().Get("Location"))
	if response.Code != 302 || location.Query().Get("error") != "capacity_exceeded" {
		t.Fatalf("capacity response: %d %s", response.Code, location)
	}
	response = f.retrieve(code, &f.app)
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(large)) {
		t.Fatal("large source was truncated or evicted")
	}
	f.h.config.MaxResultBytes = 10
	f.h.cache.config.MaxResultBytes = 10
	third := f.start("third")
	response = f.call("GET", "/auth/callback/google?"+url.Values{"state": {third.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{third.cookie}, nil)
	location, _ = url.Parse(response.Header().Get("Location"))
	if location.Query().Get("error") != "capacity_exceeded" {
		t.Fatal("byte capacity not enforced")
	}
}

func TestDisabledOrChangedAppCannotCompleteOrClaim(t *testing.T) {
	f := newFixture(t, nil)
	flow := f.start("product-state")
	input := apps.AppInput{Name: f.app.App.Name, Enabled: false, RedirectURIs: f.app.App.RedirectURIs, Providers: f.app.App.Providers}
	if _, err := f.store.Update(context.Background(), f.app.App.ID, input); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil), 410, "transaction_unavailable")
	if f.google.calls.Load() != 0 {
		t.Fatal("disabled app reached provider exchange")
	}
	input.Enabled = true
	if _, err := f.store.Update(context.Background(), f.app.App.ID, input); err != nil {
		t.Fatal(err)
	}
	code := f.complete(f.start("new-login"))
	input.Enabled = false
	if _, err := f.store.Update(context.Background(), f.app.App.ID, input); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.retrieve(code, &f.app), 401, "invalid_app")
	input.Enabled = true
	if _, err := f.store.Update(context.Background(), f.app.App.ID, input); err != nil {
		t.Fatal(err)
	}
	if f.retrieve(code, &f.app).Code != 200 {
		t.Fatal("disabled credential probe consumed result")
	}
	flow = f.start("callback-removed")
	input.RedirectURIs = []string{"https://product.example/new"}
	if _, err := f.store.Update(context.Background(), f.app.App.ID, input); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil), 410, "transaction_unavailable")
}

func TestShutdownCancelsProviderAndReleasesMemory(t *testing.T) {
	f := newFixture(t, nil)
	code := f.complete(f.start("cached-login"))
	started := make(chan struct{})
	f.google.exchange = func(ctx context.Context, _ providers.ExchangeRequest) (providers.Identity, error) {
		close(started)
		<-ctx.Done()
		return providers.Identity{}, ctx.Err()
	}
	flow := f.start("active-login")
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		completed <- f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
	}()
	<-started
	if err := f.h.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("provider exchange survived shutdown")
	}
	assertCode(t, f.retrieve(code, &f.app), 503, "service_unavailable")
	query := url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"new-login"}}
	assertCode(t, f.call("GET", "/auth/authorize?"+query.Encode(), nil, nil, nil), 503, "service_unavailable")
	assertCode(t, f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil), 503, "service_unavailable")
	if len(f.h.cache.results) != 0 || len(f.h.cache.transactions) != 0 {
		t.Fatal("shutdown retained identity state")
	}
}

func TestProviderCallbackTimeout(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.CallbackTimeout = 20 * time.Millisecond })
	f.google.exchange = func(ctx context.Context, _ providers.ExchangeRequest) (providers.Identity, error) {
		<-ctx.Done()
		return providers.Identity{}, ctx.Err()
	}
	flow := f.start("product-state")
	response := f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
	location, _ := url.Parse(response.Header().Get("Location"))
	if response.Code != 302 || location.Query().Get("error") != "provider_failed" {
		t.Fatal("callback did not surface provider deadline")
	}
}

func TestInvalidRequestsLeaveStateIntact(t *testing.T) {
	f := newFixture(t, nil)
	for _, path := range []string{
		"/auth/authorize",
		"/auth/authorize?state=x&state=y",
		"/auth/authorize?app_id=%zz",
		"/auth/authorize?" + url.Values{"app_id": {"missing"}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"state"}}.Encode(),
		"/auth/authorize?" + url.Values{"app_id": {f.app.App.ID}, "provider": {"unconfigured"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"state"}}.Encode(),
		"/auth/authorize?" + url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {"https://attacker.example/callback"}, "state": {"state"}}.Encode(),
	} {
		assertCode(t, f.call("GET", path, nil, nil, nil), 400, "invalid_request")
	}
	flow := f.start("product-state")
	for _, query := range []string{"state=%zz", "state=" + flow.state, "state=" + flow.state + "&code=a&code=b", "state=" + flow.state + "&code=a&error=denied"} {
		assertCode(t, f.call("GET", "/auth/callback/google?"+query, nil, []*http.Cookie{flow.cookie}, nil), 400, "invalid_request")
	}
	assertCode(t, f.call("GET", "/auth/callback/microsoft?state="+flow.state+"&code=code", nil, []*http.Cookie{flow.cookie}, nil), 410, "transaction_unavailable")
	code := f.complete(flow)
	assertCode(t, f.retrieve(code, nil), 401, "invalid_app")
	for _, body := range []string{"", "null", `{}`, `{"code":"` + code + `"} {}`, `{"code":"` + strings.Repeat("x", 3000) + `"}`} {
		assertCode(t, f.call("POST", "/auth/result", strings.NewReader(body), nil, &f.app), 400, "invalid_request")
	}
	if f.retrieve(code, &f.app).Code != 200 {
		t.Fatal("invalid requests consumed a legitimate result")
	}
}

func TestPeriodicCleanupAndTransactionByteCapacity(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.CleanupInterval = 5 * time.Millisecond })
	f.start("pending")
	f.complete(f.start("result"))
	f.clock.Add(int64(f.h.config.TransactionTTL))
	deadline := time.Now().Add(time.Second)
	for {
		f.h.cache.mu.Lock()
		empty := len(f.h.cache.transactions) == 0 && len(f.h.cache.results) == 0 && f.h.cache.transactionBytes == 0 && f.h.cache.resultBytes == 0
		f.h.cache.mu.Unlock()
		if empty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic cleanup retained expired records")
		}
		time.Sleep(time.Millisecond)
	}
	limited := newFixture(t, func(c *Config) { c.MaxTransactionBytes = 1 })
	query := url.Values{"app_id": {limited.app.App.ID}, "provider": {"google"}, "redirect_uri": {limited.app.App.RedirectURIs[0]}, "state": {"state"}}
	assertCode(t, limited.call("GET", "/auth/authorize?"+query.Encode(), nil, nil, nil), 503, "capacity_exceeded")
}

func TestAppChangedDuringProviderWork(t *testing.T) {
	f := newFixture(t, nil)
	f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
		input := apps.AppInput{Name: f.app.App.Name, Enabled: true, RedirectURIs: []string{"https://product.example/new"}, Providers: []string{"google"}}
		if _, err := f.store.Update(context.Background(), f.app.App.ID, input); err != nil {
			t.Error(err)
		}
		return providers.Identity{Provider: "google", Subject: "google:123", Sources: map[string]json.RawMessage{"claims": json.RawMessage(`{"sub":"123"}`)}}, nil
	}
	flow := f.start("product-state")
	assertCode(t, f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil), 410, "transaction_unavailable")
	f.h.cache.mu.Lock()
	defer f.h.cache.mu.Unlock()
	if len(f.h.cache.results) != 0 {
		t.Fatal("app changes during exchange published a result")
	}
}

func TestSanitizedOperationLogs(t *testing.T) {
	var logs bytes.Buffer
	f := newFixture(t, func(c *Config) { c.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	flow := f.start("PRIVATE_PRODUCT_STATE")
	code := f.complete(flow)
	if f.retrieve(code, &f.app).Code != 200 {
		t.Fatal("result not delivered")
	}
	assertCode(t, f.retrieve(code, &f.app), 410, "result_unavailable")
	for _, private := range []string{flow.productState, flow.state, flow.cookie.Value, code, f.app.Secret, "upstream-secret-code", "google:123", "unknown", "product.example"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("log retained private field %q", private)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	var operationID string
	seenCreate, seenConsume := false, false
	for decoder.More() {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["request_id"] == "" || entry["app_id"] != f.app.App.ID {
			t.Fatalf("missing request correlation: %v", entry)
		}
		if entry["phase"] == "result_create" {
			operationID, _ = entry["operation_id"].(string)
			seenCreate = operationID != ""
		}
		if entry["phase"] == "result_consume" {
			seenConsume = entry["operation_id"] == operationID
		}
		if entry["error_category"] == "result_unavailable" && entry["operation_id"] != "" {
			t.Fatal("unavailable request retained consumed operation history")
		}
	}
	if !seenCreate || !seenConsume {
		t.Fatal("result operation correlation missing")
	}
}

func TestConstructorAndDevelopmentCookies(t *testing.T) {
	f := newFixture(t, nil)
	valid := f.h.config
	cases := []Config{valid, valid, valid, valid, valid}
	cases[0].PublicURL = "http://auth.example"
	cases[1].PublicURL = "https://auth.example/path"
	cases[2].TransactionTTL = 0
	cases[3].MaxResultBytes = 0
	cases[4].PublicURL = "https://user:password@auth.example"
	for _, config := range cases {
		if handler, err := New(context.Background(), config, f.store, nil); err == nil {
			handler.Close()
			t.Fatal("invalid configuration accepted")
		}
	}
	if _, err := New(nil, valid, f.store, nil); err == nil {
		t.Fatal("nil lifecycle context accepted")
	}
	if _, err := New(context.Background(), valid, nil, nil); err == nil {
		t.Fatal("nil app store accepted")
	}
	if _, err := New(context.Background(), valid, f.store, map[string]providers.Provider{"github": f.google}); err == nil {
		t.Fatal("mismatched registry accepted")
	}
	valid.PublicURL = "http://127.0.0.1:8080"
	valid.AllowLoopbackHTTP = true
	valid.Logger = nil
	valid.Now = nil
	handler, err := New(context.Background(), valid, f.store, map[string]providers.Provider{"google": f.google})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest("GET", "/auth/authorize?"+url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"development"}}.Encode(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	cookies := response.Result().Cookies()
	if response.Code != 302 || len(cookies) != 1 || cookies[0].Secure || strings.HasPrefix(cookies[0].Name, "__Host-") {
		t.Fatal("explicit loopback development cookie is incorrect")
	}
}

func TestMissingAndUnavailableAppStorage(t *testing.T) {
	for _, mode := range []string{"deleted-before-callback", "deleted-during-exchange", "closed-during-exchange", "closed-before-start", "closed-before-retrieval"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, nil)
			switch mode {
			case "closed-before-start":
				if err := f.store.Close(); err != nil {
					t.Fatal(err)
				}
				query := url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"state"}}
				assertCode(t, f.call("GET", "/auth/authorize?"+query.Encode(), nil, nil, nil), 500, "internal_error")
				return
			case "closed-before-retrieval":
				code := f.complete(f.start("state"))
				if err := f.store.Close(); err != nil {
					t.Fatal(err)
				}
				assertCode(t, f.retrieve(code, &f.app), 500, "internal_error")
				return
			}
			flow := f.start("product-state")
			if mode == "deleted-before-callback" {
				if err := f.store.Delete(context.Background(), f.app.App.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
					var err error
					if mode == "deleted-during-exchange" {
						err = f.store.Delete(context.Background(), f.app.App.ID)
					} else {
						err = f.store.Close()
					}
					if err != nil {
						t.Error(err)
					}
					return providers.Identity{Provider: "google", Subject: "google:123", Sources: map[string]json.RawMessage{"claims": json.RawMessage(`{"sub":"123"}`)}}, nil
				}
			}
			response := f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"provider-code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
			if mode == "closed-during-exchange" {
				assertCode(t, response, 500, "internal_error")
			} else {
				assertCode(t, response, 410, "transaction_unavailable")
			}
			if response.Header().Get("Location") != "" {
				t.Fatal("unavailable application received a callback")
			}
			f.h.cache.mu.Lock()
			defer f.h.cache.mu.Unlock()
			if len(f.h.cache.results) != 0 {
				t.Fatal("unavailable application published identity")
			}
		})
	}
}

func TestAuthorizationProviderFailure(t *testing.T) {
	var logs bytes.Buffer
	f := newFixture(t, func(c *Config) { c.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	f.google.startError = &providers.Error{Operation: "exchange", Cause: errors.New("PRIVATE_CLIENT_SECRET")}
	query := url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"state"}}
	response := f.call("GET", "/auth/authorize?"+query.Encode(), nil, nil, nil)
	assertCode(t, response, 502, "provider_failed")
	if len(response.Result().Cookies()) != 0 || strings.Contains(response.Body.String()+logs.String(), "PRIVATE_CLIENT_SECRET") {
		t.Fatal("provider failure leaked credentials or created browser state")
	}
	if !strings.Contains(logs.String(), `"error_operation":"exchange"`) || !strings.Contains(logs.String(), `"error_cause":"operation_failed"`) {
		t.Fatal("provider failure omitted safe cause metadata")
	}
}

func TestResultContentTypeBeforeConsumption(t *testing.T) {
	f := newFixture(t, nil)
	code := f.complete(f.start("state"))
	for _, contentType := range []string{"", "text/plain", "application/json; charset", "application/x-www-form-urlencoded"} {
		request := httptest.NewRequest("POST", "/auth/result", strings.NewReader(`{"code":"`+code+`"}`))
		request.SetBasicAuth(f.app.App.ID, f.app.Secret)
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		f.mux.ServeHTTP(response, request)
		assertCode(t, response, 400, "invalid_request")
	}
	request := httptest.NewRequest("POST", "/auth/result", strings.NewReader(`{"code":"`+code+`"}`))
	request.SetBasicAuth(f.app.App.ID, f.app.Secret)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("correct JSON request could not claim result: %d", response.Code)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "PRIVATE_NETWORK_TARGET" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

func TestProviderCauseCategoriesAreSanitized(t *testing.T) {
	for _, item := range []struct {
		name                string
		err                 error
		category, operation string
	}{
		{"network", &providers.Error{Operation: "profile retrieval", Cause: timeoutError{}}, "network_timeout", "profile retrieval"},
		{"deadline", &providers.Error{Operation: "token exchange", Cause: context.DeadlineExceeded}, "deadline_exceeded", "token exchange"},
		{"canceled", context.Canceled, "canceled", "provider_exchange"},
		{"invalid-operation", &providers.Error{Operation: "PRIVATE_OPERATION", Cause: errors.New("PRIVATE_DETAIL")}, "operation_failed", "provider_exchange"},
	} {
		t.Run(item.name, func(t *testing.T) {
			var logs bytes.Buffer
			f := newFixture(t, func(c *Config) { c.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
			f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
				return providers.Identity{}, item.err
			}
			flow := f.start("state")
			response := f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
			location, _ := url.Parse(response.Header().Get("Location"))
			if response.Code != 302 || location.Query().Get("error") != "provider_failed" {
				t.Fatal("provider failure did not reach product")
			}
			if strings.Contains(logs.String()+response.Body.String(), "PRIVATE_") || !strings.Contains(logs.String(), `"error_cause":"`+item.category+`"`) || !strings.Contains(logs.String(), `"error_operation":"`+item.operation+`"`) {
				t.Fatalf("unsafe or incomplete cause metadata: %s", &logs)
			}
		})
	}
}

func TestLoopbackConfiguration(t *testing.T) {
	f := newFixture(t, nil)
	for _, origin := range []string{"http://localhost:8080", "http://[::1]:8080", "http://127.0.0.1:8080"} {
		config := f.h.config
		config.PublicURL = origin
		config.AllowLoopbackHTTP = true
		handler, err := New(context.Background(), config, f.store, nil)
		if err != nil {
			t.Fatalf("loopback origin rejected: %s: %v", origin, err)
		}
		handler.Close()
	}
	for _, origin := range []string{"http://192.0.2.1:8080", "http://auth.example", "https://%zz"} {
		config := f.h.config
		config.PublicURL = origin
		config.AllowLoopbackHTTP = true
		if handler, err := New(context.Background(), config, f.store, nil); err == nil {
			handler.Close()
			t.Fatalf("invalid origin accepted: %s", origin)
		}
	}
}

func TestShutdownAdmissionInterleavings(t *testing.T) {
	for _, phase := range []string{"authorize-insert", "callback-claim", "result-insert", "result-claim"} {
		t.Run(phase, func(t *testing.T) {
			var trigger atomic.Bool
			var handler *Handler
			f := newFixture(t, func(c *Config) {
				clock := c.Now
				c.Now = func() time.Time {
					now := clock()
					if trigger.CompareAndSwap(true, false) {
						handler.Close()
					}
					return now
				}
			})
			handler = f.h
			if phase == "authorize-insert" {
				f.google.startHook = func() { handler.Close() }
				query := url.Values{"app_id": {f.app.App.ID}, "provider": {"google"}, "redirect_uri": {f.app.App.RedirectURIs[0]}, "state": {"state"}}
				response := f.call("GET", "/auth/authorize?"+query.Encode(), nil, nil, nil)
				assertCode(t, response, 503, "service_unavailable")
				if len(response.Result().Cookies()) != 0 || response.Header().Get("Location") != "" {
					t.Fatal("shutdown admitted a browser transaction")
				}
				return
			}
			if phase == "result-claim" {
				code := f.complete(f.start("state"))
				trigger.Store(true)
				assertCode(t, f.retrieve(code, &f.app), 503, "service_unavailable")
				return
			}
			flow := f.start("state")
			if phase == "callback-claim" {
				trigger.Store(true)
			} else {
				f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) {
					trigger.Store(true)
					return providers.Identity{Provider: "google", Subject: "google:123", Sources: map[string]json.RawMessage{"claims": json.RawMessage(`{"sub":"123"}`)}}, nil
				}
			}
			response := f.call("GET", "/auth/callback/google?"+url.Values{"state": {flow.state}, "code": {"code"}}.Encode(), nil, []*http.Cookie{flow.cookie}, nil)
			assertCode(t, response, 503, "service_unavailable")
			if response.Header().Get("Location") != "" {
				t.Fatal("shutdown published a product callback")
			}
			f.h.cache.mu.Lock()
			defer f.h.cache.mu.Unlock()
			if len(f.h.cache.results) != 0 || f.h.cache.resultBytes != 0 || f.h.cache.transactionBytes != 0 || f.h.cache.activeCallbacks != 0 {
				t.Fatal("shutdown retained state after active work finished")
			}
		})
	}
}

type failedResponseWriter struct{ *httptest.ResponseRecorder }

func (w failedResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("PRIVATE_TRANSPORT_FAILURE")
}

func TestLostResponseConsumesResult(t *testing.T) {
	var logs bytes.Buffer
	f := newFixture(t, func(c *Config) { c.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	code := f.complete(f.start("state"))
	request := httptest.NewRequest("POST", "/auth/result", strings.NewReader(`{"code":"`+code+`"}`))
	request.SetBasicAuth(f.app.App.ID, f.app.Secret)
	request.Header.Set("Content-Type", "application/json")
	response := failedResponseWriter{httptest.NewRecorder()}
	f.mux.ServeHTTP(response, request)
	if response.Code != 200 || response.Body.Len() != 0 {
		t.Fatal("transport failure did not occur after accepting result")
	}
	assertCode(t, f.retrieve(code, &f.app), 410, "result_unavailable")
	if strings.Contains(logs.String(), "PRIVATE_TRANSPORT_FAILURE") || !strings.Contains(logs.String(), `"error_category":"response_write_failed"`) {
		t.Fatal("response loss telemetry is unsafe or absent")
	}
}
