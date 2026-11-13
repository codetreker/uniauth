package providers

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func diagnosticRecords(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func diagnosticRecord(t *testing.T, records []map[string]any, stage, phase string) map[string]any {
	t.Helper()
	for _, record := range records {
		if record["stage"] == stage && record["phase"] == phase {
			return record
		}
	}
	t.Fatalf("missing %s %s diagnostic: %v", stage, phase, records)
	return nil
}

func replaceDiagnosticServer(t *testing.T, f *fixture, handler http.Handler, secure bool) {
	t.Helper()
	f.server.Close()
	if secure {
		f.server = httptest.NewTLSServer(handler)
	} else {
		f.server = httptest.NewServer(handler)
	}
	f.issuer = f.server.URL
	f.claims["iss"] = f.issuer
	t.Cleanup(f.server.Close)
}

func TestMicrosoftDiagnosticsCorrelateWithoutIdentityOrCredentials(t *testing.T) {
	f := newFixture(t)
	replaceDiagnosticServer(t, f, f.server.Config.Handler, true)
	provider := f.provider(t, "microsoft")
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil)).With("request_id", "request-one", "operation_id", "operation-one", "app_id", "app-one", "provider", "microsoft")
	identity, err := provider.Exchange(WithLogger(context.Background(), logger), exchangeRequest())
	if err != nil || identity.ID != "msft:object-123@"+microsoftTenant || identity.AvatarURL == nil {
		t.Fatalf("diagnostics changed identity: %+v %v", identity, err)
	}
	records := diagnosticRecords(t, &logs)
	if len(records) != 8 {
		t.Fatalf("expected bounded start/completion events for four stages: %v", records)
	}
	for _, record := range records {
		if record["request_id"] != "request-one" || record["operation_id"] != "operation-one" || record["app_id"] != "app-one" || record["provider"] != "microsoft" || record["component"] != "provider" {
			t.Fatalf("missing correlation: %v", record)
		}
		if _, ok := record["budget_remaining_ms"].(float64); !ok {
			t.Fatalf("missing provider deadline: %v", record)
		}
	}
	token := diagnosticRecord(t, records, "token_exchange", "completed")
	if token["status"] != float64(200) || token["protocol"] != "HTTP/1.1" || token["tls_done_ms"] == nil || token["body_complete_ms"] == nil || token["connection_reused"] != false || net.ParseIP(token["remote_ip"].(string)) == nil || token["remote_family"] != "ipv4" {
		t.Fatalf("missing observed TLS/HTTP details: %v", token)
	}
	profile := diagnosticRecord(t, records, "profile_retrieval", "completed")
	if profile["status"] != float64(200) || profile["connection_reused"] != true || profile["request_written_ms"] == nil || profile["first_byte_ms"] == nil || profile["headers_ms"] == nil || profile["body_duration_ms"] == nil {
		t.Fatalf("missing profile transport details: %v", profile)
	}
	verified := diagnosticRecord(t, records, "identity_verification", "completed")
	if verified["status"] != nil || verified["connection_reused"] != nil || verified["duration_ms"] == nil || verified["outcome"] != "success" {
		t.Fatalf("verification fabricated HTTP observations: %v", verified)
	}
	for _, secret := range []string{"access-secret", "refresh-secret", "client-secret", "valid-code", "expected-nonce", "user-123", "object-123", "someone", f.server.URL} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("diagnostics disclosed %q: %s", secret, &logs)
		}
	}
}

func TestMicrosoftDiagnosticsLocateProfileDeadline(t *testing.T) {
	for _, bodyStarted := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[bodyStarted], func(t *testing.T) {
			f := newFixture(t)
			base := f.server.Config.Handler
			entered := make(chan struct{})
			replaceDiagnosticServer(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/profile" {
					base.ServeHTTP(w, r)
					return
				}
				if bodyStarted {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, "{")
					w.(http.Flusher).Flush()
				}
				close(entered)
				<-r.Context().Done()
			}), false)
			provider := f.provider(t, "microsoft")
			var logs bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := provider.Exchange(WithLogger(ctx, slog.New(slog.NewJSONHandler(&logs, nil))), exchangeRequest())
				result <- err
			}()
			select {
			case <-entered:
			case err := <-result:
				t.Fatalf("profile stage not reached: %v", err)
			}
			err := <-result
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline cause lost: %v", err)
			}
			records := diagnosticRecords(t, &logs)
			start := diagnosticRecord(t, records, "profile_retrieval", "started")
			finish := diagnosticRecord(t, records, "profile_retrieval", "completed")
			if start["budget_remaining_ms"].(float64) <= 0 || finish["error_cause"] != "deadline_exceeded" || finish["error_type"] != "context_deadline" || finish["outcome"] != "failed" {
				t.Fatalf("missing deadline/budget evidence: start=%v finish=%v", start, finish)
			}
			if bodyStarted {
				if finish["status"] != float64(200) || finish["protocol"] != "HTTP/1.1" || finish["body_error_type"] != "context_deadline" || finish["body_duration_ms"] == nil {
					t.Fatalf("lost headers during body stall: %v", finish)
				}
			} else if finish["status"] != nil || finish["protocol"] != nil || finish["headers_ms"] != nil || finish["body_complete_ms"] != nil {
				t.Fatalf("fabricated response for header stall: %v", finish)
			}
			if diagnosticRecord(t, records, "token_exchange", "completed")["outcome"] != "success" || diagnosticRecord(t, records, "identity_verification", "completed")["outcome"] != "success" {
				t.Fatal("earlier successful stages were lost")
			}
		})
	}
}

func TestMicrosoftDiagnosticsPhotoAbsenceAndSafeStatusFailure(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newFixture(t)
			f.status["/photo"] = status
			var logs bytes.Buffer
			_, err := f.provider(t, "microsoft").Exchange(WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil))), exchangeRequest())
			finish := diagnosticRecord(t, diagnosticRecords(t, &logs), "photo_retrieval", "completed")
			if finish["status"] != float64(status) || finish["body_complete_ms"] != nil || strings.Contains(logs.String(), "provider-secret-sensitive-description") {
				t.Fatalf("unsafe or fabricated status details: %v", finish)
			}
			if status == http.StatusNotFound {
				if err != nil || finish["outcome"] != "success" || finish["error_type"] != nil {
					t.Fatalf("normal photo absence became a failure: %v %v", finish, err)
				}
			} else if err == nil || finish["outcome"] != "failed" || finish["error_type"] != "http_status" {
				t.Fatalf("photo failure was hidden: %v %v", finish, err)
			}
		})
	}
}

func TestMicrosoftDiagnosticsIncludeSigningKeyWaitInSharedBudget(t *testing.T) {
	f := newFixture(t)
	f.jwksDelay = 25 * time.Millisecond
	var logs bytes.Buffer
	_, err := f.provider(t, "microsoft").Exchange(WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil))), exchangeRequest())
	if err != nil {
		t.Fatal(err)
	}
	records := diagnosticRecords(t, &logs)
	verification := diagnosticRecord(t, records, "identity_verification", "completed")
	tokenStart := diagnosticRecord(t, records, "token_exchange", "started")
	profileStart := diagnosticRecord(t, records, "profile_retrieval", "started")
	if verification["duration_ms"].(float64) < 20 || profileStart["elapsed_ms"].(float64) < 20 || tokenStart["budget_remaining_ms"].(float64)-profileStart["budget_remaining_ms"].(float64) < 20 {
		t.Fatalf("shared key-verification wait lost: token=%v verification=%v profile=%v", tokenStart, verification, profileStart)
	}
}

func TestDiagnosticsReportObservedConnectionAndTLSFailures(t *testing.T) {
	for _, mode := range []string{"connection", "tls", "write"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(server.Close)
			endpoint := server.URL
			client := &http.Client{Timeout: time.Second}
			switch mode {
			case "connection":
				server.Close()
				endpoint = strings.Replace(endpoint, "127.0.0.1", "localhost", 1)
			case "write":
				client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
					err := errors.New("PRIVATE_WRITE_FAILURE")
					httptrace.ContextClientTrace(request.Context()).WroteRequest(httptrace.WroteRequestInfo{Err: err})
					return nil, err
				})
			}
			client = boundedClient(client)
			var logs bytes.Buffer
			ctx := WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
			_, err := observe(ctx, "profile_retrieval", func(ctx context.Context) ([]byte, error) {
				body, _, _, err := fetch(ctx, client, endpoint, "PRIVATE_ACCESS_TOKEN")
				return body, err
			})
			if err == nil {
				t.Fatal("transport failure was hidden")
			}
			finish := diagnosticRecord(t, diagnosticRecords(t, &logs), "profile_retrieval", "completed")
			if finish["status"] != nil || finish["protocol"] != nil || strings.Contains(logs.String(), "PRIVATE_") || strings.Contains(logs.String(), endpoint) {
				t.Fatalf("unsafe or fabricated HTTP metadata: %v", finish)
			}
			switch mode {
			case "connection":
				if finish["dns_start_ms"] == nil || finish["dns_done_ms"] == nil || finish["connect_start_ms"] == nil || finish["connect_error_type"] != "network" {
					t.Fatalf("missing connection evidence: %v", finish)
				}
			case "tls":
				if finish["tls_start_ms"] == nil || finish["tls_done_ms"] == nil || finish["tls_error_type"] != "tls_certificate" {
					t.Fatalf("missing TLS evidence: %v", finish)
				}
			case "write":
				if finish["request_written_ms"] == nil || finish["write_error_type"] != "operation" {
					t.Fatalf("missing write evidence: %v", finish)
				}
			}
		})
	}
}

func TestDiagnosticErrorMetadataIsAllowlisted(t *testing.T) {
	cases := []struct {
		cause error
		kind  string
		group string
	}{
		{context.Canceled, "context_canceled", "canceled"},
		{context.DeadlineExceeded, "context_deadline", "deadline_exceeded"},
		{&net.DNSError{Err: "PRIVATE_DNS", IsTimeout: true}, "dns", "network_timeout"},
		{&net.OpError{Op: "PRIVATE_OPERATION", Err: errors.New("PRIVATE_NETWORK")}, "network", "operation_failed"},
		{x509.UnknownAuthorityError{}, "tls_certificate", "operation_failed"},
		{&oauth2.RetrieveError{Body: []byte("PRIVATE_TOKEN")}, "oauth_response", "operation_failed"},
		{statusError{status: 403}, "http_status", "operation_failed"},
		{errResponseTooLarge, "response_too_large", "operation_failed"},
		{&json.SyntaxError{}, "json", "operation_failed"},
		{&json.UnmarshalTypeError{}, "json", "operation_failed"},
		{errors.New("PRIVATE_DETAIL"), "operation", "operation_failed"},
	}
	var logs bytes.Buffer
	ctx := WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	for _, item := range cases {
		_, err := observe(ctx, "profile_retrieval", func(context.Context) (int, error) {
			return 0, item.cause
		})
		if err != item.cause {
			t.Fatal("diagnostics replaced the cause")
		}
		records := diagnosticRecords(t, &logs)
		finish := records[len(records)-1]
		if finish["error_type"] != item.kind || finish["error_cause"] != item.group || finish["budget_remaining_ms"] != nil || finish["status"] != nil {
			t.Fatalf("unexpected classification: %v", finish)
		}
	}
	if strings.Contains(logs.String(), "PRIVATE_") {
		t.Fatalf("raw error details escaped: %s", &logs)
	}
}
