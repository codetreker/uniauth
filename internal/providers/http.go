package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxResponseBytes = 8 << 20

var errResponseTooLarge = errors.New("provider response exceeds size limit")

type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > maxResponseBytes {
		response.Body.Close()
		return nil, errResponseTooLarge
	}
	response.Body = &boundedBody{body: response.Body, remaining: maxResponseBytes}
	return response, nil
}

type boundedBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, errResponseTooLarge
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n, errResponseTooLarge
	}
	return n, err
}

func (b *boundedBody) Close() error { return b.body.Close() }

func boundedClient(client *http.Client) *http.Client {
	copy := *client
	transport := copy.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	copy.Transport = boundedTransport{base: transport}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

type statusError struct{ status int }

func (e statusError) Error() string { return fmt.Sprintf("upstream HTTP status %d", e.status) }

func fetch(ctx context.Context, client *http.Client, endpoint, accessToken string) ([]byte, string, int, error) {
	return fetchWithAccept(ctx, client, endpoint, accessToken, "application/json")
}

func fetchWithAccept(ctx context.Context, client *http.Client, endpoint, accessToken, accept string) ([]byte, string, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", 0, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", "uniauth")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", response.StatusCode, statusError{status: response.StatusCode}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, "", response.StatusCode, err
	}
	return body, response.Header.Get("Content-Type"), response.StatusCode, nil
}

func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("provider JSON object is required")
	}
	return value, nil
}

func nonemptyString(value json.RawMessage) (string, error) {
	var result string
	if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", errors.New("required identity claim is missing")
	}
	if err := json.Unmarshal(value, &result); err != nil {
		return "", err
	}
	if result == "" {
		return "", errors.New("required identity claim is empty")
	}
	return result, nil
}
