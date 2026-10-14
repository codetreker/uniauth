package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/codetreker/uniauth/internal/providers"
	"github.com/codetreker/uniauth/internal/server"
)

const origin = "http://127.0.0.1:18080"

type flow struct {
	request providers.AuthorizationRequest
	expires time.Time
}

type issuedCode struct {
	flow
	persona string
}

type fixtureProvider struct {
	name    string
	mu      sync.Mutex
	tickets map[string]flow
	codes   map[string]issuedCode
}

func (p *fixtureProvider) Name() string { return p.name }

func (p *fixtureProvider) AuthorizationURL(request providers.AuthorizationRequest) (string, error) {
	if request.State == "" || request.Nonce == "" || len(request.CodeVerifier) < 43 {
		return "", errors.New("incomplete fixture authorization request")
	}
	ticket := rand.Text()
	p.mu.Lock()
	p.tickets[ticket] = flow{request: request, expires: time.Now().Add(time.Minute)}
	p.mu.Unlock()
	return origin + "/fixture/oauth/" + p.name + "?ticket=" + url.QueryEscape(ticket), nil
}

func (p *fixtureProvider) Exchange(_ context.Context, request providers.ExchangeRequest) (providers.Identity, error) {
	p.mu.Lock()
	code, ok := p.codes[request.Code]
	delete(p.codes, request.Code)
	p.mu.Unlock()
	if !ok || !time.Now().Before(code.expires) || request.ExpectedNonce != code.request.Nonce || request.CodeVerifier != code.request.CodeVerifier {
		return providers.Identity{}, errors.New("fixture code is unavailable or does not match its authorization")
	}
	name, email := "Alice Developer", "alice@example.com"
	switch code.persona {
	case "admin":
		name, email = "Platform Administrator", "sychenjianjun@gmail.com"
	case "user-b":
		name, email = "Bob Developer", "bob@example.com"
	case "no-email":
		name = "Microsoft Developer"
	case "same-email":
		name = "Separate Developer"
	}
	verified := true
	identity := providers.Identity{Provider: p.name, ID: p.name + ":" + code.persona, Name: &name, Email: &email, EmailVerified: &verified, Origin: map[string]json.RawMessage{"fixture": json.RawMessage(`{"source":"browser-provider"}`)}}
	if p.name == "microsoft" {
		identity.EmailVerified = nil
	}
	if code.persona == "no-email" {
		identity.Email, identity.EmailVerified = nil, nil
	}
	return identity, nil
}

var chooser = template.Must(template.New("provider").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Browser OAuth provider</title></head><body><h1>Fixture identity for {{.Provider}}</h1><form method="post"><input type="hidden" name="ticket" value="{{.Ticket}}"><button name="persona" value="admin">Administrator</button><button name="persona" value="user-a">User A</button><button name="persona" value="user-b">User B</button><button name="persona" value="no-email">Identity without email</button><button name="persona" value="same-email">Separate identity with same email</button><button name="persona" value="cancel">Cancel provider login</button></form></body></html>`))

func (p *fixtureProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid fixture form", http.StatusBadRequest)
		return
	}
	ticket := r.Form.Get("ticket")
	p.mu.Lock()
	pending, exists := p.tickets[ticket]
	if !exists || !time.Now().Before(pending.expires) {
		p.mu.Unlock()
		http.Error(w, "Fixture authorization expired", http.StatusGone)
		return
	}
	if r.Method == http.MethodGet {
		p.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := chooser.Execute(w, struct{ Provider, Ticket string }{p.name, ticket}); err != nil {
			slog.Error("render provider fixture", "error", err)
		}
		return
	}
	persona := r.PostForm.Get("persona")
	switch persona {
	case "admin", "user-a", "user-b", "no-email", "same-email", "cancel":
	default:
		p.mu.Unlock()
		http.Error(w, "Unknown fixture identity", http.StatusBadRequest)
		return
	}
	delete(p.tickets, ticket)
	query := url.Values{"state": {pending.request.State}}
	if persona == "cancel" {
		query.Set("error", "access_denied")
	} else {
		code := rand.Text()
		p.codes[code] = issuedCode{flow: pending, persona: persona}
		query.Set("code", code)
	}
	p.mu.Unlock()
	http.Redirect(w, r, origin+"/auth/callback/"+p.name+"?"+query.Encode(), http.StatusSeeOther)
}

func run() (result error) {
	path := os.Getenv("UNIAUTH_DATABASE_PATH")
	if path == "" {
		return errors.New("UNIAUTH_DATABASE_PATH is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	registry := make(map[string]providers.Provider)
	mux := http.NewServeMux()
	for _, name := range []string{"microsoft", "google", "github"} {
		provider := &fixtureProvider{name: name, tickets: make(map[string]flow), codes: make(map[string]issuedCode)}
		registry[name] = provider
		mux.Handle("/fixture/oauth/"+name, provider)
	}
	// Browsers remove decimal-port padding when serializing their Origin.
	service, err := server.New(ctx, server.Config{PublicURL: "http://127.0.0.1:018080", ListenAddr: "127.0.0.1:18080", DatabasePath: path, InitialAdminEmail: "sychenjianjun@gmail.com", AllowLoopbackHTTP: true}, registry, slog.Default())
	if err != nil {
		return fmt.Errorf("initialize browser service: %w", err)
	}
	defer func() { result = errors.Join(result, service.Close()) }()
	mux.Handle("/", service.Handler())
	httpServer := &http.Server{Addr: "127.0.0.1:18080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	finished := make(chan error, 1)
	go func() { finished <- httpServer.ListenAndServe() }()
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdown); err != nil {
			return errors.Join(err, httpServer.Close())
		}
		err := <-finished
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
