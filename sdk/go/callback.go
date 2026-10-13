package uniauth

import (
	"crypto/subtle"
	"net/url"
)

// CallbackError.Code is invalid_callback, state_mismatch, or a gateway callback
// failure: provider_denied, provider_failed, or capacity_exceeded.
type CallbackError struct {
	Code string
}

func (e *CallbackError) Error() string { return "uniauth: callback failed (" + e.Code + ")" }

// ParseCallback validates the callback against pending state already bound to
// the initiating browser. Parsing does not consume that state: the product must
// atomically consume it on success or failure before retrieving an identity.
func ParseCallback(query url.Values, expectedState string) (string, error) {
	state, ok := singleValue(query, "state", 2048)
	if !ok || !validValue(expectedState, 2048) {
		return "", &CallbackError{Code: "invalid_callback"}
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(expectedState)) != 1 {
		return "", &CallbackError{Code: "state_mismatch"}
	}
	_, hasCode := query["code"]
	_, hasError := query["error"]
	if hasCode == hasError {
		return "", &CallbackError{Code: "invalid_callback"}
	}
	if hasCode {
		code, ok := singleValue(query, "code", 512)
		if !ok {
			return "", &CallbackError{Code: "invalid_callback"}
		}
		return code, nil
	}
	code, ok := singleValue(query, "error", 32)
	if !ok || (code != "provider_denied" && code != "provider_failed" && code != "capacity_exceeded") {
		return "", &CallbackError{Code: "invalid_callback"}
	}
	return "", &CallbackError{Code: code}
}

func singleValue(query url.Values, name string, maxBytes int) (string, bool) {
	values := query[name]
	if len(values) != 1 || !validValue(values[0], maxBytes) {
		return "", false
	}
	return values[0], true
}
