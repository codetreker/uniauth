package console

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/auth"
	"github.com/codetreker/uniauth/internal/providers"
)

type pendingLogin struct {
	provider      string
	cookieName    string
	binding       string
	expiresAt     time.Time
	linkAccountID string
	sessionKey    *[32]byte
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) { h.beginLogin(w, r, false) }
func (h *Handler) link(w http.ResponseWriter, r *http.Request)  { h.beginLogin(w, r, true) }

func (h *Handler) beginLogin(w http.ResponseWriter, r *http.Request, linking bool) {
	var input struct {
		Provider string `json:"provider"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !h.available[input.Provider] {
		writeError(w, http.StatusBadRequest, "invalid_provider", "The selected login provider is unavailable.")
		return
	}
	pending := pendingLogin{provider: input.Provider, cookieName: "uniauth-console-flow-" + randomToken(), binding: randomToken(), expiresAt: h.config.Now().Add(h.config.PendingTTL)}
	if h.secure {
		pending.cookieName = "__Host-" + pending.cookieName
	}
	if session, ok := h.session(r); ok {
		pending.sessionKey = &session.key
		if linking {
			pending.linkAccountID = session.accountID
		}
	} else if linking {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Console authentication is required.")
		return
	}
	state := randomToken()
	h.mu.Lock()
	h.prunePending(h.config.Now())
	if h.closed || len(h.pending) >= h.config.MaxPending {
		h.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "login_capacity", "Console login capacity has been reached.")
		return
	}
	h.pending[state] = pending
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: pending.cookieName, Value: pending.binding, Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: int((h.config.PendingTTL + time.Second - 1) / time.Second)})
	authorization := h.origin + "/auth/authorize?" + url.Values{"app_id": {h.config.AppID}, "provider": {input.Provider}, "redirect_uri": {strings.TrimSuffix(h.config.PublicURL, "/") + "/console/auth/callback"}, "state": {state}}.Encode()
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": authorization})
}

func (h *Handler) prunePending(now time.Time) {
	for state, pending := range h.pending {
		if !now.Before(pending.expiresAt) {
			delete(h.pending, state)
		}
	}
}

func (h *Handler) claimPending(r *http.Request, state string) (pendingLogin, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.config.Now()
	h.prunePending(now)
	pending, exists := h.pending[state]
	if !exists || h.closed {
		return pendingLogin{}, false
	}
	cookie, valid := uniqueCookie(r, pending.cookieName)
	if !valid || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(pending.binding)) != 1 {
		return pendingLogin{}, false
	}
	delete(h.pending, state)
	h.pruneSessions(now)
	if pending.linkAccountID != "" {
		session, live := h.sessions[*pending.sessionKey]
		if !live || session.accountID != pending.linkAccountID {
			return pending, false
		}
		// A Strict session cookie may be absent after a cross-site redirect. If one
		// is present, it must name the session that initiated the link.
		cookie, hasCookie := uniqueCookie(r, h.cookieName)
		if hasCookie {
			session, ok := h.sessionLocked(cookie.Value)
			if !ok || session.key != *pending.sessionKey {
				return pending, false
			}
		} else {
			for _, cookie := range r.Cookies() {
				if cookie.Name == h.cookieName {
					return pending, false
				}
			}
		}
	}
	return pending, true
}

func (h *Handler) sessionLocked(token string) (authenticatedSession, bool) {
	key := sessionTokenKey(token)
	session, ok := h.sessions[key]
	return authenticatedSession{consoleSession: session, key: key}, ok
}

func callbackValue(query url.Values, name string, size int) (string, bool) {
	values := query[name]
	returnValue := ""
	if len(values) == 1 {
		returnValue = values[0]
	}
	return returnValue, len(values) == 1 && returnValue != "" && len(returnValue) <= size && utf8.ValidString(returnValue)
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	state, validState := callbackValue(query, "state", 43)
	code, hasCode := callbackValue(query, "code", 512)
	_, hasError := callbackValue(query, "error", 256)
	if err != nil || !validState || len(state) != 43 || hasCode == hasError || hasCode && len(query["error"]) != 0 || hasError && len(query["code"]) != 0 {
		h.callbackRedirect(w, r, "invalid_callback")
		return
	}
	for name := range query {
		if name != "state" && name != "code" && name != "error" {
			h.callbackRedirect(w, r, "invalid_callback")
			return
		}
	}
	pending, ok := h.claimPending(r, state)
	if pending.cookieName != "" {
		http.SetCookie(w, &http.Cookie{Name: pending.cookieName, Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
	if !ok {
		h.callbackRedirect(w, r, "login_unavailable")
		return
	}
	if hasError {
		h.callbackRedirect(w, r, "provider_failed")
		return
	}
	result, err := h.config.Gateway.ConsumeResult(r.Context(), h.config.AppID, h.config.AppSecret, code)
	if err != nil {
		category := "login_failed"
		if errors.Is(err, auth.ErrUnavailable) {
			category = "login_unavailable"
		}
		h.callbackRedirect(w, r, category)
		return
	}
	var identity providers.Identity
	if err := json.Unmarshal(result.Identity, &identity); err != nil || identity.Provider != pending.provider || result.Provider != pending.provider {
		h.callbackRedirect(w, r, "login_failed")
		return
	}
	if pending.linkAccountID != "" {
		// Logout and expiry must not pass the final live-session check while the
		// identity is being attached. Store operations never call back into Console.
		h.mu.Lock()
		h.pruneSessions(h.config.Now())
		session, live := h.sessions[*pending.sessionKey]
		if !h.closed && live && session.accountID == pending.linkAccountID {
			err = h.store.LinkIdentity(r.Context(), pending.linkAccountID, identity)
		} else {
			err = auth.ErrUnavailable
		}
		h.mu.Unlock()
		if err != nil {
			category := "login_failed"
			if errors.Is(err, apps.ErrIdentityConflict) {
				category = "identity_conflict"
			}
			if errors.Is(err, auth.ErrUnavailable) {
				category = "login_unavailable"
			}
			h.callbackRedirect(w, r, category)
			return
		}
	} else {
		account, err := h.store.ResolveAccount(r.Context(), identity)
		if err != nil {
			h.callbackRedirect(w, r, "login_failed")
			return
		}
		if !h.issueSession(w, r, account.ID, pending.sessionKey) {
			h.callbackRedirect(w, r, "session_capacity")
			return
		}
	}
	h.callbackRedirect(w, r, "")
}

func (h *Handler) callbackRedirect(w http.ResponseWriter, r *http.Request, category string) {
	h.config.Logger.InfoContext(r.Context(), "console OAuth operation", "phase", "callback", "error_category", category)
	target := "/console/"
	if category != "" {
		target += "?login_error=" + url.QueryEscape(category)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handler) identities(w http.ResponseWriter, r *http.Request) {
	identities, err := h.store.ListIdentities(r.Context(), requestSession(r).accountID)
	if !storeError(w, err) {
		writeJSON(w, http.StatusOK, map[string]any{"identities": identities})
	}
}

func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "The user-list query is invalid.")
		return
	}
	for key, values := range query {
		if (key != "cursor" && key != "limit") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_input", "The user-list query is invalid.")
			return
		}
	}
	limit := 50
	if values, exists := query["limit"]; exists {
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > 100 || strconv.Itoa(parsed) != values[0] {
			writeError(w, http.StatusBadRequest, "invalid_input", "The user-list limit must be between one and 100.")
			return
		}
		limit = parsed
	}
	cursor := query.Get("cursor")
	if len(cursor) > 512 || !utf8.ValidString(cursor) || strings.ContainsAny(cursor, "\r\n") {
		writeError(w, http.StatusBadRequest, "invalid_input", "The user-list cursor is invalid.")
		return
	}
	users, err := h.store.ListUsers(r.Context(), requestSession(r).accountID, cursor, limit)
	if !storeError(w, err) {
		writeJSON(w, http.StatusOK, users)
	}
}
