package uniauth_test

import (
	"context"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/codetreker/uniauth/sdk/go"
)

const identityJSON = `{"provider":"github","subject":"github:1","sources":{"profile":{"id":1}}}`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clientFor(t *testing.T, address string, httpClient *http.Client) *uniauth.Client {
	t.Helper()
	c, err := uniauth.New(uniauth.Config{BaseURL: address, AppID: "product", AppSecret: "private-secret", AllowLoopbackHTTP: true, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func response(status int, contentType, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {"request-123"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRetrieveRequestAndLosslessIdentity(t *testing.T) {
	body := " \n" + `{"provider":"github","subject":"github:9007199254740993","sources":{"profile":{"id":9007199254740993,"unknown":{"nested":[null,false,1.234567890123456789]}},"emails":[{"verified":false}],"photo":null},"future":false}` + "\n"
	var calls atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		id, secret, ok := r.BasicAuth()
		if !ok || id != "product" || secret != "private-secret" || r.Method != "POST" || r.URL.String() != "https://auth.example/auth/result" || r.GetBody != nil {
			t.Errorf("invalid retrieval request: method=%s URL=%s Basic=%v replayable=%v", r.Method, r.URL, ok, r.GetBody != nil)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" {
			t.Errorf("invalid request headers")
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input) != 1 || input["code"] != "result-code" {
			t.Errorf("invalid retrieval body: %v", err)
		}
		return response(200, "application/json; charset=utf-8", body), nil
	})
	c := clientFor(t, "https://auth.example/", &http.Client{Transport: transport})
	identity, err := c.Retrieve(context.Background(), "result-code")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || identity.Provider != uniauth.GitHub || identity.Subject != "github:9007199254740993" || string(identity.RawJSON) != body {
		t.Fatal("identity envelope or complete response changed")
	}
	if string(identity.Sources["photo"]) != "null" || string(identity.Sources["emails"]) != `[{"verified":false}]` || !strings.Contains(string(identity.Sources["profile"]), "9007199254740993") || !strings.Contains(string(identity.Sources["profile"]), "1.234567890123456789") {
		t.Fatal("source JSON lost numeric precision, null, false, or nested values")
	}
}

func TestRetrieveLargeIdentity(t *testing.T) {
	body := `{"provider":"microsoft","subject":"microsoft:tenant:oid","sources":{"photo":{"data_base64":"` + strings.Repeat("A", 9<<20) + `"}}}`
	c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, "application/json", body), nil })})
	identity, err := c.Retrieve(context.Background(), "code")
	if err != nil || identity == nil || len(identity.RawJSON) != len(body) {
		t.Fatalf("complete identity above 8 MiB was rejected: %v", err)
	}
}

func TestRetrieveFailureResponses(t *testing.T) {
	for _, status := range []int{400, 401, 410, 500, 503, 599} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(status, "application/json", `{"error":{"code":"future_error","message":"private-response-data"},"future":true}`), nil
			})})
			_, err := c.Retrieve(context.Background(), "private-code")
			var apiErr *uniauth.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Code != "future_error" || apiErr.Message != "private-response-data" || apiErr.RequestID != "request-123" {
				t.Fatalf("gateway error was not preserved: %v", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("error text exposed request or response data")
			}
		})
	}
	tests := []struct {
		name, contentType, body, reason string
		status                          int
	}{
		{"redirect", "application/json", identityJSON, "unexpected_status", 302},
		{"created", "application/json", identityJSON, "unexpected_status", 201},
		{"text", "text/html", "private-body", "content_type", 200},
		{"missing-type", "", identityJSON, "content_type", 200},
		{"bad-type", "application/json; bad", identityJSON, "content_type", 200},
		{"invalid-json", "application/json", `{"private-body":`, "json", 200},
		{"trailing-json", "application/json", identityJSON + `{}`, "json", 200},
		{"invalid-utf8", "application/json", string([]byte{'{', '"', 255, '"', ':', '0', '}'}), "json", 200},
		{"array", "application/json", `[]`, "json", 200},
		{"null", "application/json", `null`, "json", 200},
		{"missing-provider", "application/json", `{"subject":"github:1","sources":{}}`, "identity_schema", 200},
		{"unknown-provider", "application/json", `{"provider":"future","subject":"future:1","sources":{}}`, "identity_schema", 200},
		{"bad-provider", "application/json", `{"provider":1,"subject":"github:1","sources":{}}`, "identity_schema", 200},
		{"empty-subject", "application/json", `{"provider":"github","subject":"","sources":{}}`, "identity_schema", 200},
		{"bad-subject", "application/json", `{"provider":"github","subject":1,"sources":{}}`, "identity_schema", 200},
		{"missing-sources", "application/json", `{"provider":"github","subject":"github:1"}`, "identity_schema", 200},
		{"null-sources", "application/json", `{"provider":"github","subject":"github:1","sources":null}`, "identity_schema", 200},
		{"array-sources", "application/json", `{"provider":"github","subject":"github:1","sources":[]}`, "identity_schema", 200},
		{"missing-error", "application/json", `{}`, "error_schema", 410},
		{"null-error", "application/json", `{"error":null}`, "error_schema", 410},
		{"empty-error-code", "application/json", `{"error":{"code":"","message":"x"}}`, "error_schema", 410},
		{"bad-error-code", "application/json", `{"error":{"code":1,"message":"x"}}`, "error_schema", 410},
		{"missing-error-message", "application/json", `{"error":{"code":"x"}}`, "error_schema", 410},
		{"bad-error-message", "application/json", `{"error":{"code":"x","message":false}}`, "error_schema", 410},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(test.status, test.contentType, test.body), nil
			})})
			_, err := c.Retrieve(context.Background(), "private-code")
			var protocolErr *uniauth.ProtocolError
			if !errors.As(err, &protocolErr) || protocolErr.StatusCode != test.status || protocolErr.RequestID != "request-123" || protocolErr.Reason != test.reason {
				t.Fatalf("wrong protocol error: %v", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("protocol error exposed input data")
			}
		})
	}
}

type failingBody struct{ readErr, closeErr error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.readErr }
func (b failingBody) Close() error             { return b.closeErr }

func TestRetrievePreservesCausesWithoutRetry(t *testing.T) {
	for _, bodyFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "network", true: "body"}[bodyFailure], func(t *testing.T) {
			cause := errors.New("private-code-and-data")
			closeCause := errors.New("close failed")
			var calls atomic.Int32
			c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				if !bodyFailure {
					return nil, cause
				}
				r := response(200, "application/json", "")
				r.Body = failingBody{readErr: cause, closeErr: closeCause}
				return r, nil
			})})
			_, err := c.Retrieve(context.Background(), "private-code")
			if !errors.Is(err, cause) || calls.Load() != 1 || strings.Contains(err.Error(), "private") || (bodyFailure && !errors.Is(err, closeCause)) {
				t.Fatalf("cause, request count, or safe message changed: %v", err)
			}
		})
	}
}

func TestRetrieveCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := clientFor(t, server.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Retrieve(ctx, "code"); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("context cancellation lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop retrieval")
	}
}

func TestRetrieveDisablesRedirectWithoutMutatingCaller(t *testing.T) {
	var targetCalls, policyCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(204)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer origin.Close()
	caller := &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { policyCalls.Add(1); return nil }}
	c := clientFor(t, origin.URL, caller)
	_, err := c.Retrieve(context.Background(), "code")
	var protocolErr *uniauth.ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.StatusCode != 302 || targetCalls.Load() != 0 || policyCalls.Load() != 0 {
		t.Fatalf("SDK followed redirect or changed caller policy: %v", err)
	}
	r, err := caller.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if targetCalls.Load() != 1 || policyCalls.Load() != 1 || caller.Timeout != 7*time.Second {
		t.Fatal("caller client was mutated")
	}
}

func TestConcurrentRetrievalDelegatesOneTimeClaim(t *testing.T) {
	var claims atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if claims.Add(1) == 1 {
			io.WriteString(w, identityJSON)
			return
		}
		w.WriteHeader(410)
		io.WriteString(w, `{"error":{"code":"result_unavailable","message":"Unavailable."}}`)
	}))
	defer server.Close()
	c := clientFor(t, server.URL, nil)
	var group sync.WaitGroup
	var successful atomic.Int32
	for range 16 {
		group.Go(func() {
			identity, err := c.Retrieve(context.Background(), "same-code")
			if err == nil && identity != nil {
				successful.Add(1)
				return
			}
			var apiErr *uniauth.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "result_unavailable" {
				t.Errorf("unexpected concurrent claim: %v", err)
			}
		})
	}
	group.Wait()
	if successful.Load() != 1 || claims.Load() != 16 {
		t.Fatalf("claim count: successful=%d requests=%d", successful.Load(), claims.Load())
	}
}

func TestGenerateState(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		state := uniauth.GenerateState()
		bytes, err := base64.RawURLEncoding.DecodeString(state)
		if err != nil || len(bytes) != 32 || seen[state] {
			t.Fatal("state lacks 32 unique URL-safe random bytes")
		}
		seen[state] = true
	}
}

func TestRetrieveRejectsInvalidInputBeforeSending(t *testing.T) {
	var calls int
	c := clientFor(t, "https://auth.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, "application/json", identityJSON), nil
	})})
	for _, code := range []string{"", strings.Repeat("é", 257), string([]byte{255})} {
		if _, err := c.Retrieve(context.Background(), code); err == nil {
			t.Fatal("invalid code was accepted")
		}
	}
	if _, err := c.Retrieve(nil, "code"); err == nil || calls != 0 {
		t.Fatal("invalid input sent a request")
	}
}

func ExampleParseCallback() {
	query := url.Values{"state": {"browser-bound-state"}, "code": {"one-time-result"}}
	code, err := uniauth.ParseCallback(query, "browser-bound-state")
	if err != nil {
		panic(err)
	}
	fmt.Println(code)
	// Output: one-time-result
}
