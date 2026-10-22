package uniauth_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/codetreker/uniauth/sdk/go"
)

func TestConfiguration(t *testing.T) {
	valid := uniauth.Config{BaseURL: "https://auth.example", AppID: "product", AppSecret: "secret"}
	for _, address := range []string{"https://auth.example", "https://auth.example/", "https://auth.example:8443", "https://[::1]", "https://LOCALHOST"} {
		config := valid
		config.BaseURL = address
		if _, err := uniauth.New(config); err != nil {
			t.Errorf("valid origin rejected: %s: %v", address, err)
		}
	}
	for _, address := range []string{"", "https://", "https://auth.example/path", "https://auth.example/%2F", "https://name:secret@auth.example", "https://auth.example?", "https://auth.example#", "https://auth.example?x=1", "https://auth.example/#fragment", "https:opaque", "https://auth.example:bad", "https://auth.example/%zz", "http://auth.example", "http://127.0.0.1", "ftp://auth.example", string([]byte{'h', 255})} {
		config := valid
		config.BaseURL = address
		if _, err := uniauth.New(config); err == nil {
			t.Errorf("invalid origin accepted: %q", address)
		}
	}
	for _, address := range []string{"http://127.0.0.1:8080", "http://127.2.3.4", "http://localhost", "http://LOCALHOST", "http://[::1]:8080"} {
		config := valid
		config.BaseURL, config.AllowLoopbackHTTP = address, true
		if _, err := uniauth.New(config); err != nil {
			t.Errorf("loopback opt-in rejected: %s: %v", address, err)
		}
	}
	for _, address := range []string{"http://auth.example", "http://192.168.1.1", "http://localhost.example", "http://[::ffff:192.168.1.1]"} {
		config := valid
		config.BaseURL, config.AllowLoopbackHTTP = address, true
		if _, err := uniauth.New(config); err == nil {
			t.Errorf("non-loopback HTTP accepted: %s", address)
		}
	}
	for _, id := range []string{"", "has:colon", strings.Repeat("é", 65), string([]byte{255})} {
		config := valid
		config.AppID = id
		if _, err := uniauth.New(config); err == nil {
			t.Error("invalid App ID accepted")
		}
	}
	for _, secret := range []string{"", strings.Repeat("é", 257), string([]byte{255})} {
		config := valid
		config.AppSecret = secret
		if _, err := uniauth.New(config); err == nil {
			t.Error("invalid App Secret accepted")
		}
	}
	valid.AppID, valid.AppSecret = strings.Repeat("é", 64), strings.Repeat("é", 256)
	if _, err := uniauth.New(valid); err != nil {
		t.Fatalf("UTF-8 byte boundaries rejected: %v", err)
	}
}

func TestAuthorizationPreservesExactCallbackAndExcludesSecret(t *testing.T) {
	c := clientFor(t, "https://auth.example/", nil)
	for _, callback := range []string{"https://PRODUCT.example:443/a%2fb?z=last&x=a%20b&x=%2B&flag", "http://PRODUCT.example:80/a%2fb?z=last&x=a%20b&x=%2B&flag"} {
		for _, provider := range []uniauth.Provider{uniauth.Microsoft, uniauth.Google, uniauth.GitHub} {
			address, err := c.AuthorizationURL(uniauth.AuthorizationRequest{Provider: provider, RedirectURI: callback, State: "browser + state"})
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(address)
			if err != nil || u.Path != "/auth/authorize" || len(u.Query()) != 4 || u.Query().Get("redirect_uri") != callback || u.Query().Get("state") != "browser + state" || u.Query().Get("app_id") != "product" || u.Query().Get("provider") != string(provider) || strings.Contains(address, "private-secret") {
				t.Fatalf("authorization changed the callback or exposed a secret: %v", err)
			}
		}
	}
	for _, callback := range []string{"http://127.0.0.1/cb", "http://[::1]/cb", "http://localhost/cb"} {
		if _, err := c.AuthorizationURL(uniauth.AuthorizationRequest{Provider: uniauth.GitHub, RedirectURI: callback, State: "state"}); err != nil {
			t.Fatalf("loopback callback rejected: %v", err)
		}
	}
	strict, err := uniauth.New(uniauth.Config{BaseURL: "https://auth.example", AppID: "product", AppSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, callback := range []string{"http://product.example/cb", "http://localhost/cb", "http://192.168.1.1/cb"} {
		if _, err := strict.AuthorizationURL(uniauth.AuthorizationRequest{Provider: uniauth.GitHub, RedirectURI: callback, State: "state"}); err != nil {
			t.Fatalf("HTTP callback rejected without gateway transport opt-in: %v", err)
		}
	}
}

func TestAuthorizationValidation(t *testing.T) {
	c := clientFor(t, "https://auth.example", nil)
	valid := uniauth.AuthorizationRequest{Provider: uniauth.GitHub, RedirectURI: "https://product.example/cb", State: "state"}
	for _, callback := range []string{"", strings.Repeat("a", 2049), "https://product.example/" + string([]byte{255}), "https://", "https:opaque", "https://name:secret@product.example/cb", "https://product.example/cb#", "https://product.example/%zz", "ftp://product.example/cb", "https://product.example/cb?x=%zz", "https://product.example/cb?x=a;b", "https://product.example/cb?code=", "https://product.example/cb?%73tate=x", "https://product.example/cb?error"} {
		request := valid
		request.RedirectURI = callback
		if _, err := c.AuthorizationURL(request); err == nil {
			t.Errorf("invalid callback accepted: %q", callback)
		}
	}
	for _, state := range []string{"", strings.Repeat("é", 1025), string([]byte{255})} {
		request := valid
		request.State = state
		if _, err := c.AuthorizationURL(request); err == nil {
			t.Error("invalid state accepted")
		}
	}
	valid.Provider = "unknown"
	if _, err := c.AuthorizationURL(valid); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	valid.Provider, valid.State = uniauth.Google, strings.Repeat("é", 1024)
	valid.RedirectURI = "https://product.example/" + strings.Repeat("a", 2048-len("https://product.example/"))
	if _, err := c.AuthorizationURL(valid); err != nil {
		t.Fatalf("authorization byte boundaries rejected: %v", err)
	}
}

func TestCallbackValidation(t *testing.T) {
	tests := []struct {
		name, expected, code string
		query                url.Values
	}{
		{"valid", "state", "", url.Values{"state": {"state"}, "code": {"result"}, "product_query": {"retained"}}},
		{"nil", "state", "invalid_callback", nil},
		{"duplicate-state", "state", "invalid_callback", url.Values{"state": {"state", "state"}, "code": {"result"}}},
		{"empty-state", "state", "invalid_callback", url.Values{"state": {""}, "code": {"result"}}},
		{"long-state", "state", "invalid_callback", url.Values{"state": {strings.Repeat("é", 1025)}, "code": {"result"}}},
		{"bad-state", "state", "invalid_callback", url.Values{"state": {string([]byte{255})}, "code": {"result"}}},
		{"empty-expected", "", "invalid_callback", url.Values{"state": {"state"}, "code": {"result"}}},
		{"long-expected", strings.Repeat("é", 1025), "invalid_callback", url.Values{"state": {"state"}, "code": {"result"}}},
		{"bad-expected", string([]byte{255}), "invalid_callback", url.Values{"state": {"state"}, "code": {"result"}}},
		{"mismatch", "other-state", "state_mismatch", url.Values{"state": {"state"}, "code": {"result"}}},
		{"neither", "state", "invalid_callback", url.Values{"state": {"state"}}},
		{"both", "state", "invalid_callback", url.Values{"state": {"state"}, "code": {"result"}, "error": {"provider_denied"}}},
		{"empty-error-with-code", "state", "invalid_callback", url.Values{"state": {"state"}, "code": {"result"}, "error": {}}},
		{"empty-code", "state", "invalid_callback", url.Values{"state": {"state"}, "code": {""}}},
		{"duplicate-code", "state", "invalid_callback", url.Values{"state": {"state"}, "code": {"result", "result"}}},
		{"long-code", "state", "invalid_callback", url.Values{"state": {"state"}, "code": {strings.Repeat("é", 257)}}},
		{"bad-code", "state", "invalid_callback", url.Values{"state": {"state"}, "code": {string([]byte{255})}}},
		{"unknown-error", "state", "invalid_callback", url.Values{"state": {"state"}, "error": {"unknown"}}},
		{"duplicate-error", "state", "invalid_callback", url.Values{"state": {"state"}, "error": {"provider_denied", "provider_denied"}}},
		{"denied", "state", "provider_denied", url.Values{"state": {"state"}, "error": {"provider_denied"}}},
		{"failed", "state", "provider_failed", url.Values{"state": {"state"}, "error": {"provider_failed"}}},
		{"capacity", "state", "capacity_exceeded", url.Values{"state": {"state"}, "error": {"capacity_exceeded"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := uniauth.ParseCallback(test.query, test.expected)
			if test.code == "" {
				if err != nil || result != "result" {
					t.Fatalf("valid callback failed: %v", err)
				}
				return
			}
			var callbackErr *uniauth.CallbackError
			if result != "" || !errors.As(err, &callbackErr) || callbackErr.Code != test.code || strings.Contains(err.Error(), "result") {
				t.Fatalf("wrong callback failure: %v", err)
			}
		})
	}
	state, code := strings.Repeat("é", 1024), strings.Repeat("é", 256)
	if result, err := uniauth.ParseCallback(url.Values{"state": {state}, "code": {code}}, state); err != nil || result != code {
		t.Fatalf("callback byte boundaries rejected: %v", err)
	}
}
