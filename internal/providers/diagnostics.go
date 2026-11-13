package providers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

type diagnosticKey struct{}
type stageKey struct{}

type diagnosticOperation struct {
	logger  *slog.Logger
	started time.Time
}

// WithLogger correlates provider diagnostics with the caller's operation fields.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, diagnosticKey{}, &diagnosticOperation{logger: logger, started: time.Now()})
}

type diagnosticStage struct {
	operation *diagnosticOperation
	name      string
	started   time.Time
	mu        sync.Mutex
	fields    map[string]any
	bodyStart time.Time
}

func observe[T any](ctx context.Context, name string, run func(context.Context) (T, error)) (value T, err error) {
	operation, ok := ctx.Value(diagnosticKey{}).(*diagnosticOperation)
	if !ok {
		return run(ctx)
	}
	stage := &diagnosticStage{operation: operation, name: name, started: time.Now(), fields: make(map[string]any)}
	stage.log(ctx, "started", nil)
	ctx = context.WithValue(ctx, stageKey{}, stage)
	ctx = httptrace.WithClientTrace(ctx, stage.trace())
	defer func() { stage.log(ctx, "completed", err) }()
	return run(ctx)
}

func (s *diagnosticStage) log(ctx context.Context, phase string, err error) {
	now := time.Now()
	fields := []any{"component", "provider", "stage", s.name, "phase", phase, "elapsed_ms", now.Sub(s.operation.started).Milliseconds()}
	if deadline, ok := ctx.Deadline(); ok {
		fields = append(fields, "budget_remaining_ms", deadline.Sub(now).Milliseconds())
	}
	if phase == "completed" {
		outcome := "success"
		if err != nil {
			outcome = "failed"
			fields = append(fields, "error_cause", diagnosticCause(err), "error_type", diagnosticErrorType(err))
		}
		fields = append(fields, "duration_ms", now.Sub(s.started).Milliseconds(), "outcome", outcome)
		s.mu.Lock()
		for key, value := range s.fields {
			fields = append(fields, key, value)
		}
		s.mu.Unlock()
	}
	s.operation.logger.InfoContext(ctx, "provider operation", fields...)
}

func (s *diagnosticStage) record(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.fields[key]; !exists {
		s.fields[key] = value
	}
}

func (s *diagnosticStage) event(key string) { s.record(key, time.Since(s.started).Milliseconds()) }

func (s *diagnosticStage) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { s.event("dns_start_ms") },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			s.event("dns_done_ms")
			if info.Err != nil {
				s.record("dns_error_type", diagnosticErrorType(info.Err))
			}
		},
		ConnectStart: func(string, string) { s.event("connect_start_ms") },
		ConnectDone: func(_, _ string, err error) {
			s.event("connect_done_ms")
			if err != nil {
				s.record("connect_error_type", diagnosticErrorType(err))
			}
		},
		TLSHandshakeStart: func() { s.event("tls_start_ms") },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			s.event("tls_done_ms")
			if err != nil {
				s.record("tls_error_type", diagnosticErrorType(err))
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			s.event("connection_ms")
			s.record("connection_reused", info.Reused)
			if address, ok := info.Conn.RemoteAddr().(*net.TCPAddr); ok && address.IP != nil {
				family := "ipv6"
				if address.IP.To4() != nil {
					family = "ipv4"
				}
				s.record("remote_ip", address.IP.String())
				s.record("remote_family", family)
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			s.event("request_written_ms")
			if info.Err != nil {
				s.record("write_error_type", diagnosticErrorType(info.Err))
			}
		},
		GotFirstResponseByte: func() { s.event("first_byte_ms") },
	}
}

func (s *diagnosticStage) response(response *http.Response) {
	s.event("headers_ms")
	s.record("status", response.StatusCode)
	s.record("protocol", response.Proto)
}

func (s *diagnosticStage) readStarted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bodyStart.IsZero() {
		s.bodyStart = time.Now()
	}
}

func (s *diagnosticStage) readFinished(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.fields["body_complete_ms"]; !exists {
		now := time.Now()
		s.fields["body_complete_ms"] = now.Sub(s.started).Milliseconds()
		s.fields["body_duration_ms"] = now.Sub(s.bodyStart).Milliseconds()
		if !errors.Is(err, io.EOF) {
			s.fields["body_error_type"] = diagnosticErrorType(err)
		}
	}
}

type diagnosticBody struct {
	io.ReadCloser
	stage *diagnosticStage
}

func (b diagnosticBody) Read(body []byte) (int, error) {
	b.stage.readStarted()
	n, err := b.ReadCloser.Read(body)
	b.stage.readFinished(err)
	return n, err
}

func diagnosticCause(err error) string {
	var networkError net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &networkError) && networkError.Timeout():
		return "network_timeout"
	default:
		return "operation_failed"
	}
}

func diagnosticErrorType(err error) string {
	var dnsError *net.DNSError
	var networkError *net.OpError
	var certificateError *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	var oauthError *oauth2.RetrieveError
	var httpError statusError
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, errResponseTooLarge):
		return "response_too_large"
	case errors.As(err, &dnsError):
		return "dns"
	case errors.As(err, &certificateError), errors.As(err, &unknownAuthority), errors.As(err, &hostnameError), errors.As(err, &invalidCertificate):
		return "tls_certificate"
	case errors.As(err, &networkError):
		return "network"
	case errors.As(err, &oauthError):
		return "oauth_response"
	case errors.As(err, &httpError):
		return "http_status"
	case errors.As(err, &syntaxError), errors.As(err, &typeError):
		return "json"
	default:
		return "operation"
	}
}
