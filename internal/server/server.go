package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/auth"
	"github.com/codetreker/uniauth/internal/console"
	"github.com/codetreker/uniauth/internal/providers"
	consoleweb "github.com/codetreker/uniauth/web/console"
)

const shutdownTimeout = 15 * time.Second

type Service struct {
	store     *apps.Store
	auth      *auth.Handler
	console   *console.Handler
	handler   http.Handler
	logger    *slog.Logger
	closeOnce sync.Once
	closeErr  error
}

func New(ctx context.Context, cfg Config, registry map[string]providers.Provider, logger *slog.Logger) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		return nil, errors.New("server logger is required")
	}
	var err error
	if registry == nil {
		registry, err = buildRegistry(ctx, cfg)
		if err != nil {
			return nil, err
		}
	}
	if len(registry) == 0 {
		return nil, errors.New("at least one OAuth provider must be available")
	}
	store, err := apps.Open(ctx, apps.Config{Path: cfg.DatabasePath, AllowLoopbackHTTP: cfg.AllowLoopbackHTTP})
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*Service, error) { return nil, errors.Join(cause, store.Close()) }
	available := make([]string, 0, len(registry))
	for name := range registry {
		available = append(available, name)
	}
	sort.Strings(available)
	// The authentication cache remains live until accepted HTTP requests drain.
	authHandler, err := auth.New(context.WithoutCancel(ctx), auth.Config{
		PublicURL: cfg.PublicURL, AllowLoopbackHTTP: cfg.AllowLoopbackHTTP,
		TransactionTTL: 10 * time.Minute, CallbackTimeout: 30 * time.Second, CleanupInterval: time.Minute,
		MaxTransactions: 10000, MaxTransactionBytes: 32 << 20, MaxResults: 10000, MaxResultBytes: 64 << 20, Logger: logger,
	}, store, registry)
	if err != nil {
		return fail(err)
	}
	fail = func(cause error) (*Service, error) {
		return nil, errors.Join(cause, authHandler.Close(), store.Close())
	}
	if err := store.InitializePlatform(ctx, cfg.InitialAdminEmail); err != nil {
		return fail(err)
	}
	consoleApp, err := store.ProvisionConsole(ctx, strings.TrimSuffix(cfg.PublicURL, "/")+"/console/auth/callback", available)
	if err != nil {
		return fail(err)
	}
	admin, err := console.New(store, available, console.Config{
		PublicURL: cfg.PublicURL, Gateway: authHandler, AppID: consoleApp.App.ID, AppSecret: consoleApp.Secret,
		AllowLoopbackHTTP: cfg.AllowLoopbackHTTP, Logger: logger,
	})
	if err != nil {
		return fail(err)
	}
	mux := http.NewServeMux()
	authHandler.Register(mux)
	admin.Register(mux)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := store.Ping(r.Context()); err != nil {
			http.Error(w, "Service unavailable.", http.StatusServiceUnavailable)
			return
		}
		health(w, r)
	})
	mux.Handle("/console/", consoleweb.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			http.Redirect(w, r, "/console/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	service := &Service{store: store, auth: authHandler, console: admin, logger: logger}
	service.handler = accessLog(http.MaxBytesHandler(mux, 1<<20), logger)
	return service, nil
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
}

func buildRegistry(ctx context.Context, cfg Config) (map[string]providers.Provider, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	registry := make(map[string]providers.Provider)
	for _, entry := range []struct {
		name        string
		credentials Credentials
	}{{"microsoft", cfg.Microsoft}, {"google", cfg.Google}, {"github", cfg.GitHub}} {
		if entry.credentials.ClientID == "" {
			continue
		}
		providerCfg := providers.Config{ClientID: entry.credentials.ClientID, ClientSecret: entry.credentials.ClientSecret, RedirectURL: strings.TrimSuffix(cfg.PublicURL, "/") + "/auth/callback/" + entry.name, HTTPClient: client}
		var provider providers.Provider
		var err error
		switch entry.name {
		case "microsoft":
			provider, err = providers.NewMicrosoft(ctx, providerCfg)
		case "google":
			provider, err = providers.NewGoogle(ctx, providerCfg)
		case "github":
			provider, err = providers.NewGitHub(providerCfg)
		}
		if err != nil {
			return nil, fmt.Errorf("initialize %s provider: %w", entry.name, err)
		}
		registry[entry.name] = provider
	}
	return registry, nil
}

func (s *Service) Handler() http.Handler { return s.handler }

func (s *Service) Close() error {
	s.closeOnce.Do(func() { s.closeErr = errors.Join(s.console.Close(), s.auth.Close(), s.store.Close()) })
	return s.closeErr
}

func (s *Service) Serve(ctx context.Context, listener net.Listener) error {
	httpServer := &http.Server{Handler: s.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(transportLog{s.logger}, "", 0)}
	finished := make(chan error, 1)
	go func() { finished <- httpServer.Serve(listener) }()
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return errors.Join(err, s.Close())
	case <-ctx.Done():
		drainCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := httpServer.Shutdown(drainCtx)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, httpServer.Close())
		}
		serveErr := <-finished
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr, s.Close())
	}
}

type responseLog struct {
	http.ResponseWriter
	status int
}

func (w *responseLog) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseLog) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}
func (w *responseLog) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func accessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("X-Request-ID", rand.Text())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		record := &responseLog{ResponseWriter: w}
		defer func() {
			if recover() != nil {
				logger.ErrorContext(r.Context(), "HTTP handler failed", "request_id", w.Header().Get("X-Request-ID"))
				if record.status == 0 {
					http.Error(record, "Internal server error.", http.StatusInternalServerError)
				}
			}
			status := record.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.InfoContext(r.Context(), "HTTP request completed", "request_id", w.Header().Get("X-Request-ID"), "method", safeMethod(r.Method), "operation", operation(r.URL.Path), "status", status, "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(record, r)
	})
}

func safeMethod(method string) string {
	switch method {
	case "GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS":
		return method
	default:
		return "other"
	}
}
func operation(path string) string {
	switch {
	case path == "/healthz":
		return "health"
	case path == "/readyz":
		return "readiness"
	case path == "/auth/authorize":
		return "authorize"
	case strings.HasPrefix(path, "/auth/callback/"):
		return "callback"
	case path == "/auth/result":
		return "result"
	case path == "/console/auth/callback":
		return "console_callback"
	case strings.HasPrefix(path, "/console/api/"):
		return "console_api"
	case strings.HasPrefix(path, "/console/"):
		return "console_static"
	default:
		return "unknown"
	}
}

// net/http diagnostics can include connection details and untrusted input.
type transportLog struct{ logger *slog.Logger }

func (w transportLog) Write(body []byte) (int, error) {
	w.logger.Error("HTTP transport failed", "component", "server")
	return len(body), nil
}
