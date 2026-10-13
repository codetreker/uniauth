package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/providers"
)

type Handler struct {
	config      Config
	apps        *apps.Store
	providers   map[string]providers.Provider
	cache       *transientStore
	secure      bool
	ctx         context.Context
	cancel      context.CancelFunc
	cleanupDone chan struct{}
}

type requestMetadata struct {
	requestID   string
	operationID string
	appID       string
	provider    string
}

type metadataKey struct{}

func New(ctx context.Context, config Config, store *apps.Store, registry map[string]providers.Provider) (*Handler, error) {
	if ctx == nil || store == nil {
		return nil, errors.New("authentication context and application store are required")
	}
	secure, err := config.validate()
	if err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	owned := make(map[string]providers.Provider, len(registry))
	for name, provider := range registry {
		if provider == nil || provider.Name() != name || !slices.Contains([]string{"microsoft", "google", "github"}, name) {
			return nil, errors.New("authentication provider registry is invalid")
		}
		owned[name] = provider
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	h := &Handler{config: config, apps: store, providers: owned, cache: newTransientStore(config), secure: secure, ctx: serviceCtx, cancel: cancel, cleanupDone: make(chan struct{})}
	go h.cleanup()
	return h, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/authorize", h.authorize)
	mux.HandleFunc("GET /auth/callback/{provider}", h.callback)
	mux.HandleFunc("POST /auth/result", h.retrieve)
}

func (h *Handler) Close() error {
	h.cancel()
	h.cache.close()
	<-h.cleanupDone
	return nil
}

func (h *Handler) cleanup() {
	defer close(h.cleanupDone)
	ticker := time.NewTicker(h.config.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			h.cache.close()
			return
		case <-ticker.C:
			txCount, resultCount := h.cache.cleanup(h.config.Now())
			if txCount != 0 || resultCount != 0 {
				h.config.Logger.InfoContext(h.ctx, "authentication state expired", "component", "auth", "phase", "cleanup", "transactions", txCount, "results", resultCount)
			}
		}
	}
}

func (h *Handler) begin(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	requestID := token(16)
	r = r.WithContext(context.WithValue(r.Context(), metadataKey{}, requestMetadata{requestID: requestID}))
	w.Header().Set("X-Request-ID", requestID)
	if h.ctx.Err() != nil {
		h.fail(w, r, http.StatusServiceUnavailable, "service_unavailable", "Authentication is unavailable.")
		return r, false
	}
	return r, true
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	r, ok := h.begin(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		h.fail(w, r, 400, "invalid_request", "The authentication request is invalid.")
		return
	}
	appID, a := single(query, "app_id", 128)
	name, b := single(query, "provider", 32)
	redirectURI, c := single(query, "redirect_uri", 2048)
	productState, d := single(query, "state", 2048)
	if !a || !b || !c || !d {
		h.fail(w, r, 400, "invalid_request", "The authentication request is invalid.")
		return
	}
	provider, available := h.providers[name]
	if !available {
		h.fail(w, r, 400, "invalid_request", "The selected provider is unavailable.")
		return
	}
	app, err := h.apps.Get(r.Context(), appID)
	if err != nil {
		if errors.Is(err, apps.ErrNotFound) {
			h.fail(w, r, 400, "invalid_request", "The application is unavailable.")
		} else {
			h.fail(w, r, 500, "internal_error", "Authentication could not be completed.", err)
		}
		return
	}
	if !eligible(app, name, redirectURI) {
		h.fail(w, r, 400, "invalid_request", "The application does not allow this authentication request.")
		return
	}
	state := token(32)
	binding := token(32)
	cookieSuffix := token(16)
	nonce := token(32)
	verifier := token(32)
	operationID := token(16)
	cookieName := "uniauth-flow-" + cookieSuffix
	if h.secure {
		cookieName = "__Host-" + cookieName
	}
	tx := transaction{appID: app.ID, provider: name, redirectURI: redirectURI, productState: productState, cookieName: cookieName, browserBinding: binding, nonce: nonce, verifier: verifier, operationID: operationID, expires: h.config.Now().Add(h.config.TransactionTTL)}
	tx.size = int64(len(state) + len(app.ID) + len(name) + len(redirectURI) + len(productState) + len(cookieName) + len(binding) + len(nonce) + len(verifier) + len(operationID))
	r = withOperation(r, tx)
	authorizationURL, err := provider.AuthorizationURL(providers.AuthorizationRequest{State: state, Nonce: nonce, CodeVerifier: verifier})
	if err != nil {
		h.fail(w, r, 502, "provider_failed", "The provider could not start authentication.", err)
		return
	}
	if err := h.cache.addTransaction(state, tx, h.config.Now()); err != nil {
		h.cacheError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: binding, Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: int((h.config.TransactionTTL + time.Second - 1) / time.Second)})
	h.log(r, "provider_redirect", "")
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	r, ok := h.begin(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		h.fail(w, r, 400, "invalid_request", "The provider callback is invalid.")
		return
	}
	state, validState := single(query, "state", 512)
	code, hasCode := single(query, "code", 4096)
	_, hasError := single(query, "error", 256)
	if !validState || hasCode == hasError || (!hasCode && !hasError) || (hasCode && len(query["error"]) != 0) || (hasError && len(query["code"]) != 0) {
		h.fail(w, r, 400, "invalid_request", "The provider callback is invalid.")
		return
	}
	name := r.PathValue("provider")
	provider, exists := h.providers[name]
	if !exists {
		h.fail(w, r, 410, "transaction_unavailable", "The authentication transaction is unavailable.")
		return
	}
	cookies := make(map[string]string)
	duplicate := make(map[string]bool)
	for _, cookie := range r.Cookies() {
		if _, seen := cookies[cookie.Name]; seen {
			duplicate[cookie.Name] = true
		}
		cookies[cookie.Name] = cookie.Value
	}
	for name := range duplicate {
		delete(cookies, name)
	}
	tx, err := h.cache.claimTransaction(state, name, cookies, h.config.Now())
	if err != nil {
		h.cacheError(w, r, err)
		return
	}
	defer h.cache.finishTransaction(tx)
	r = withOperation(r, tx)
	http.SetCookie(w, &http.Cookie{Name: tx.cookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	ctx, cancel := context.WithTimeout(r.Context(), h.config.CallbackTimeout)
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	defer cancel()
	if !h.recheck(w, r.WithContext(ctx), tx) {
		return
	}
	if hasError {
		h.log(r, "provider_denied", "provider_denied")
		productRedirect(w, r, tx, "error", "provider_denied")
		return
	}
	identity, err := provider.Exchange(ctx, providers.ExchangeRequest{Code: code, ExpectedNonce: tx.nonce, CodeVerifier: tx.verifier})
	if err != nil || ctx.Err() != nil {
		h.log(r, "provider_exchange", "provider_failed", errors.Join(err, ctx.Err()))
		productRedirect(w, r, tx, "error", "provider_failed")
		return
	}
	if identity.Provider != name || identity.ID == "" || identity.Origin == nil {
		h.log(r, "provider_identity", "provider_failed")
		productRedirect(w, r, tx, "error", "provider_failed")
		return
	}
	payload, err := json.Marshal(identity)
	if err != nil {
		h.log(r, "result_serialize", "provider_failed", err)
		productRedirect(w, r, tx, "error", "provider_failed")
		return
	}
	if !h.recheck(w, r.WithContext(ctx), tx) {
		return
	}
	resultCode := token(32)
	created := h.config.Now()
	entry := result{appID: tx.appID, redirectURI: tx.redirectURI, operationID: tx.operationID, provider: tx.provider, payload: payload, expires: created.Add(ResultTTL)}
	entry.size = int64(len(payload) + len(resultCode) + len(entry.appID) + len(entry.redirectURI) + len(entry.operationID) + len(entry.provider))
	if err := h.cache.addResult(resultCode, entry, created); err != nil {
		if errors.Is(err, ErrCapacity) {
			h.log(r, "result_create", "capacity_exceeded")
			productRedirect(w, r, tx, "error", "capacity_exceeded")
		} else {
			h.cacheError(w, r, err)
		}
		return
	}
	h.log(r, "result_create", "")
	productRedirect(w, r, tx, "code", resultCode)
}

func (h *Handler) retrieve(w http.ResponseWriter, r *http.Request) {
	r, ok := h.begin(w, r)
	if !ok {
		return
	}
	appID, secret, basic := r.BasicAuth()
	if !basic || len(appID) > 128 || len(secret) > 512 || appID == "" || secret == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="uniauth"`)
		h.fail(w, r, 401, "invalid_app", "Application authentication failed.")
		return
	}
	app, err := h.apps.Authenticate(r.Context(), appID, secret)
	if err != nil {
		if errors.Is(err, apps.ErrUnauthorized) {
			w.Header().Set("WWW-Authenticate", `Basic realm="uniauth"`)
			h.fail(w, r, 401, "invalid_app", "Application authentication failed.")
		} else {
			h.fail(w, r, 500, "internal_error", "Authentication could not be completed.", err)
		}
		return
	}
	metadata := r.Context().Value(metadataKey{}).(requestMetadata)
	metadata.appID = app.ID
	r = r.WithContext(context.WithValue(r.Context(), metadataKey{}, metadata))
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		h.fail(w, r, 400, "invalid_request", "The result request requires application/json.")
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	body := http.MaxBytesReader(w, r.Body, 2048)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		h.fail(w, r, 400, "invalid_request", "The result request is invalid.")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) || input.Code == "" || len(input.Code) > 512 || !utf8.ValidString(input.Code) {
		h.fail(w, r, 400, "invalid_request", "The result request is invalid.")
		return
	}
	entry, err := h.cache.takeResult(input.Code, app.ID, h.config.Now())
	if err != nil {
		h.cacheError(w, r, err)
		return
	}
	metadata.operationID, metadata.provider = entry.operationID, entry.provider
	r = r.WithContext(context.WithValue(r.Context(), metadataKey{}, metadata))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(entry.payload); err != nil {
		h.log(r, "result_response", "response_write_failed", err)
		return
	}
	h.log(r, "result_consume", "")
}

func (h *Handler) recheck(w http.ResponseWriter, r *http.Request, tx transaction) bool {
	app, err := h.apps.Get(r.Context(), tx.appID)
	if err != nil {
		if errors.Is(err, apps.ErrNotFound) {
			h.fail(w, r, 410, "transaction_unavailable", "The authentication transaction is unavailable.")
		} else {
			h.fail(w, r, 500, "internal_error", "Authentication could not be completed.", err)
		}
		return false
	}
	if !eligible(app, tx.provider, tx.redirectURI) {
		h.fail(w, r, 410, "transaction_unavailable", "The authentication transaction is unavailable.")
		return false
	}
	return true
}

func eligible(app apps.App, provider, redirectURI string) bool {
	return app.Enabled && slices.Contains(app.Providers, provider) && slices.Contains(app.RedirectURIs, redirectURI)
}

func withOperation(r *http.Request, tx transaction) *http.Request {
	metadata := r.Context().Value(metadataKey{}).(requestMetadata)
	metadata.operationID, metadata.appID, metadata.provider = tx.operationID, tx.appID, tx.provider
	return r.WithContext(context.WithValue(r.Context(), metadataKey{}, metadata))
}

func (h *Handler) log(r *http.Request, phase, errorCategory string, causes ...error) {
	metadata, _ := r.Context().Value(metadataKey{}).(requestMetadata)
	fields := []any{"component", "auth", "phase", phase, "request_id", metadata.requestID, "operation_id", metadata.operationID, "app_id", metadata.appID, "provider", metadata.provider, "error_category", errorCategory}
	if cause := errors.Join(causes...); cause != nil {
		operation := phase
		var providerError *providers.Error
		if errors.As(cause, &providerError) && slices.Contains([]string{"exchange", "token exchange", "discovery", "identity verification", "identity normalization", "userinfo retrieval", "profile retrieval", "email retrieval", "photo retrieval", "photo encoding"}, providerError.Operation) {
			operation = providerError.Operation
		}
		category := "operation_failed"
		var networkError net.Error
		switch {
		case errors.Is(cause, context.DeadlineExceeded):
			category = "deadline_exceeded"
		case errors.Is(cause, context.Canceled):
			category = "canceled"
		case errors.As(cause, &networkError) && networkError.Timeout():
			category = "network_timeout"
		}
		fields = append(fields, "error_operation", operation, "error_cause", category)
	}
	h.config.Logger.InfoContext(r.Context(), "authentication operation", fields...)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, code, message string, cause ...error) {
	h.log(r, "request_error", code, cause...)
	writeError(w, status, code, message)
}

func (h *Handler) cacheError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrClosed):
		h.fail(w, r, 503, "service_unavailable", "Authentication is unavailable.")
	case errors.Is(err, ErrCapacity):
		h.fail(w, r, 503, "capacity_exceeded", "Authentication capacity is exhausted.")
	default:
		code := "transaction_unavailable"
		message := "The authentication transaction is unavailable."
		if r.URL.Path == "/auth/result" {
			code = "result_unavailable"
			message = "The authentication result is unavailable."
		}
		h.fail(w, r, 410, code, message)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
}

func productRedirect(w http.ResponseWriter, r *http.Request, tx transaction, key, value string) {
	u, _ := url.Parse(tx.redirectURI)
	query := u.Query()
	query.Set(key, value)
	query.Set("state", tx.productState)
	u.RawQuery = query.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func single(query url.Values, name string, maxBytes int) (string, bool) {
	values := query[name]
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxBytes || !utf8.ValidString(values[0]) {
		return "", false
	}
	return values[0], true
}

func token(size int) string {
	bytes := make([]byte, size)
	_, _ = rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}
