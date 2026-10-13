package console

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
)

type Handler struct {
	store           *apps.Store
	config          Config
	origin          string
	cookieName      string
	secure          bool
	available       map[string]bool
	mu              sync.Mutex
	sessions        map[[32]byte]adminSession
	loginTokens     float64
	lastLoginRefill time.Time
}

type sessionContextKey struct{}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/console/api/login", h.route([]string{"POST"}, false, h.login))
	mux.HandleFunc("/console/api/session", h.route([]string{"GET"}, true, h.getSession))
	mux.HandleFunc("/console/api/logout", h.route([]string{"POST"}, true, h.logout))
	mux.HandleFunc("/console/api/apps", h.route([]string{"GET", "POST"}, true, h.management(map[string]string{"POST": "app.create"}, h.apps)))
	mux.HandleFunc("/console/api/apps/{id}", h.route([]string{"GET", "PUT", "DELETE"}, true, h.management(map[string]string{"PUT": "app.update", "DELETE": "app.delete"}, h.app)))
	mux.HandleFunc("/console/api/apps/{id}/secrets", h.route([]string{"GET", "POST"}, true, h.management(map[string]string{"POST": "secret.create"}, h.secrets)))
	mux.HandleFunc("/console/api/apps/{id}/secrets/{secretID}", h.route([]string{"DELETE"}, true, h.management(map[string]string{"DELETE": "secret.revoke"}, h.secret)))
	mux.HandleFunc("/console/api/providers", h.route([]string{"GET"}, true, h.providers))
	mux.HandleFunc("/console/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, http.StatusNotFound, "not_found", "Console endpoint not found.")
	})
}

func (h *Handler) route(methods []string, authenticated bool, serve http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		allowed := false
		for _, method := range methods {
			allowed = allowed || r.Method == method
		}
		if !allowed {
			w.Header().Set("Allow", strings.Join(methods, ", "))
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
			return
		}
		mutation := r.Method != http.MethodGet
		if mutation {
			origins := r.Header.Values("Origin")
			if len(origins) != 1 || origins[0] != h.origin {
				writeError(w, http.StatusForbidden, "invalid_origin", "The request origin is not allowed.")
				return
			}
		}
		if authenticated {
			session, ok := h.session(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized", "Administrator authentication is required.")
				return
			}
			if mutation && !validCSRF(r, session.adminSession) {
				writeError(w, http.StatusForbidden, "invalid_csrf", "The CSRF token is invalid.")
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, session))
		}
		serve(w, r)
	}
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if len(input.Password) == 0 || len(input.Password) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_input", "Password must contain between one and 1024 bytes.")
		return
	}
	if !h.allowLogin() {
		w.Header().Set("Retry-After", strconv.Itoa(int(h.config.LoginInterval/time.Second)+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many administrator login attempts.")
		return
	}
	if subtle.ConstantTimeCompare([]byte(input.Password), []byte(h.config.Password)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "The administrator password is incorrect.")
		return
	}
	if session, ok := h.issueSession(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]string{"csrf_token": session.csrfToken})
	}
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	session := r.Context().Value(sessionContextKey{}).(authenticatedSession)
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": session.csrfToken})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	session := r.Context().Value(sessionContextKey{}).(authenticatedSession)
	h.removeSession(session.key)
	h.setCookie(w, "", time.Unix(1, 0), -1)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) apps(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		list, err := h.store.List(r.Context())
		if !storeError(w, err) {
			writeJSON(w, http.StatusOK, map[string]any{"apps": list})
		}
		return
	}
	var input apps.AppInput
	if !readJSON(w, r, &input) {
		return
	}
	created, err := h.store.Create(r.Context(), input)
	if !storeError(w, err) {
		w.(*auditResponse).appID = created.App.ID
		writeJSON(w, http.StatusCreated, created)
	}
}

func (h *Handler) app(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		app, err := h.store.Get(r.Context(), id)
		if !storeError(w, err) {
			writeJSON(w, http.StatusOK, app)
		}
	case http.MethodPut:
		var input apps.AppInput
		if !readJSON(w, r, &input) {
			return
		}
		app, err := h.store.Update(r.Context(), id, input)
		if !storeError(w, err) {
			writeJSON(w, http.StatusOK, app)
		}
	case http.MethodDelete:
		if !storeError(w, h.store.Delete(r.Context(), id)) {
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func (h *Handler) secrets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		secrets, err := h.store.ListSecrets(r.Context(), r.PathValue("id"))
		if !storeError(w, err) {
			writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
		}
		return
	}
	secret, err := h.store.CreateSecret(r.Context(), r.PathValue("id"))
	if !storeError(w, err) {
		writeJSON(w, http.StatusCreated, secret)
	}
}

func (h *Handler) secret(w http.ResponseWriter, r *http.Request) {
	err := h.store.RevokeSecret(r.Context(), r.PathValue("id"), r.PathValue("secretID"))
	if !storeError(w, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) providers(w http.ResponseWriter, r *http.Request) {
	type provider struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
	}
	list := make([]provider, 0, 3)
	for _, name := range []string{"microsoft", "google", "github"} {
		list = append(list, provider{ID: name, Available: h.available[name]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": list})
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Content-Type must be application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "The request body must be a valid JSON object with supported fields.")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_input", "The request body must contain exactly one JSON object.")
		return false
	}
	return true
}

func storeError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, apps.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_input", "The application configuration is invalid.")
	case errors.Is(err, apps.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "The application or credential was not found.")
	case errors.Is(err, apps.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "Application authentication failed.")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Unable to complete the application operation.")
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
