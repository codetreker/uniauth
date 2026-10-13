package providers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

const testVerifier = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

var testKeyOnce sync.Once
var testKey *rsa.PrivateKey

func signingKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	var err error
	testKeyOnce.Do(func() { testKey, err = rsa.GenerateKey(rand.Reader, 2048) })
	if err != nil {
		t.Fatal(err)
	}
	return testKey
}

type fixture struct {
	server     *httptest.Server
	claims     map[string]any
	profile    string
	userinfo   string
	emails     string
	emailPages map[string]string
	photo      []byte
	photoType  string
	status     map[string]int
	tokenExtra map[string]any
	omitID     bool
	jwksDelay  time.Duration
	issuer     string
	key        *rsa.PrivateKey
	jwksKey    *rsa.PrivateKey
	requests   []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{key: signingKey(t), status: map[string]int{}, tokenExtra: map[string]any{},
		profile:  `{"id":123,"email":null,"unknown":{"false":false},"login":"someone"}`,
		userinfo: `{"sub":"user-123","unknown":null,"email_verified":false}`, emails: `[{"email":"a@example.test","verified":false,"primary":true,"unknown":null}]`, photo: []byte{0, 1, 2, 3}, photoType: "image/jpeg"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.URL.Path)
		if status := f.status[r.URL.Path]; status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":"provider-secret-sensitive-description"}`)
			return
		}
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": f.issuer, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "jwks_uri": f.server.URL + "/jwks", "userinfo_endpoint": f.server.URL + "/userinfo", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			time.Sleep(f.jwksDelay)
			key := f.key
			if f.jwksKey != nil {
				key = f.jwksKey
			}
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("code_verifier") != testVerifier || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "valid-code" || r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" {
				t.Errorf("invalid exchange form: %v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			result := map[string]any{"access_token": "access-secret", "token_type": "Bearer", "refresh_token": "refresh-secret"}
			if !f.omitID {
				signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: f.key, KeyID: "fixture"}}, nil)
				if err != nil {
					t.Error(err)
					return
				}
				claims, _ := json.Marshal(f.claims)
				signed, err := signer.Sign(claims)
				if err != nil {
					t.Error(err)
					return
				}
				result["id_token"], _ = signed.CompactSerialize()
			}
			for key, value := range f.tokenExtra {
				result[key] = value
			}
			json.NewEncoder(w).Encode(result)
		case "/profile", "/userinfo", "/emails", "/photo":
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				t.Errorf("invalid authorization header")
			}
			if r.URL.Path == "/photo" {
				if r.Header.Get("Accept") != "image/*" {
					t.Errorf("wrong photo Accept header")
				}
				w.Header().Set("Content-Type", f.photoType)
				w.Write(f.photo)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/profile":
				fmt.Fprint(w, f.profile)
			case "/userinfo":
				fmt.Fprint(w, f.userinfo)
			case "/emails":
				if f.emailPages != nil {
					fmt.Fprint(w, f.emailPages[r.URL.Query().Get("page")])
				} else {
					fmt.Fprint(w, f.emails)
				}
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	f.issuer = f.server.URL
	f.claims = map[string]any{"iss": f.issuer, "aud": "client-id", "sub": "user-123", "nonce": "expected-nonce", "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(), "tid": microsoftTenant, "oid": "object-123", "unknown": map[string]any{"value": false}, "optional": nil}
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) config() Config {
	client := f.server.Client()
	client.Timeout = time.Second
	return Config{ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://gateway.example/auth/callback", HTTPClient: client}
}

func (f *fixture) provider(t *testing.T, name string) Provider {
	t.Helper()
	endpoint := oauth2.Endpoint{AuthURL: f.server.URL + "/authorize", TokenURL: f.server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
	var p Provider
	var err error
	switch name {
	case "microsoft":
		p, err = newMicrosoft(context.Background(), f.config(), f.issuer, f.server.URL+"/jwks", endpoint, f.server.URL+"/profile", f.server.URL+"/photo")
	case "google":
		p, err = newGoogle(context.Background(), f.config(), f.issuer)
	case "github":
		p, err = newGitHub(f.config(), endpoint, f.server.URL+"/profile", f.server.URL+"/emails")
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func exchangeRequest() ExchangeRequest {
	return ExchangeRequest{Code: "valid-code", ExpectedNonce: "expected-nonce", CodeVerifier: testVerifier}
}

func TestProvidersCompleteIdentityAndAuthorization(t *testing.T) {
	for _, name := range []string{"microsoft", "google", "github"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			p := f.provider(t, name)
			if p.Name() != name {
				t.Fatalf("wrong name %s", p.Name())
			}
			authURL, err := p.AuthorizationURL(AuthorizationRequest{State: "flow-state", Nonce: "expected-nonce", CodeVerifier: testVerifier})
			if err != nil {
				t.Fatal(err)
			}
			parsed, _ := url.Parse(authURL)
			query := parsed.Query()
			if query.Get("state") != "flow-state" || query.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(testVerifier) || query.Get("code_challenge_method") != "S256" || query.Get("redirect_uri") != f.config().RedirectURL || query.Get("response_type") != "code" {
				t.Fatalf("authorization parameters: %v", query)
			}
			if name != "github" && query.Get("nonce") != "expected-nonce" {
				t.Fatal("missing nonce")
			}
			identity, err := p.Exchange(context.Background(), exchangeRequest())
			if err != nil {
				t.Fatal(err)
			}
			serialized, _ := json.Marshal(identity)
			if strings.Contains(string(serialized), "access-secret") || strings.Contains(string(serialized), "refresh-secret") || strings.Contains(string(serialized), "client-secret") {
				t.Fatal("identity leaked credentials")
			}
			switch name {
			case "microsoft":
				if identity.ID != "microsoft:"+microsoftTenant+":object-123" || string(identity.Origin["profile"]) != f.profile {
					t.Fatalf("identity: %+v", identity)
				}
				var photo struct {
					ContentType string `json:"content_type"`
					DataBase64  string `json:"data_base64"`
				}
				if err := json.Unmarshal(identity.Origin["photo"], &photo); err != nil {
					t.Fatal(err)
				}
				if photo.ContentType != "image/jpeg" || photo.DataBase64 != base64.StdEncoding.EncodeToString(f.photo) {
					t.Fatalf("photo: %+v", photo)
				}
				if !strings.Contains(query.Get("scope"), "User.Read") {
					t.Fatal("missing Microsoft User.Read")
				}
			case "google":
				if identity.ID != "google:user-123" || string(identity.Origin["userinfo"]) != f.userinfo {
					t.Fatalf("identity: %+v", identity)
				}
			case "github":
				if identity.ID != "github:123" || string(identity.Origin["profile"]) != f.profile || string(identity.Origin["emails"]) != f.emails {
					t.Fatalf("identity: %+v", identity)
				}
				if query.Get("scope") != "read:user user:email" {
					t.Fatalf("scopes %s", query.Get("scope"))
				}
			}
			if name != "github" {
				fields, err := jsonObject(identity.Origin["id_token_claims"])
				if err != nil {
					t.Fatal(err)
				}
				if string(fields["optional"]) != "null" || string(fields["unknown"]) != "{\"value\":false}" {
					t.Fatalf("claims lost fields: %s", identity.Origin["id_token_claims"])
				}
			}
		})
	}
}

func TestOIDCRejectsUntrustedClaims(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fixture)
	}{
		{"issuer", func(f *fixture) { f.claims["iss"] = "https://attacker.example" }},
		{"audience", func(f *fixture) { f.claims["aud"] = "another-client" }},
		{"nonce", func(f *fixture) { f.claims["nonce"] = "another-flow" }},
		{"missing nonce", func(f *fixture) { delete(f.claims, "nonce") }},
		{"empty subject", func(f *fixture) { f.claims["sub"] = "" }},
		{"wrong subject type", func(f *fixture) { f.claims["sub"] = 123 }},
		{"expired", func(f *fixture) { f.claims["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{"missing expiry", func(f *fixture) { delete(f.claims, "exp") }},
		{"missing issued-at", func(f *fixture) { delete(f.claims, "iat") }},
		{"future issued-at", func(f *fixture) { f.claims["iat"] = time.Now().Add(time.Hour).Unix() }},
		{"azp mismatch", func(f *fixture) { f.claims["azp"] = "another-client" }},
		{"azp null", func(f *fixture) { f.claims["azp"] = nil }},
		{"multiple audience without azp", func(f *fixture) { f.claims["aud"] = []string{"client-id", "another-client"} }},
		{"missing ID token", func(f *fixture) { f.omitID = true }},
		{"access-token hash mismatch", func(f *fixture) { f.claims["at_hash"] = "invalid" }},
		{"nonbearer token", func(f *fixture) { f.tokenExtra["token_type"] = "Mac" }},
		{"empty access token", func(f *fixture) { f.tokenExtra["access_token"] = "" }},
	}
	for _, provider := range []string{"microsoft", "google"} {
		for _, tc := range cases {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				f := newFixture(t)
				p := f.provider(t, provider)
				tc.mutate(f)
				_, err := p.Exchange(context.Background(), exchangeRequest())
				if err == nil {
					t.Fatal("invalid claims accepted")
				}
				if errors.Unwrap(err) == nil {
					t.Fatalf("cause missing: %v", err)
				}
			})
		}
	}
}

func TestProviderSpecificIdentityFailures(t *testing.T) {
	cases := []struct {
		name, provider string
		mutate         func(*fixture)
	}{
		{"organization tenant", "microsoft", func(f *fixture) { f.claims["tid"] = "organization" }},
		{"missing oid", "microsoft", func(f *fixture) { delete(f.claims, "oid") }},
		{"empty oid", "microsoft", func(f *fixture) { f.claims["oid"] = "" }},
		{"numeric oid", "microsoft", func(f *fixture) { f.claims["oid"] = 123 }},
		{"profile null", "microsoft", func(f *fixture) { f.profile = "null" }},
		{"profile invalid", "microsoft", func(f *fixture) { f.profile = "{" }},
		{"photo forbidden", "microsoft", func(f *fixture) { f.status["/photo"] = 403 }},
		{"photo empty", "microsoft", func(f *fixture) { f.photo = nil }},
		{"userinfo mismatch", "google", func(f *fixture) { f.userinfo = `{"sub":"another-user"}` }},
		{"userinfo null", "google", func(f *fixture) { f.userinfo = "null" }},
		{"userinfo malformed", "google", func(f *fixture) { f.userinfo = "{" }},
		{"userinfo unavailable", "google", func(f *fixture) { f.status["/userinfo"] = 500 }},
		{"id missing", "github", func(f *fixture) { f.profile = `{"login":"someone"}` }},
		{"id string", "github", func(f *fixture) { f.profile = `{"id":"123"}` }},
		{"id negative", "github", func(f *fixture) { f.profile = `{"id":-123}` }},
		{"profile unavailable", "github", func(f *fixture) { f.status["/profile"] = 403 }},
		{"emails null", "github", func(f *fixture) { f.emails = "null" }},
		{"emails invalid", "github", func(f *fixture) { f.emails = "{" }},
		{"email item null", "github", func(f *fixture) { f.emails = "[null]" }},
		{"emails unavailable", "github", func(f *fixture) { f.status["/emails"] = 403 }},
		{"token error", "github", func(f *fixture) { f.status["/token"] = 400 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := f.provider(t, tc.provider)
			tc.mutate(f)
			_, err := p.Exchange(context.Background(), exchangeRequest())
			if err == nil {
				t.Fatal("provider failure ignored")
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("unsafe error: %v", err)
			}
			if errors.Unwrap(err) == nil {
				t.Fatal("underlying cause lost")
			}
		})
	}
}

func TestMicrosoftPhotoAbsenceAndGoogleMultipleAudiences(t *testing.T) {
	f := newFixture(t)
	f.status["/photo"] = 404
	identity, err := f.provider(t, "microsoft").Exchange(context.Background(), exchangeRequest())
	if err != nil || string(identity.Origin["photo"]) != "null" || identity.AvatarURL != nil {
		t.Fatalf("missing photo: %+v, %v", identity, err)
	}
	f2 := newFixture(t)
	f2.claims["aud"] = []string{"client-id", "another-client"}
	f2.claims["azp"] = "client-id"
	if _, err := f2.provider(t, "google").Exchange(context.Background(), exchangeRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidProviderRequestsAndConfiguration(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"microsoft", "google", "github"} {
		p := f.provider(t, name)
		for _, r := range []AuthorizationRequest{{}, {State: "state", Nonce: "nonce", CodeVerifier: "short"}, {State: "state", Nonce: "nonce", CodeVerifier: strings.Repeat("!", 43)}} {
			if _, err := p.AuthorizationURL(r); err == nil {
				t.Fatal("invalid authorization accepted")
			}
		}
		if _, err := p.Exchange(context.Background(), ExchangeRequest{}); err == nil {
			t.Fatal("invalid exchange accepted")
		}
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.ClientID = "" }, func(c *Config) { c.ClientSecret = "" }, func(c *Config) { c.RedirectURL = "relative" }, func(c *Config) { c.RedirectURL = "https://user@example.test/callback" }, func(c *Config) { c.HTTPClient = nil }, func(c *Config) { c.HTTPClient = &http.Client{} }, func(c *Config) { c.HTTPClient = &http.Client{Timeout: 2 * time.Minute} }} {
		c := f.config()
		mutate(&c)
		if _, err := NewMicrosoft(context.Background(), c); err == nil {
			t.Fatal("invalid Microsoft config accepted")
		}
		if _, err := NewGoogle(context.Background(), c); err == nil {
			t.Fatal("invalid Google config accepted")
		}
		if _, err := NewGitHub(c); err == nil {
			t.Fatal("invalid GitHub config accepted")
		}
	}
}

func TestJWKSRefreshAndExchangeBoundedByTimeout(t *testing.T) {
	f := newFixture(t)
	f.jwksDelay = 200 * time.Millisecond
	c := f.config()
	c.HTTPClient.Timeout = 30 * time.Millisecond
	p, err := newMicrosoft(context.Background(), c, f.issuer, f.server.URL+"/jwks", oauth2.Endpoint{TokenURL: f.server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}, f.server.URL+"/profile", f.server.URL+"/photo")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = p.Exchange(context.Background(), exchangeRequest())
	if err == nil || time.Since(started) > 150*time.Millisecond {
		t.Fatalf("JWKS fetch not bounded: %v elapsed %s", err, time.Since(started))
	}
}

func TestRedirectsDoNotForwardProviderCredentials(t *testing.T) {
	called := false
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer sink.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	_, _, _, err := fetch(context.Background(), boundedClient(client), server.URL, "secret")
	if err == nil || called {
		t.Fatalf("redirect followed: called=%v error=%v", called, err)
	}
}

func TestGitHubPreservesCompleteEmailPagination(t *testing.T) {
	f := newFixture(t)
	batch := make([]json.RawMessage, 100)
	for i := range batch {
		batch[i] = json.RawMessage(fmt.Sprintf(`{"email":"%d@example.test","verified":false,"extra":null}`, i))
	}
	first, _ := json.Marshal(batch)
	f.emailPages = map[string]string{"1": string(first), "2": `[{"email":"last@example.test","primary":true,"verified":true,"unknown":false}]`}
	identity, err := f.provider(t, "github").Exchange(context.Background(), exchangeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID != "github:123" || identity.Email == nil || *identity.Email != "last@example.test" || identity.EmailVerified == nil || !*identity.EmailVerified {
		t.Fatalf("primary email from later page not selected: %+v", identity)
	}
	var emails []map[string]json.RawMessage
	if err := json.Unmarshal(identity.Origin["emails"], &emails); err != nil {
		t.Fatal(err)
	}
	if len(emails) != 101 || string(emails[100]["unknown"]) != "false" || string(emails[0]["extra"]) != "null" {
		t.Fatalf("incomplete email pages: %s", identity.Origin["emails"])
	}
}

func TestBodySizeLimitsAndCanceledRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(maxResponseBytes+1))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	_, _, _, err := fetch(context.Background(), boundedClient(client), server.URL, "secret")
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("large response accepted: %v", err)
	}
	f := newFixture(t)
	p := f.provider(t, "github")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.Exchange(ctx, exchangeRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
}

func TestProductionEndpointsAndDiscoveryFailure(t *testing.T) {
	f := newFixture(t)
	ms, err := NewMicrosoft(context.Background(), f.config())
	if err != nil {
		t.Fatal(err)
	}
	if ms.oauth.Endpoint.AuthURL != "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize" || ms.oauth.Endpoint.TokenURL != "https://login.microsoftonline.com/consumers/oauth2/v2.0/token" {
		t.Fatalf("wrong Microsoft endpoint: %+v", ms.oauth.Endpoint)
	}
	gh, err := NewGitHub(f.config())
	if err != nil {
		t.Fatal(err)
	}
	if gh.profileURL != "https://api.github.com/user" || gh.emailsURL != "https://api.github.com/user/emails" {
		t.Fatal("wrong GitHub endpoints")
	}
	f.status["/.well-known/openid-configuration"] = 500
	if _, err := newGoogle(context.Background(), f.config(), f.issuer); err == nil || errors.Unwrap(err) == nil {
		t.Fatalf("discovery error lost: %v", err)
	}
}

func TestOIDCRejectsInvalidSignature(t *testing.T) {
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"microsoft", "google"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.jwksKey = wrongKey
			if _, err := f.provider(t, name).Exchange(context.Background(), exchangeRequest()); err == nil {
				t.Fatal("invalid signature accepted")
			}
		})
	}
}

func TestProviderProfileStatusCauseIsAvailable(t *testing.T) {
	f := newFixture(t)
	f.status["/profile"] = http.StatusForbidden
	_, err := f.provider(t, "github").Exchange(context.Background(), exchangeRequest())
	var status statusError
	if !errors.As(err, &status) || status.status != http.StatusForbidden || status.Error() != "upstream HTTP status 403" {
		t.Fatalf("profile HTTP cause lost: %v", err)
	}
	if err.Error() != "provider profile retrieval failed" {
		t.Fatalf("unsafe provider error: %v", err)
	}
}

type countedBody struct {
	io.Reader
	reads int
}

func (b *countedBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (*countedBody) Close() error                 { return nil }

func TestResponseBodySizeFailureIsTerminal(t *testing.T) {
	underlying := &countedBody{Reader: strings.NewReader("too large")}
	body := &boundedBody{body: underlying, remaining: 2}
	buffer := make([]byte, 32)
	n, err := body.Read(buffer)
	if n != 3 || !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("oversize response accepted: n=%d err=%v", n, err)
	}
	for range 3 {
		n, err = body.Read(buffer)
		if n != 0 || !errors.Is(err, errResponseTooLarge) {
			t.Fatalf("terminal body error lost: n=%d err=%v", n, err)
		}
	}
	if underlying.reads != 1 {
		t.Fatalf("read upstream body after terminal failure %d times", underlying.reads)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedBody struct{ cause error }

func (b failedBody) Read([]byte) (int, error) { return 0, b.cause }
func (failedBody) Close() error               { return nil }

func TestProviderProfileBodyReadCauseIsPreserved(t *testing.T) {
	f := newFixture(t)
	cause := errors.New("upstream stream interrupted")
	config := f.config()
	base := config.HTTPClient.Transport
	config.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/profile" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: failedBody{cause: cause}, Request: r}, nil
		}
		return base.RoundTrip(r)
	})
	p, err := newGitHub(config, oauth2.Endpoint{TokenURL: f.server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}, f.server.URL+"/profile", f.server.URL+"/emails")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exchange(context.Background(), exchangeRequest())
	if !errors.Is(err, cause) {
		t.Fatalf("body read cause lost: %v", err)
	}
}
