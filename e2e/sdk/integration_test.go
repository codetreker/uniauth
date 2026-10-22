package sdkintegration

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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/auth"
	"github.com/codetreker/uniauth/internal/providers"
	uniauth "github.com/codetreker/uniauth/sdk/go"
)

const profileJSON = `{"id":9007199254740993,"unknown":{"nested":[null,false,{"text":"original"}]},"email":null,"verified":false}`

const productRedirect = "http://product.example/callback?existing=preserved"

type controlledProvider struct {
	mu       sync.Mutex
	requests map[string]providers.AuthorizationRequest
}

func (p *controlledProvider) Name() string { return "google" }

func (p *controlledProvider) AuthorizationURL(request providers.AuthorizationRequest) (string, error) {
	p.mu.Lock()
	p.requests[request.State] = request
	p.mu.Unlock()
	return "https://provider.example/authorize?" + url.Values{"state": {request.State}}.Encode(), nil
}

func (p *controlledProvider) Exchange(ctx context.Context, request providers.ExchangeRequest) (providers.Identity, error) {
	if err := ctx.Err(); err != nil {
		return providers.Identity{}, err
	}
	p.mu.Lock()
	authorization, found := p.requests[request.Code]
	delete(p.requests, request.Code)
	p.mu.Unlock()
	if !found || request.ExpectedNonce != authorization.Nonce || request.CodeVerifier != authorization.CodeVerifier {
		return providers.Identity{}, errors.New("callback did not retain its authorization nonce and verifier")
	}
	verified := false
	name := "Integration User"
	return providers.Identity{
		Provider: "google", ID: "integration-user", Email: nil, EmailVerified: &verified,
		Name: &name, AvatarURL: nil,
		Origin: map[string]json.RawMessage{
			"profile": json.RawMessage(profileJSON), "optional": json.RawMessage(`null`), "enabled": json.RawMessage(`false`),
		},
	}, nil
}

type gateway struct {
	store      *apps.Store
	ownerID    string
	server     *httptest.Server
	app, other apps.CreatedApp
	clock      atomic.Int64
}

func newGateway(t *testing.T) *gateway {
	t.Helper()
	if err := os.MkdirAll("../../.tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("../../.tmp", "sdk-integration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	store, err := apps.Open(t.Context(), apps.Config{Path: filepath.Join(directory, "apps.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := store.InitializePlatform(t.Context(), "admin@example.test"); err != nil {
		t.Fatal(err)
	}
	owner, err := store.ResolveAccount(t.Context(), providers.Identity{Provider: "google", ID: "sdk-fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	g := &gateway{store: store, ownerID: owner.ID}
	g.clock.Store(time.Now().UnixNano())
	for i, destination := range []*apps.CreatedApp{&g.app, &g.other} {
		*destination, err = store.Create(t.Context(), owner.ID, apps.CreateAppInput{AppInput: apps.AppInput{Name: fmt.Sprintf("Product %d", i), Enabled: true, RedirectURIs: []string{productRedirect}, Providers: []string{"google"}}, InitialSecret: &apps.SecretInput{Name: "SDK fixture"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	g.server = httptest.NewUnstartedServer(mux)
	handler, err := auth.New(t.Context(), auth.Config{
		PublicURL: "http://" + g.server.Listener.Addr().String(), AllowLoopbackHTTP: true,
		TransactionTTL: 10 * time.Minute, CallbackTimeout: time.Second, CleanupInterval: time.Hour,
		MaxTransactions: 32, MaxTransactionBytes: 1 << 20, MaxResults: 32, MaxResultBytes: 1 << 20,
		Now: func() time.Time { return time.Unix(0, g.clock.Load()) }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, store, map[string]providers.Provider{"google": &controlledProvider{requests: make(map[string]providers.AuthorizationRequest)}})
	if err != nil {
		g.server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := handler.Close(); err != nil {
			t.Error(err)
		}
	})
	handler.Register(mux)
	g.server.Start()
	t.Cleanup(g.server.Close)
	return g
}

func (g *gateway) client(t *testing.T, app apps.CreatedApp) *uniauth.Client {
	t.Helper()
	client, err := uniauth.New(uniauth.Config{BaseURL: g.server.URL, AppID: app.App.ID, AppSecret: app.Secret, AllowLoopbackHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func (g *gateway) login(t *testing.T, client *uniauth.Client) string {
	t.Helper()
	state := uniauth.GenerateState()
	authorization, err := client.AuthorizationURL(uniauth.AuthorizationRequest{Provider: uniauth.Google, RedirectURI: productRedirect, State: state})
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := browser.Get(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusFound {
		t.Fatalf("authorize: HTTP %d", response.StatusCode)
	}
	upstream, err := response.Location()
	if err != nil {
		t.Fatal(err)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || upstream.Query().Get("state") == state {
		t.Fatal("authorization did not create a separate browser-bound upstream flow")
	}
	upstreamState := upstream.Query().Get("state")
	callback, err := http.NewRequestWithContext(t.Context(), http.MethodGet, g.server.URL+"/auth/callback/google?"+url.Values{"state": {upstreamState}, "code": {upstreamState}}.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	callback.AddCookie(cookies[0])
	response, err = browser.Do(callback)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusFound {
		t.Fatalf("callback: HTTP %d", response.StatusCode)
	}
	product, err := response.Location()
	if err != nil {
		t.Fatal(err)
	}
	if product.Scheme != "http" || product.Host != "product.example" || product.Query().Get("existing") != "preserved" {
		t.Fatalf("product redirect changed: %s", product)
	}
	if _, err := uniauth.ParseCallback(product.Query(), "incorrect-state"); err == nil {
		t.Fatal("callback accepted a mismatched product state")
	}
	code, err := uniauth.ParseCallback(product.Query(), state)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func assertAPIError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var failure *uniauth.APIError
	if !errors.As(err, &failure) || failure.StatusCode != status || failure.Code != code || failure.RequestID == "" {
		t.Fatalf("retrieval error = %v, want HTTP %d %s with request ID", err, status, code)
	}
}

func assertIdentity(t *testing.T, identity *uniauth.Identity) {
	t.Helper()
	if identity.Provider != uniauth.Google || identity.ID != "integration-user" || identity.Email != nil || identity.EmailVerified == nil || *identity.EmailVerified || identity.Name == nil || *identity.Name != "Integration User" || identity.AvatarURL != nil {
		t.Fatalf("normalized identity changed: %#v", identity)
	}
	if len(identity.Origin) != 3 || string(identity.Origin["profile"]) != profileJSON || string(identity.Origin["optional"]) != "null" || string(identity.Origin["enabled"]) != "false" {
		t.Fatalf("origin JSON changed: %#v", identity.Origin)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(identity.RawJSON, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 7 {
		t.Fatalf("identity envelope has %d fields, want seven: %s", len(wire), identity.RawJSON)
	}
	for field, expected := range map[string]string{
		"provider": `"google"`, "id": `"integration-user"`, "email": `null`,
		"email_verified": `false`, "name": `"Integration User"`, "avatar_url": `null`,
	} {
		if string(wire[field]) != expected {
			t.Fatalf("raw identity %s = %s, want %s", field, wire[field], expected)
		}
	}
	var origin map[string]json.RawMessage
	if err := json.Unmarshal(wire["origin"], &origin); err != nil || len(origin) != len(identity.Origin) {
		t.Fatalf("complete origin JSON changed: %s", wire["origin"])
	}
	for name, source := range identity.Origin {
		if !bytes.Equal(origin[name], source) {
			t.Fatalf("raw origin %s differs from decoded identity", name)
		}
	}
}

func TestGoSDKAgainstGateway(t *testing.T) {
	g := newGateway(t)
	client := g.client(t, g.app)
	code := g.login(t, client)
	incorrect := g.app
	incorrect.Secret = "incorrect-secret"
	_, err := g.client(t, incorrect).Retrieve(t.Context(), code)
	assertAPIError(t, err, http.StatusUnauthorized, "invalid_app")
	_, err = g.client(t, g.other).Retrieve(t.Context(), code)
	assertAPIError(t, err, http.StatusGone, "result_unavailable")
	identity, err := client.Retrieve(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	assertIdentity(t, identity)
	_, err = client.Retrieve(t.Context(), code)
	assertAPIError(t, err, http.StatusGone, "result_unavailable")

	code = g.login(t, client)
	type claim struct {
		identity *uniauth.Identity
		err      error
	}
	claims := make(chan claim, 12)
	for range cap(claims) {
		go func() {
			identity, err := client.Retrieve(t.Context(), code)
			claims <- claim{identity, err}
		}()
	}
	winners := 0
	for range cap(claims) {
		result := <-claims
		if result.err != nil {
			assertAPIError(t, result.err, http.StatusGone, "result_unavailable")
			continue
		}
		winners++
		assertIdentity(t, result.identity)
	}
	if winners != 1 {
		t.Fatalf("concurrent retrievals consumed the identity %d times", winners)
	}

	code = g.login(t, client)
	g.clock.Add(int64(auth.ResultTTL))
	_, err = client.Retrieve(t.Context(), code)
	assertAPIError(t, err, http.StatusGone, "result_unavailable")
}

func TestJavaScriptSDKAgainstGateway(t *testing.T) {
	g := newGateway(t)
	payload, err := json.Marshal(struct {
		BaseURL     string          `json:"baseURL"`
		App         apps.CreatedApp `json:"app"`
		Other       apps.CreatedApp `json:"other"`
		RedirectURI string          `json:"redirectURI"`
		ProfileJSON string          `json:"profileJSON"`
	}{g.server.URL, g.app, g.other, productRedirect, profileJSON})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "integration.mjs")
	command.Stdin = bytes.NewReader(payload)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		t.Fatalf("JavaScript gateway integration: %v", err)
	}
}
