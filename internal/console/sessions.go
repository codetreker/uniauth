package console

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"
)

type consoleSession struct {
	accountID string
	csrfToken string
	expiresAt time.Time
}

type authenticatedSession struct {
	consoleSession
	key [32]byte
}

func sessionTokenKey(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func randomToken() string {
	var value [32]byte
	rand.Read(value[:])
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func uniqueCookie(r *http.Request, name string) (*http.Cookie, bool) {
	var result *http.Cookie
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			if result != nil {
				return nil, false
			}
			result = cookie
		}
	}
	return result, result != nil
}

func (h *Handler) session(r *http.Request) (authenticatedSession, bool) {
	cookie, ok := uniqueCookie(r, h.cookieName)
	if !ok || len(cookie.Value) != 43 {
		return authenticatedSession{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneSessions(h.config.Now())
	key := sha256.Sum256([]byte(cookie.Value))
	session, ok := h.sessions[key]
	return authenticatedSession{consoleSession: session, key: key}, ok && !h.closed
}

func (h *Handler) pruneSessions(now time.Time) {
	for key, session := range h.sessions {
		if !now.Before(session.expiresAt) {
			delete(h.sessions, key)
		}
	}
}

func (h *Handler) issueSession(w http.ResponseWriter, r *http.Request, accountID string, oldKey *[32]byte) bool {
	token := randomToken()
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.config.Now()
	h.pruneSessions(now)
	if h.closed {
		return false
	}
	if oldKey != nil {
		delete(h.sessions, *oldKey)
	}
	if cookie, ok := uniqueCookie(r, h.cookieName); ok {
		delete(h.sessions, sha256.Sum256([]byte(cookie.Value)))
	}
	if len(h.sessions) >= h.config.MaxSessions {
		return false
	}
	session := consoleSession{accountID: accountID, csrfToken: randomToken(), expiresAt: now.Add(h.config.SessionTTL)}
	h.sessions[sha256.Sum256([]byte(token))] = session
	h.setCookie(w, token, session.expiresAt, int(h.config.SessionTTL/time.Second))
	return true
}

func (h *Handler) setCookie(w http.ResponseWriter, token string, expiry time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cookieName, Value: token, Path: "/", HttpOnly: true, Secure: h.secure,
		SameSite: http.SameSiteStrictMode, Expires: expiry, MaxAge: maxAge,
	})
}

func (h *Handler) removeSession(key [32]byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, key)
}

func validCSRF(r *http.Request, session consoleSession) bool {
	values := r.Header.Values("X-CSRF-Token")
	return len(values) == 1 && subtle.ConstantTimeCompare([]byte(values[0]), []byte(session.csrfToken)) == 1
}

func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	clear(h.sessions)
	clear(h.pending)
	return nil
}
