package console

import (
	"encoding/base64"
	"net/http"
)

type auditResponse struct {
	http.ResponseWriter
	status int
	appID  string
}

func (w *auditResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (h *Handler) management(actions map[string]string, serve http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action := actions[r.Method]
		if action == "" {
			serve(w, r)
			return
		}
		// The response header is assigned by our HTTP composition, never read from a client header.
		requestID := w.Header().Get("X-Request-ID")
		if requestID == "" {
			requestID = randomToken()
			w.Header().Set("X-Request-ID", requestID)
		}
		w.Header().Set("X-Operation-ID", requestID)
		reply := &auditResponse{ResponseWriter: w, status: http.StatusOK, appID: auditAppID(r.PathValue("id"))}
		serve(reply, r)
		outcome := "success"
		if reply.status >= 400 {
			outcome = "failure"
		}
		h.config.Logger.InfoContext(r.Context(), "console management operation",
			"operation_id", requestID, "request_id", requestID, "phase", "completed", "action", action,
			"affected_app_id", reply.appID, "outcome", outcome, "error_category", auditErrorCategory(reply.status), "status", reply.status)
	}
}

func auditAppID(candidate string) string {
	if len(candidate) != 22 {
		return ""
	}
	value, err := base64.RawURLEncoding.Strict().DecodeString(candidate)
	if err != nil || len(value) != 16 {
		return ""
	}
	return candidate
}

func auditErrorCategory(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_input"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusUnsupportedMediaType:
		return "invalid_content_type"
	case http.StatusInternalServerError:
		return "storage_failure"
	default:
		return ""
	}
}
