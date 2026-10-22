package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/codetreker/uniauth/internal/apps"
)

func (h *Handler) discoverProviders(w http.ResponseWriter, r *http.Request) {
	r, ok := h.begin(w, r)
	if !ok {
		return
	}
	fail := func(status int, code, message string, cause ...error) {
		h.log(r, "provider_discovery", code, cause...)
		writeError(w, status, code, message)
	}
	appID, secret, basic := r.BasicAuth()
	if !basic || len(r.Header.Values("Authorization")) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="uniauth"`)
		fail(http.StatusUnauthorized, "invalid_app", "Application authentication failed.")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	defer cancel()
	app, err := h.authenticateApp(ctx, appID, secret)
	if h.ctx.Err() != nil {
		fail(http.StatusServiceUnavailable, "service_unavailable", "Authentication is unavailable.")
		return
	}
	if err != nil {
		if errors.Is(err, apps.ErrUnauthorized) {
			w.Header().Set("WWW-Authenticate", `Basic realm="uniauth"`)
			fail(http.StatusUnauthorized, "invalid_app", "Application authentication failed.")
		} else {
			fail(http.StatusInternalServerError, "internal_error", "Authentication could not be completed.", err)
		}
		return
	}
	metadata := r.Context().Value(metadataKey{}).(requestMetadata)
	metadata.appID = app.ID
	r = r.WithContext(context.WithValue(ctx, metadataKey{}, metadata))
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(http.StatusBadRequest, "invalid_request", "The provider request requires application/json.")
		return
	}
	redirectURI, err := providerRedirectURI(http.MaxBytesReader(w, r.Body, 16<<10))
	if h.ctx.Err() != nil {
		fail(http.StatusServiceUnavailable, "service_unavailable", "Authentication is unavailable.", err)
		return
	}
	if ctx.Err() != nil {
		fail(http.StatusInternalServerError, "internal_error", "Authentication could not be completed.", errors.Join(err, ctx.Err()))
		return
	}
	if err != nil || !slices.Contains(app.RedirectURIs, redirectURI) {
		fail(http.StatusBadRequest, "invalid_request", "The provider request is invalid.", err)
		return
	}
	names := make([]string, 0, len(h.providers))
	for _, name := range []string{"microsoft", "google", "github"} {
		if _, available := h.providers[name]; available && slices.Contains(app.Providers, name) {
			names = append(names, name)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(struct {
		Providers []string `json:"providers"`
	}{Providers: names}); err != nil {
		h.log(r, "provider_discovery", "response_write_failed", err)
		return
	}
	h.log(r, "provider_discovery", "")
}

func providerRedirectURI(body io.Reader) (string, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return "", ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", errors.Join(ErrInvalidRequest, err)
	}
	var redirectURI string
	seen := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || key != "redirect_uri" || seen {
			return "", errors.Join(ErrInvalidRequest, err)
		}
		if err := decoder.Decode(&redirectURI); err != nil {
			return "", errors.Join(ErrInvalidRequest, err)
		}
		seen = true
	}
	if _, err := decoder.Token(); err != nil {
		return "", errors.Join(ErrInvalidRequest, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return "", errors.Join(ErrInvalidRequest, err)
	}
	if !seen || redirectURI == "" || len(redirectURI) > 2048 || !utf8.ValidString(redirectURI) {
		return "", ErrInvalidRequest
	}
	return redirectURI, nil
}
