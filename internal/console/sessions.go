package console

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"
)

const loginBurst = 5

type adminSession struct {
	csrfToken string
	expiresAt time.Time
}

type authenticatedSession struct {
	adminSession
	key [32]byte
}

func randomToken() string {
	var value [32]byte
	rand.Read(value[:])
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func (h *Handler) session(r *http.Request) (authenticatedSession, bool) {
	cookie, err := r.Cookie(h.cookieName)
	if err != nil || len(cookie.Value) != 43 {
		return authenticatedSession{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneSessions(h.config.Now())
	key := sha256.Sum256([]byte(cookie.Value))
	session, ok := h.sessions[key]
	return authenticatedSession{adminSession: session, key: key}, ok
}

func (h *Handler) pruneSessions(now time.Time) {
	for key, session := range h.sessions {
		if !now.Before(session.expiresAt) {
			delete(h.sessions, key)
		}
	}
}

func (h *Handler) issueSession(w http.ResponseWriter, r *http.Request) (adminSession, bool) {
	token := randomToken()
	csrfToken := randomToken()
	h.mu.Lock()
	now := h.config.Now()
	h.pruneSessions(now)
	oldCookie, cookieErr := r.Cookie(h.cookieName)
	if cookieErr == nil {
		delete(h.sessions, sha256.Sum256([]byte(oldCookie.Value)))
	}
	if len(h.sessions) >= h.config.MaxSessions {
		h.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "session_capacity", "Administrator session capacity has been reached.")
		return adminSession{}, false
	}
	session := adminSession{csrfToken: csrfToken, expiresAt: now.Add(h.config.SessionTTL)}
	h.sessions[sha256.Sum256([]byte(token))] = session
	h.mu.Unlock()
	h.setCookie(w, token, session.expiresAt, int(h.config.SessionTTL/time.Second))
	return session, true
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

func (h *Handler) allowLogin() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.config.Now()
	elapsed := now.Sub(h.lastLoginRefill)
	if elapsed > 0 {
		h.loginTokens += float64(elapsed) / float64(h.config.LoginInterval)
		if h.loginTokens > loginBurst {
			h.loginTokens = loginBurst
		}
		h.lastLoginRefill = now
	}
	if h.loginTokens < 1 {
		return false
	}
	h.loginTokens--
	return true
}

func validCSRF(r *http.Request, session adminSession) bool {
	values := r.Header.Values("X-CSRF-Token")
	return len(values) == 1 && subtle.ConstantTimeCompare([]byte(values[0]), []byte(session.csrfToken)) == 1
}
