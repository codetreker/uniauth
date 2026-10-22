package uniauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const providerResponseLimit = 16 << 10

// Providers returns the gateway's currently available providers for this App
// and exact callback URL. An empty, non-nil slice means none are available.
// It makes one POST, never follows redirects or retries, and accepts at most
// 16 KiB of response data. Unknown fields are tolerated; duplicate JSON keys,
// duplicate providers, and unsupported provider names are protocol failures.
func (c *Client) Providers(ctx context.Context, redirectURI string) ([]Provider, error) {
	if ctx == nil {
		return nil, errors.New("uniauth: provider discovery context is required")
	}
	if err := validateRedirectURI(redirectURI); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(struct {
		RedirectURI string `json:"redirect_uri"`
	}{RedirectURI: redirectURI})
	u := c.baseURL
	u.Path = "/auth/providers"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		return nil, &RequestError{Cause: err}
	}
	request.SetBasicAuth(c.appID, c.appSecret)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, &RequestError{Cause: err}
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, providerResponseLimit+1))
	closeErr := response.Body.Close()
	cause := errors.Join(readErr, closeErr)
	reason := "response_body"
	if len(body) > providerResponseLimit {
		reason = "response_size"
		cause = errors.Join(cause, errors.New("provider response exceeds 16 KiB"))
	}
	if cause != nil {
		return nil, &ProtocolError{StatusCode: response.StatusCode, RequestID: response.Header.Get("X-Request-ID"), Reason: reason, Cause: cause}
	}
	fields, err := parseResponseEnvelope(response.StatusCode, response.Header, body, uniqueObjectFields)
	if err != nil {
		return nil, err
	}
	var providers []Provider
	if err := json.Unmarshal(fields["providers"], &providers); err != nil || providers == nil {
		return nil, &ProtocolError{StatusCode: response.StatusCode, RequestID: response.Header.Get("X-Request-ID"), Reason: "providers_schema", Cause: err}
	}
	seen := make(map[Provider]bool, len(providers))
	for _, provider := range providers {
		if !supportedProvider(provider) || seen[provider] {
			return nil, &ProtocolError{StatusCode: response.StatusCode, RequestID: response.Header.Get("X-Request-ID"), Reason: "providers_schema"}
		}
		seen[provider] = true
	}
	return providers, nil
}

func uniqueObjectFields(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeUniqueJSONValue(decoder); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("response requires one JSON value")
	}
	return objectFields(raw)
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, valid := key.(string)
			if !valid || seen[name] {
				return errors.New("response contains a duplicate JSON field")
			}
			seen[name] = true
		}
		if err := consumeUniqueJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
