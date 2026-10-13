package uniauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"unicode/utf8"
)

// APIError describes a valid gateway failure response. Message is supplied by
// the gateway; Error omits response data so routine logs do not expose it.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("uniauth: result retrieval failed (HTTP %d)", e.StatusCode)
}

// ProtocolError means the response cannot be interpreted as an identity or
// gateway failure. Cause retains JSON and body errors without printing payloads.
type ProtocolError struct {
	StatusCode int
	RequestID  string
	Reason     string
	Cause      error
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("uniauth: invalid result response (%s, HTTP %d)", e.Reason, e.StatusCode)
}

func (e *ProtocolError) Unwrap() error { return e.Cause }

// RequestError retains the transport or context error without printing the URL
// or any data supplied by a custom transport.
type RequestError struct {
	Cause error
}

func (e *RequestError) Error() string { return "uniauth: result request failed" }
func (e *RequestError) Unwrap() error { return e.Cause }

func parseResponse(status int, header http.Header, body []byte) (*Identity, error) {
	failure := func(reason string, cause error) (*Identity, error) {
		return nil, &ProtocolError{StatusCode: status, RequestID: header.Get("X-Request-ID"), Reason: reason, Cause: cause}
	}
	if status != http.StatusOK && (status < 400 || status > 599) {
		return failure("unexpected_status", nil)
	}
	contentType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return failure("content_type", err)
	}
	if !utf8.Valid(body) {
		return failure("json", errors.New("response JSON requires valid UTF-8"))
	}
	fields, err := objectFields(body)
	if err != nil {
		return failure("json", err)
	}
	if status != http.StatusOK {
		errorFields, err := objectFields(fields["error"])
		if err != nil {
			return failure("error_schema", err)
		}
		var code, message string
		if json.Unmarshal(errorFields["code"], &code) != nil || code == "" || json.Unmarshal(errorFields["message"], &message) != nil || message == "" {
			return failure("error_schema", nil)
		}
		return nil, &APIError{StatusCode: status, Code: code, Message: message, RequestID: header.Get("X-Request-ID")}
	}
	var provider Provider
	var subject string
	if json.Unmarshal(fields["provider"], &provider) != nil || !supportedProvider(provider) || json.Unmarshal(fields["subject"], &subject) != nil || subject == "" {
		return failure("identity_schema", nil)
	}
	sources, err := objectFields(fields["sources"])
	if err != nil {
		return failure("identity_schema", err)
	}
	return &Identity{Provider: provider, Subject: subject, Sources: sources, RawJSON: json.RawMessage(body)}, nil
}

func objectFields(raw []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("response field requires a JSON object")
	}
	return fields, nil
}
