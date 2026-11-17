package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

const testOrgOne = "11111111-2222-3333-4444-555555555555"
const testOrgTwo = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const testKeyTemplate = "https://login.microsoftonline.com/{tenantid}/v2.0"

func testMicrosoftIssuer(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/v2.0"
}

func testMicrosoftClaims(tenant string) map[string]any {
	return map[string]any{"iss": testMicrosoftIssuer(tenant), "tid": tenant, "oid": "same-object", "aud": "client-id", "sub": "subject", "nonce": "nonce", "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()}
}

func testMicrosoftToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: kid}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func testMicrosoftJWK(t *testing.T, key *rsa.PrivateKey, kid, issuer string) map[string]any {
	t.Helper()
	data, err := json.Marshal(jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"})
	if err != nil {
		t.Fatal(err)
	}
	var item map[string]any
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatal(err)
	}
	if issuer != "" {
		item["issuer"] = issuer
	}
	return item
}

type microsoftTestJWKS struct {
	mu      sync.Mutex
	body    string
	count   int
	started chan struct{}
	release chan struct{}
}

func (s *microsoftTestJWKS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.count++
	body, started, release := s.body, s.started, s.release
	s.mu.Unlock()
	if r.Method != "GET" || r.URL.Path != "/jwks" || r.Header.Get("Authorization") != "" {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}
func (s *microsoftTestJWKS) set(t *testing.T, keys ...map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.body = string(data)
	s.mu.Unlock()
}
func (s *microsoftTestJWKS) requests() int { s.mu.Lock(); defer s.mu.Unlock(); return s.count }
func testMicrosoftVerifier(t *testing.T) (*microsoftTestJWKS, *microsoftVerifier) {
	t.Helper()
	fixture := &microsoftTestJWKS{}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 2 * time.Second
	keys := &microsoftKeySet{client: boundedClient(client), url: server.URL + "/jwks"}
	return fixture, &microsoftVerifier{keys: keys, clientID: "client-id"}
}

func TestMicrosoftTenantAndKeyIssuerMatrix(t *testing.T) {
	key := signingKey(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, tenant string
		mutate       func(map[string]any)
		keys         []map[string]any
		valid        bool
		noFetch      bool
	}{
		{"personal template", microsoftTenant, nil, []map[string]any{testMicrosoftJWK(t, key, "key", testKeyTemplate)}, true, false},
		{"organization template", testOrgOne, nil, []map[string]any{testMicrosoftJWK(t, key, "key", testKeyTemplate)}, true, false},
		{"organization concrete", testOrgTwo, nil, []map[string]any{testMicrosoftJWK(t, key, "key", testMicrosoftIssuer(testOrgTwo))}, true, false},
		{"missing tenant", testOrgOne, func(c map[string]any) { delete(c, "tid") }, nil, false, true},
		{"wrong tenant type", testOrgOne, func(c map[string]any) { c["tid"] = 42 }, nil, false, true},
		{"invalid tenant", testOrgOne, func(c map[string]any) { c["tid"] = "organization" }, nil, false, true},
		{"wrong tenant issuer", testOrgOne, func(c map[string]any) { c["iss"] = testMicrosoftIssuer(testOrgTwo) }, nil, false, true},
		{"wrong host", testOrgOne, func(c map[string]any) { c["iss"] = "https://evil.example/" + testOrgOne + "/v2.0" }, nil, false, true},
		{"national cloud", testOrgOne, func(c map[string]any) { c["iss"] = "https://login.microsoftonline.us/" + testOrgOne + "/v2.0" }, nil, false, true},
		{"wrong version", testOrgOne, func(c map[string]any) { c["iss"] = "https://login.microsoftonline.com/" + testOrgOne + "/v1.0" }, nil, false, true},
		{"query", testOrgOne, func(c map[string]any) { c["iss"] = testMicrosoftIssuer(testOrgOne) + "?a=b" }, nil, false, true},
		{"suffix", testOrgOne, func(c map[string]any) { c["iss"] = testMicrosoftIssuer(testOrgOne) + "/" }, nil, false, true},
		{"missing key issuer", testOrgOne, nil, []map[string]any{testMicrosoftJWK(t, key, "key", "")}, false, false},
		{"consumer restricted", testOrgOne, nil, []map[string]any{testMicrosoftJWK(t, key, "key", testMicrosoftIssuer(microsoftTenant))}, false, false},
		{"other tenant restricted", testOrgOne, nil, []map[string]any{testMicrosoftJWK(t, key, "key", testMicrosoftIssuer(testOrgTwo))}, false, false},
		{"duplicate kid invalid", testOrgOne, nil, []map[string]any{testMicrosoftJWK(t, key, "key", testMicrosoftIssuer(microsoftTenant)), testMicrosoftJWK(t, other, "key", testKeyTemplate)}, false, false},
		{"duplicate kid valid", testOrgOne, nil, []map[string]any{testMicrosoftJWK(t, other, "key", testMicrosoftIssuer(microsoftTenant)), testMicrosoftJWK(t, key, "key", testKeyTemplate)}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, verifier := testMicrosoftVerifier(t)
			keys := tc.keys
			if keys == nil {
				keys = []map[string]any{testMicrosoftJWK(t, key, "key", testKeyTemplate)}
			}
			fixture.set(t, keys...)
			claims := testMicrosoftClaims(tc.tenant)
			if tc.mutate != nil {
				tc.mutate(claims)
			}
			_, err := verifier.Verify(context.Background(), testMicrosoftToken(t, key, "key", claims))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.noFetch && fixture.requests() != 0 {
				t.Fatal("invalid claims caused JWKS request")
			}
		})
	}
}

func TestMicrosoftKeyCacheRotationAndFailurePreserveSnapshot(t *testing.T) {
	key := signingKey(t)
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture, verifier := testMicrosoftVerifier(t)
	fixture.set(t, testMicrosoftJWK(t, key, "first", testKeyTemplate))
	first := testMicrosoftToken(t, key, "first", testMicrosoftClaims(testOrgOne))
	second := testMicrosoftToken(t, rotated, "second", testMicrosoftClaims(testOrgOne))
	verify := func(token string) error { _, err := verifier.Verify(context.Background(), token); return err }
	if err := verify(first); err != nil {
		t.Fatal(err)
	}
	if err := verify(first); err != nil {
		t.Fatal(err)
	}
	if fixture.requests() != 1 {
		t.Fatal("cache hit fetched keys")
	}
	fixture.mu.Lock()
	fixture.body = "{"
	fixture.mu.Unlock()
	err = verify(second)
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("decode cause lost: %v", err)
	}
	if err := verify(first); err != nil {
		t.Fatalf("old snapshot discarded: %v", err)
	}
	fixture.set(t, testMicrosoftJWK(t, rotated, "second", testKeyTemplate))
	if err := verify(second); err != nil {
		t.Fatal(err)
	}
	if fixture.requests() != 3 {
		t.Fatalf("unexpected fetch count %d", fixture.requests())
	}
}

func TestMicrosoftRefreshWaiterCancellation(t *testing.T) {
	key := signingKey(t)
	fixture, verifier := testMicrosoftVerifier(t)
	fixture.set(t, testMicrosoftJWK(t, key, "key", testKeyTemplate))
	fixture.mu.Lock()
	fixture.started = make(chan struct{}, 1)
	fixture.release = make(chan struct{})
	started, release := fixture.started, fixture.release
	fixture.mu.Unlock()
	token := testMicrosoftToken(t, key, "key", testMicrosoftClaims(testOrgOne))
	leader := make(chan error, 1)
	go func() { _, err := verifier.Verify(context.Background(), token); leader <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader did not start")
	}
	base, cancel := context.WithCancel(context.Background())
	ctx := &microsoftWaitingContext{Context: base, waiting: make(chan struct{})}
	waiter := make(chan error, 1)
	go func() { _, err := verifier.Verify(ctx, token); waiter <- err }()
	select {
	case <-ctx.waiting:
	case <-time.After(time.Second):
		t.Fatal("waiter did not join the flight")
	}
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	close(release)
	select {
	case err := <-leader:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not finish")
	}
	if fixture.requests() != 1 {
		t.Fatalf("unexpected fetches: %d", fixture.requests())
	}
}

func TestMicrosoftVerifierPreservesTransportCause(t *testing.T) {
	cause := errors.New("test transport failure")
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })}
	verifier := &microsoftVerifier{keys: &microsoftKeySet{client: boundedClient(client), url: "https://login.microsoftonline.com/common/discovery/v2.0/keys"}, clientID: "client-id"}
	_, err := verifier.Verify(context.Background(), testMicrosoftToken(t, signingKey(t), "key", testMicrosoftClaims(testOrgOne)))
	var failure *microsoftVerifyError
	if !errors.Is(err, cause) || !errors.As(err, &failure) || failure.category != "key_fetch" {
		t.Fatalf("transport failure lost: %v", err)
	}
}

func TestMicrosoftCanceledLeaderAllowsLiveFollower(t *testing.T) {
	key := signingKey(t)
	fixture, verifier := testMicrosoftVerifier(t)
	fixture.set(t, testMicrosoftJWK(t, key, "key", testKeyTemplate))
	fixture.mu.Lock()
	fixture.started = make(chan struct{}, 2)
	fixture.release = make(chan struct{})
	started, release := fixture.started, fixture.release
	fixture.mu.Unlock()
	token := testMicrosoftToken(t, key, "key", testMicrosoftClaims(testOrgOne))
	ctx, cancel := context.WithCancel(context.Background())
	leader := make(chan error, 1)
	go func() { _, err := verifier.Verify(ctx, token); leader <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader did not start")
	}
	followerCtx := &microsoftWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
	follower := make(chan error, 1)
	go func() { _, err := verifier.Verify(followerCtx, token); follower <- err }()
	select {
	case <-followerCtx.waiting:
	case <-time.After(time.Second):
		t.Fatal("follower did not join the flight")
	}
	cancel()
	select {
	case err := <-leader:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled leader did not finish")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("follower did not replace cancelled leader")
	}
	close(release)
	select {
	case err := <-follower:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("follower did not finish")
	}
	if fixture.requests() != 2 {
		t.Fatalf("unexpected fetches: %d", fixture.requests())
	}
}

func TestMicrosoftAdapterTenantIdentityAndMandatoryFailure(t *testing.T) {
	key := signingKey(t)
	keys := &microsoftTestJWKS{}
	keys.set(t, testMicrosoftJWK(t, key, "key", testKeyTemplate))
	var mu sync.Mutex
	var token string
	graphCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			keys.ServeHTTP(w, r)
		case "/token":
			mu.Lock()
			value := token
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"access_token": "secret", "token_type": "Bearer", "id_token": value})
		case "/profile", "/photo":
			mu.Lock()
			graphCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 2 * time.Second
	config := Config{ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://gateway.example/callback", HTTPClient: client}
	provider, err := newMicrosoft(context.Background(), config, server.URL+"/jwks", oauth2.Endpoint{TokenURL: server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}, server.URL+"/profile", server.URL+"/photo")
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{microsoftTenant, testOrgOne, testOrgTwo} {
		claims := testMicrosoftClaims(tenant)
		claims["nonce"] = "expected-nonce"
		claims["unknown"] = map[string]any{"value": false}
		claims["nullable"] = nil
		mu.Lock()
		token = testMicrosoftToken(t, key, "key", claims)
		mu.Unlock()
		identity, err := provider.Exchange(context.Background(), exchangeRequest())
		if err != nil {
			t.Fatalf("tenant %s: %v", tenant, err)
		}
		if identity.Provider != "microsoft" || identity.ID != "msft:same-object@"+tenant || identity.Email != nil || identity.EmailVerified != nil || identity.Name != nil || identity.AvatarURL != nil {
			t.Fatalf("tenant %s identity: %+v", tenant, identity)
		}
		var origin map[string]json.RawMessage
		if err := json.Unmarshal(identity.Origin["id_token_claims"], &origin); err != nil || string(origin["unknown"]) != `{"value":false}` || string(origin["nullable"]) != "null" {
			t.Fatalf("claims not preserved: %s (%v)", identity.Origin["id_token_claims"], err)
		}
	}
	mu.Lock()
	before := graphCalls
	claims := testMicrosoftClaims(testOrgOne)
	claims["nonce"] = "wrong"
	token = testMicrosoftToken(t, key, "key", claims)
	mu.Unlock()
	if _, err := provider.Exchange(context.Background(), exchangeRequest()); err == nil {
		t.Fatal("invalid nonce accepted")
	}
	mu.Lock()
	after := graphCalls
	mu.Unlock()
	if before != after {
		t.Fatal("enrichment followed mandatory verification failure")
	}
}

func TestMicrosoftRefreshFailureClassesAndRedirect(t *testing.T) {
	key := signingKey(t)
	token := testMicrosoftToken(t, key, "key", testMicrosoftClaims(testOrgOne))
	cases := []struct {
		name    string
		handler http.HandlerFunc
		check   func(error) bool
	}{
		{"status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, func(err error) bool {
			var status statusError
			return errors.As(err, &status) && status.status == http.StatusServiceUnavailable
		}},
		{"oversize", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(maxResponseBytes+1))
			w.WriteHeader(http.StatusOK)
		}, func(err error) bool { return errors.Is(err, errResponseTooLarge) }},
		{"empty keys", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"keys":[]}`) }, func(err error) bool { return err != nil }},
		{"wrong key algorithm", func(w http.ResponseWriter, _ *http.Request) {
			jwk := testMicrosoftJWK(t, key, "key", testKeyTemplate)
			jwk["alg"] = "RS512"
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwk}})
		}, func(err error) bool { return err != nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			client := server.Client()
			client.Timeout = time.Second
			verifier := &microsoftVerifier{keys: &microsoftKeySet{client: boundedClient(client), url: server.URL}, clientID: "client-id"}
			_, err := verifier.Verify(context.Background(), token)
			var typed *microsoftVerifyError
			if !tc.check(err) || !errors.As(err, &typed) || typed.category != "key_fetch" {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	reached := false
	sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer sink.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }))
	defer source.Close()
	client := source.Client()
	client.Timeout = time.Second
	verifier := &microsoftVerifier{keys: &microsoftKeySet{client: boundedClient(client), url: source.URL}, clientID: "client-id"}
	_, err := verifier.Verify(context.Background(), token)
	var status statusError
	if !errors.As(err, &status) || status.status != http.StatusFound || reached {
		t.Fatalf("redirect not rejected: err=%v reached=%v", err, reached)
	}
}

func TestMicrosoftRefreshMismatchChecksOnlyOnce(t *testing.T) {
	key := signingKey(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture, verifier := testMicrosoftVerifier(t)
	fixture.set(t, testMicrosoftJWK(t, other, "key", testKeyTemplate))
	_, err = verifier.Verify(context.Background(), testMicrosoftToken(t, key, "key", testMicrosoftClaims(testOrgOne)))
	if err == nil || fixture.requests() != 1 {
		t.Fatalf("mismatch should fail after one fetch: err=%v requests=%d", err, fixture.requests())
	}
}

type microsoftWaitingContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *microsoftWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestMicrosoftConcurrentRefreshSharesResult(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint("timeout=", timeout), func(t *testing.T) {
			key := signingKey(t)
			fixture, verifier := testMicrosoftVerifier(t)
			fixture.set(t, testMicrosoftJWK(t, key, "key", testKeyTemplate))
			fixture.mu.Lock()
			fixture.started = make(chan struct{}, 1)
			fixture.release = make(chan struct{})
			started, release := fixture.started, fixture.release
			fixture.mu.Unlock()
			if timeout {
				verifier.keys.client.Timeout = 300 * time.Millisecond
			}
			token := testMicrosoftToken(t, key, "key", testMicrosoftClaims(testOrgOne))
			leader := make(chan error, 1)
			go func() { _, err := verifier.Verify(context.Background(), token); leader <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("leader did not start")
			}
			ctx := &microsoftWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
			follower := make(chan error, 1)
			go func() { _, err := verifier.Verify(ctx, token); follower <- err }()
			select {
			case <-ctx.waiting:
			case <-time.After(time.Second):
				t.Fatal("follower did not wait for flight")
			}
			if !timeout {
				close(release)
			}
			var first, second error
			select {
			case first = <-leader:
			case <-time.After(time.Second):
				t.Fatal("leader did not finish")
			}
			select {
			case second = <-follower:
			case <-time.After(time.Second):
				t.Fatal("follower did not finish")
			}
			if timeout {
				close(release)
				if !errors.Is(first, context.DeadlineExceeded) || !errors.Is(second, context.DeadlineExceeded) {
					t.Fatalf("timeout not shared: leader=%v follower=%v", first, second)
				}
			} else if first != nil || second != nil {
				t.Fatalf("refresh failed: leader=%v follower=%v", first, second)
			}
			if fixture.requests() != 1 {
				t.Fatalf("expected one fetch, got %d", fixture.requests())
			}
		})
	}
}

type microsoftGenerationContext struct {
	context.Context
	mu      sync.Mutex
	calls   int
	waiting chan struct{}
	release chan struct{}
}

func (c *microsoftGenerationContext) Err() error {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == 2 {
		close(c.waiting)
		<-c.release
	}
	return c.Context.Err()
}

func TestMicrosoftNewGenerationAvoidsRedundantFetch(t *testing.T) {
	old := signingKey(t)
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture, verifier := testMicrosoftVerifier(t)
	fixture.set(t, testMicrosoftJWK(t, old, "old", testKeyTemplate))
	oldToken := testMicrosoftToken(t, old, "old", testMicrosoftClaims(testOrgOne))
	token := testMicrosoftToken(t, rotated, "new", testMicrosoftClaims(testOrgOne))
	if _, err := verifier.keys.VerifySignature(context.Background(), oldToken); err != nil {
		t.Fatal(err)
	}
	fixture.set(t, testMicrosoftJWK(t, rotated, "new", testKeyTemplate))
	ctx := &microsoftGenerationContext{Context: context.Background(), waiting: make(chan struct{}), release: make(chan struct{})}
	first := make(chan error, 1)
	go func() { _, err := verifier.keys.VerifySignature(ctx, token); first <- err }()
	select {
	case <-ctx.waiting:
	case <-time.After(time.Second):
		t.Fatal("cached check did not reach election")
	}
	if _, err := verifier.keys.VerifySignature(context.Background(), token); err != nil {
		close(ctx.release)
		t.Fatal(err)
	}
	close(ctx.release)
	select {
	case err := <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stale caller did not finish")
	}
	if fixture.requests() != 2 {
		t.Fatalf("stale caller fetched again: %d", fixture.requests())
	}
}

type microsoftCancelReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (r *microsoftCancelReader) Read(p []byte) (int, error) { r.cancel(); return r.reader.Read(p) }
func (*microsoftCancelReader) Close() error                 { return nil }

func TestMicrosoftSuccessfulFetchPublishedDespiteLeaderCancellation(t *testing.T) {
	key := signingKey(t)
	jwk := testMicrosoftJWK(t, key, "key", testKeyTemplate)
	data, err := json.Marshal(map[string]any{"keys": []any{jwk}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &microsoftCancelReader{reader: bytes.NewReader(data), cancel: cancel}, Request: r}, nil
	})}
	verifier := &microsoftVerifier{keys: &microsoftKeySet{client: boundedClient(client), url: "https://login.microsoftonline.com/common/discovery/v2.0/keys"}, clientID: "client-id"}
	token := testMicrosoftToken(t, key, "key", testMicrosoftClaims(testOrgOne))
	if _, err := verifier.Verify(ctx, token); !errors.Is(err, context.Canceled) {
		t.Fatalf("leader cancellation lost: %v", err)
	}
	if _, err := verifier.Verify(context.Background(), token); err != nil {
		t.Fatalf("completed snapshot unavailable: %v", err)
	}
	if calls != 1 {
		t.Fatalf("completed snapshot was not published: %d", calls)
	}
}

func TestMicrosoftEmptyRefreshPreservesPreviousSnapshot(t *testing.T) {
	key := signingKey(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture, verifier := testMicrosoftVerifier(t)
	fixture.set(t, testMicrosoftJWK(t, key, "old", testKeyTemplate))
	oldToken := testMicrosoftToken(t, key, "old", testMicrosoftClaims(testOrgOne))
	newToken := testMicrosoftToken(t, other, "new", testMicrosoftClaims(testOrgOne))
	if _, err := verifier.Verify(context.Background(), oldToken); err != nil {
		t.Fatal(err)
	}
	fixture.set(t)
	if _, err := verifier.Verify(context.Background(), newToken); err == nil {
		t.Fatal("empty refresh accepted")
	}
	if _, err := verifier.Verify(context.Background(), oldToken); err != nil {
		t.Fatalf("previous snapshot lost: %v", err)
	}
	if fixture.requests() != 2 {
		t.Fatalf("previous cache hit fetched: %d", fixture.requests())
	}
}

func TestMicrosoftExchangeRetainsVerificationTransportCause(t *testing.T) {
	cause := errors.New("JWKS test transport failure")
	claims := testMicrosoftClaims(testOrgOne)
	claims["nonce"] = "expected-nonce"
	token := testMicrosoftToken(t, signingKey(t), "key", claims)
	graphCalls := 0
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/token":
			body, _ := json.Marshal(map[string]any{"access_token": "secret", "token_type": "Bearer", "id_token": token})
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
		case "/jwks":
			return nil, cause
		default:
			graphCalls++
			return nil, errors.New("unexpected profile request")
		}
	})}
	config := Config{ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://gateway.example/callback", HTTPClient: client}
	provider, err := newMicrosoft(context.Background(), config, "https://example.test/jwks", oauth2.Endpoint{TokenURL: "https://example.test/token", AuthStyle: oauth2.AuthStyleInParams}, "https://example.test/profile", "https://example.test/photo")
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Exchange(context.Background(), exchangeRequest())
	var typed *microsoftVerifyError
	var providerErr *Error
	if !errors.Is(err, cause) || !errors.As(err, &typed) || typed.category != "key_fetch" || !errors.As(err, &providerErr) || providerErr.Operation != "identity verification" || graphCalls != 0 {
		t.Fatalf("exchange cause or stage lost: err=%v graph=%d", err, graphCalls)
	}
}

func TestMicrosoftConcurrentVerificationCategoriesStayCallLocal(t *testing.T) {
	cause := errors.New("key transport failure")
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })}
	verifier := &microsoftVerifier{keys: &microsoftKeySet{client: boundedClient(client), url: "https://example.test/jwks"}, clientID: "client-id"}
	valid := testMicrosoftClaims(testOrgOne)
	invalid := testMicrosoftClaims(testOrgOne)
	invalid["tid"] = 42
	tokens := []string{testMicrosoftToken(t, signingKey(t), "key", valid), testMicrosoftToken(t, signingKey(t), "key", invalid)}
	type result struct {
		err      error
		category any
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, token := range tokens {
		go func(token string) {
			stage := &diagnosticStage{fields: make(map[string]any)}
			ctx := context.WithValue(context.Background(), stageKey{}, stage)
			<-start
			_, err := verifier.Verify(ctx, token)
			results <- result{err: err, category: stage.fields["verification_category"]}
		}(token)
	}
	close(start)
	seen := map[any]error{}
	for range tokens {
		select {
		case result := <-results:
			seen[result.category] = result.err
		case <-time.After(time.Second):
			t.Fatal("verification did not finish")
		}
	}
	if !errors.Is(seen["key_fetch"], cause) || seen["tenant_claim"] == nil || errors.Is(seen["tenant_claim"], cause) || len(seen) != 2 {
		t.Fatalf("mixed call-local categories or causes: %v", seen)
	}
}

func TestMicrosoftExchangeRetainsDecodeAndCancellationCauses(t *testing.T) {
	cause := errors.New("independent read failure")
	claims := testMicrosoftClaims(testOrgOne)
	claims["nonce"] = "expected-nonce"
	token := testMicrosoftToken(t, signingKey(t), "key", claims)
	for _, tc := range []struct {
		name   string
		body   string
		cancel bool
		check  func(error) bool
	}{
		{"malformed envelope", "{", false, func(err error) bool { var target *json.SyntaxError; return errors.As(err, &target) }},
		{"malformed key metadata", `{"keys":[{"kty":"RSA","n":"AQAB","e":"AQAB","issuer":42}]}`, false, func(err error) bool { var target *json.UnmarshalTypeError; return errors.As(err, &target) }},
		{"cancelled fetch", "", true, func(err error) bool { return errors.Is(err, context.Canceled) && errors.Is(err, cause) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			graphCalls := 0
			client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/token":
					body, _ := json.Marshal(map[string]any{"access_token": "secret", "token_type": "Bearer", "id_token": token})
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
				case "/jwks":
					if tc.cancel {
						cancel()
						return nil, cause
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(tc.body)), Request: r}, nil
				default:
					graphCalls++
					return nil, errors.New("unexpected profile request")
				}
			})}
			config := Config{ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://gateway.example/callback", HTTPClient: client}
			provider, err := newMicrosoft(context.Background(), config, "https://example.test/jwks", oauth2.Endpoint{TokenURL: "https://example.test/token", AuthStyle: oauth2.AuthStyleInParams}, "https://example.test/profile", "https://example.test/photo")
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Exchange(ctx, exchangeRequest())
			var typed *microsoftVerifyError
			if !tc.check(err) || !errors.As(err, &typed) || typed.category != "key_fetch" || graphCalls != 0 {
				t.Fatalf("exchange cause or stage lost: err=%v graph=%d", err, graphCalls)
			}
		})
	}
}

func TestMicrosoftConcurrentExchangeFailuresStayCallLocal(t *testing.T) {
	cause := errors.New("JWKS connection failed")
	valid := testMicrosoftClaims(testOrgOne)
	valid["nonce"] = "expected-nonce"
	valid["email"] = "sensitive@example.test"
	invalid := testMicrosoftClaims(testOrgOne)
	invalid["tid"] = 42
	invalid["nonce"] = "expected-nonce"
	tokens := map[string]string{
		"valid-code":   testMicrosoftToken(t, signingKey(t), "key", valid),
		"invalid-code": testMicrosoftToken(t, signingKey(t), "key", invalid),
	}
	started, release := make(chan struct{}), make(chan struct{})
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				return nil, err
			}
			body, _ := json.Marshal(map[string]any{"access_token": "secret", "token_type": "Bearer", "id_token": tokens[r.Form.Get("code")]})
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
		case "/jwks":
			close(started)
			<-release
			return nil, cause
		default:
			return nil, errors.New("unexpected profile request")
		}
	})}
	config := Config{ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://gateway.example/callback", HTTPClient: client}
	provider, err := newMicrosoft(context.Background(), config, "https://example.test/jwks", oauth2.Endpoint{TokenURL: "https://example.test/token", AuthStyle: oauth2.AuthStyleInParams}, "https://example.test/profile", "https://example.test/photo")
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		err  error
		logs *bytes.Buffer
	}
	run := func(code string, results chan<- result) {
		logs := new(bytes.Buffer)
		logger := slog.New(slog.NewJSONHandler(logs, nil)).With("request_id", "request-one", "operation_id", "operation-one", "app_id", "app-one", "provider", "microsoft")
		ctx := WithLogger(context.Background(), logger)
		request := exchangeRequest()
		request.Code = code
		_, err := provider.Exchange(ctx, request)
		results <- result{err, logs}
	}
	first := make(chan result, 1)
	second := make(chan result, 1)
	go run("valid-code", first)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("JWKS request did not start")
	}
	go run("invalid-code", second)
	var invalidResult result
	select {
	case invalidResult = <-second:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("invalid request did not finish")
	}
	close(release)
	var validResult result
	select {
	case validResult = <-first:
	case <-time.After(time.Second):
		t.Fatal("JWKS request did not finish")
	}
	if !errors.Is(validResult.err, cause) || invalidResult.err == nil || errors.Is(invalidResult.err, cause) {
		t.Fatalf("causes mixed: %v; %v", validResult.err, invalidResult.err)
	}
	for _, tc := range []struct {
		result   result
		category string
	}{{validResult, "key_fetch"}, {invalidResult, "tenant_claim"}} {
		entry := diagnosticRecord(t, diagnosticRecords(t, tc.result.logs), "identity_verification", "completed")
		if entry["verification_category"] != tc.category || entry["request_id"] != "request-one" || entry["operation_id"] != "operation-one" || entry["app_id"] != "app-one" || entry["provider"] != "microsoft" {
			t.Fatalf("category or correlation mixed: %v", entry)
		}
		for _, secret := range []string{testOrgOne, "sensitive@example.test", "client-secret", "secret", "valid-code", "expected-nonce", tokens["valid-code"], tokens["invalid-code"]} {
			if strings.Contains(tc.result.logs.String(), secret) {
				t.Fatalf("diagnostics leaked %q", secret)
			}
		}
	}
}

func TestMicrosoftKeySetRejectsMalformedAndMultipleSignatures(t *testing.T) {
	key := signingKey(t)
	signer, err := jose.NewMultiSigner([]jose.SigningKey{{Algorithm: jose.RS256, Key: key}, {Algorithm: jose.RS256, Key: key}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(testMicrosoftClaims(testOrgOne))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	fixture, verifier := testMicrosoftVerifier(t)
	for _, raw := range []string{"not-a-jwt", signed.FullSerialize()} {
		_, err := verifier.keys.VerifySignature(context.Background(), raw)
		var failure *microsoftVerifyError
		if !errors.As(err, &failure) || failure.category != "signature" {
			t.Fatalf("invalid signature envelope accepted: %v", err)
		}
	}
	if fixture.requests() != 0 {
		t.Fatal("invalid signature envelope caused a fetch")
	}
}
