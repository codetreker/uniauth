package uniauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codetreker/uniauth/sdk/go"
)

func TestProvidersRequestAndAvailableSubsets(t *testing.T) {
	callback := "http://Product.Example:8080/%63allback?tenant=a%2Fb&tenant=c+d"
	for _, want := range [][]uniauth.Provider{{uniauth.Microsoft, uniauth.Google, uniauth.GitHub}, {uniauth.Google, uniauth.GitHub}, {uniauth.GitHub}, {}} {
		t.Run(strings.Join(providerNames(want), ","), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				id, secret, ok := r.BasicAuth()
				if !ok || id != "product" || secret != "private-secret" || r.Method != http.MethodPost || r.URL.RequestURI() != "/auth/providers" {
					t.Errorf("invalid provider request: method=%s path=%s Basic=%v", r.Method, r.URL.RequestURI(), ok)
				}
				if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
					t.Error("invalid provider request content negotiation")
				}
				var input map[string]string
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input) != 1 || input["redirect_uri"] != callback {
					t.Errorf("callback was changed: %v", err)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(map[string]any{"providers": want, "future": map[string]any{"available": true}})
			}))
			defer server.Close()
			got, err := clientFor(t, server.URL, nil).Providers(context.Background(), callback)
			if err != nil || got == nil || !reflect.DeepEqual(got, want) || calls.Load() != 1 {
				t.Fatalf("provider subset changed: got=%v want=%v calls=%d err=%v", got, want, calls.Load(), err)
			}
		})
	}
}

func providerNames(providers []uniauth.Provider) []string {
	names := make([]string, len(providers))
	for i, provider := range providers {
		names[i] = string(provider)
	}
	return names
}

func TestProvidersResponseValidation(t *testing.T) {
	for _, test := range []struct {
		name, contentType, body, reason string
		status                          int
	}{
		{"redirect", "application/json", `{"providers":[]}`, "unexpected_status", 307},
		{"created", "application/json", `{"providers":[]}`, "unexpected_status", 201},
		{"text", "text/html", "private-data", "content_type", 200},
		{"missing-type", "", `{"providers":[]}`, "content_type", 200},
		{"bad-type", "application/json; bad", `{"providers":[]}`, "content_type", 200},
		{"invalid-json", "application/json", `{"providers":`, "json", 200},
		{"trailing-json", "application/json", `{"providers":[]} {}`, "json", 200},
		{"trailing-garbage", "application/json", `{"providers":[]} garbage`, "json", 200},
		{"invalid-utf8", "application/json", "{\"providers\":[\"" + string([]byte{255}) + "\"]}", "json", 200},
		{"array-envelope", "application/json", `[]`, "json", 200},
		{"null-envelope", "application/json", `null`, "json", 200},
		{"scalar-envelope", "application/json", `true`, "json", 200},
		{"missing-providers", "application/json", `{}`, "providers_schema", 200},
		{"null-providers", "application/json", `{"providers":null}`, "providers_schema", 200},
		{"string-providers", "application/json", `{"providers":"github"}`, "providers_schema", 200},
		{"object-providers", "application/json", `{"providers":{}}`, "providers_schema", 200},
		{"number-providers", "application/json", `{"providers":1}`, "providers_schema", 200},
		{"bool-providers", "application/json", `{"providers":false}`, "providers_schema", 200},
		{"unknown-provider", "application/json", `{"providers":["private-provider"]}`, "providers_schema", 200},
		{"null-provider", "application/json", `{"providers":[null]}`, "providers_schema", 200},
		{"number-provider", "application/json", `{"providers":[1]}`, "providers_schema", 200},
		{"bool-provider", "application/json", `{"providers":[false]}`, "providers_schema", 200},
		{"array-provider", "application/json", `{"providers":[[]]}`, "providers_schema", 200},
		{"object-provider", "application/json", `{"providers":[{}]}`, "providers_schema", 200},
		{"duplicate-provider", "application/json", `{"providers":["github","github"]}`, "providers_schema", 200},
		{"duplicate-providers-field", "application/json", `{"providers":["github"],"providers":[]}`, "json", 200},
		{"escaped-duplicate-field", "application/json", `{"providers":[],"\u0070roviders":[]}`, "json", 200},
		{"duplicate-unknown-field", "application/json", `{"providers":[],"future":1,"future":2}`, "json", 200},
		{"nested-duplicate-field", "application/json", `{"providers":[],"future":[{"x":1,"x":2}]}`, "json", 200},
		{"missing-error", "application/json", `{}`, "error_schema", 400},
		{"null-error", "application/json", `{"error":null}`, "error_schema", 400},
		{"array-error", "application/json", `{"error":[]}`, "error_schema", 400},
		{"missing-error-code", "application/json", `{"error":{"message":"private-data"}}`, "error_schema", 400},
		{"empty-error-code", "application/json", `{"error":{"code":"","message":"private-data"}}`, "error_schema", 400},
		{"null-error-code", "application/json", `{"error":{"code":null,"message":"private-data"}}`, "error_schema", 400},
		{"bad-error-code", "application/json", `{"error":{"code":false,"message":"private-data"}}`, "error_schema", 400},
		{"missing-error-message", "application/json", `{"error":{"code":"invalid_request"}}`, "error_schema", 400},
		{"empty-error-message", "application/json", `{"error":{"code":"invalid_request","message":""}}`, "error_schema", 400},
		{"null-error-message", "application/json", `{"error":{"code":"invalid_request","message":null}}`, "error_schema", 400},
		{"bad-error-message", "application/json", `{"error":{"code":"invalid_request","message":1}}`, "error_schema", 400},
		{"duplicate-error-field", "application/json", `{"error":{"code":"invalid_request","message":"x","message":"y"}}`, "json", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(test.status, test.contentType, test.body), nil
			})})
			providers, err := c.Providers(context.Background(), "https://product.example/private-callback")
			var protocolErr *uniauth.ProtocolError
			if providers != nil || !errors.As(err, &protocolErr) || protocolErr.StatusCode != test.status || protocolErr.RequestID != "request-123" || protocolErr.Reason != test.reason {
				t.Fatalf("wrong provider protocol error: %v", err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "result") {
				t.Fatal("error text exposed data or described a result operation")
			}
		})
	}
}

func TestProvidersPreservesGatewayFailures(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{{400, "invalid_request"}, {401, "invalid_app"}, {500, "internal"}, {503, "closed"}, {599, "future_error"}} {
		t.Run(test.code, func(t *testing.T) {
			c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(test.status, "application/json", `{"error":{"code":"`+test.code+`","message":"private-response","future":false},"future":true}`), nil
			})})
			_, err := c.Providers(context.Background(), "https://product.example/private-callback")
			var apiErr *uniauth.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != test.status || apiErr.Code != test.code || apiErr.Message != "private-response" || apiErr.RequestID != "request-123" || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "result") {
				t.Fatalf("gateway error was changed or exposed: %v", err)
			}
		})
	}
}

type countedProviderBody struct {
	reader   io.Reader
	read     int
	closed   bool
	closeErr error
}

func (b *countedProviderBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *countedProviderBody) Close() error {
	b.closed = true
	return b.closeErr
}

func TestProvidersBoundsSuccessAndFailureBodies(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			closeCause := errors.New("private-close-data")
			body := &countedProviderBody{reader: strings.NewReader(strings.Repeat(" ", 1<<20)), closeErr: closeCause}
			c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				r := response(status, "application/json", "")
				r.Body = body
				return r, nil
			})})
			_, err := c.Providers(context.Background(), "https://product.example/callback")
			var protocolErr *uniauth.ProtocolError
			if !errors.As(err, &protocolErr) || protocolErr.Reason != "response_size" || !errors.Is(err, closeCause) || body.read != (16<<10)+1 || !body.closed || strings.Contains(err.Error(), "private") {
				t.Fatalf("response overflow was not bounded and closed: bytes=%d closed=%v err=%v", body.read, body.closed, err)
			}
		})
	}
	for _, content := range []string{`{"providers":[]}`, `{"providers":[],"future":[1e1000,{"nested":true}]}`} {
		body := content + strings.Repeat(" ", (16<<10)-len(content))
		c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(200, "application/json", body), nil
		})})
		if providers, err := c.Providers(context.Background(), "https://product.example/callback"); err != nil || providers == nil || len(providers) != 0 {
			t.Fatalf("valid response at size boundary was rejected: %v", err)
		}
	}
}

func TestProvidersRetainsCausesWithoutReplay(t *testing.T) {
	for _, kind := range []string{"transport", "read", "close", "read-and-close"} {
		t.Run(kind, func(t *testing.T) {
			readCause, closeCause := errors.New("private-read-data"), errors.New("private-close-data")
			var calls atomic.Int32
			c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				if r.GetBody != nil || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" {
					t.Error("provider discovery request can be replayed")
				}
				if kind == "transport" {
					return nil, readCause
				}
				result := response(200, "application/json", `{"providers":[]}`)
				if kind == "close" {
					result.Body = &countedProviderBody{reader: strings.NewReader(`{"providers":[]}`), closeErr: closeCause}
				} else {
					body := failingBody{readErr: readCause}
					if kind == "read-and-close" {
						body.closeErr = closeCause
					}
					result.Body = body
				}
				return result, nil
			})})
			_, err := c.Providers(context.Background(), "https://product.example/private-callback")
			var requestErr *uniauth.RequestError
			var protocolErr *uniauth.ProtocolError
			if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "result") {
				t.Fatalf("request count or safe error text changed: %v", err)
			}
			if kind == "transport" {
				if !errors.As(err, &requestErr) || !errors.Is(err, readCause) {
					t.Fatalf("transport cause lost: %v", err)
				}
			} else if !errors.As(err, &protocolErr) || protocolErr.Reason != "response_body" || (kind != "close" && !errors.Is(err, readCause)) || (kind != "read" && !errors.Is(err, closeCause)) {
				t.Fatalf("body cause lost: %v", err)
			}
		})
	}
}

func TestProvidersCancellationAndTimeout(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline", "client-timeout"} {
		t.Run(kind, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			caller := &http.Client{}
			if kind == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer deadlineCancel()
			}
			if kind == "client-timeout" {
				caller.Timeout = 100 * time.Millisecond
			}
			c := clientFor(t, server.URL, caller)
			done := make(chan error, 1)
			go func() { _, err := c.Providers(ctx, "https://product.example/callback"); done <- err }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not start")
			}
			want := context.DeadlineExceeded
			if kind == "cancel" {
				want = context.Canceled
				cancel()
			}
			select {
			case err := <-done:
				var requestErr *uniauth.RequestError
				if !errors.Is(err, want) || !errors.As(err, &requestErr) {
					t.Fatalf("request context/timeout lost: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request context/timeout did not stop discovery")
			}
		})
	}
}

func TestProvidersDisablesRedirectAndCopiesClient(t *testing.T) {
	var targetCalls, policyCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	caller := &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { policyCalls.Add(1); return nil }}
	c := clientFor(t, origin.URL, caller)
	_, err := c.Providers(context.Background(), "https://product.example/callback")
	var protocolErr *uniauth.ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.StatusCode != 307 || targetCalls.Load() != 0 || policyCalls.Load() != 0 {
		t.Fatalf("provider discovery followed redirect: %v", err)
	}
	r, err := caller.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if targetCalls.Load() != 1 || policyCalls.Load() != 1 || caller.Timeout != 7*time.Second {
		t.Fatal("caller HTTP client was mutated")
	}
}

func TestProvidersRejectsLocalErrorsBeforeSending(t *testing.T) {
	var calls atomic.Int32
	c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(200, "application/json", `{"providers":[]}`), nil
	})})
	for _, callback := range []string{"", strings.Repeat("a", 2049), string([]byte{255}), "/callback", "https://user:secret@product.example/callback", "https://product.example/callback#fragment", "ftp://product.example/callback", "https://product.example/callback?bad=%GG", "https://product.example/callback?code=x", "https://product.example/callback?state=x", "https://product.example/callback?error=x"} {
		if _, err := c.Providers(context.Background(), callback); err == nil {
			t.Fatalf("invalid callback accepted: %q", callback)
		}
	}
	if _, err := c.Providers(nil, "https://product.example/callback"); err == nil || calls.Load() != 0 {
		t.Fatal("invalid local input sent an HTTP request")
	}
}
