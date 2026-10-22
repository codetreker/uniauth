package uniauth

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode/utf8"
)

func validValue(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value)
}

func supportedProvider(provider Provider) bool {
	return provider == Microsoft || provider == Google || provider == GitHub
}

func validateBaseURL(raw string, allowLoopbackHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || !utf8.ValidString(raw) || u.Hostname() == "" || u.Opaque != "" || u.User != nil || strings.ContainsAny(raw, "?#") || (u.EscapedPath() != "" && u.EscapedPath() != "/") {
		return nil, errors.New("uniauth: BaseURL must be an origin without credentials, query, or fragment")
	}
	if !allowedScheme(u, allowLoopbackHTTP) {
		return nil, errors.New("uniauth: BaseURL requires HTTPS or explicitly enabled loopback HTTP")
	}
	return u, nil
}

func validateRedirectURI(raw string) error {
	if !validValue(raw, 2048) {
		return errors.New("uniauth: callback URL requires 1 to 2048 UTF-8 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || strings.Contains(raw, "#") {
		return errors.New("uniauth: callback URL requires a host and excludes credentials and fragments")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("uniauth: callback URL requires HTTP or HTTPS")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("uniauth: callback query is invalid")
	}
	for _, name := range []string{"code", "state", "error"} {
		if _, exists := query[name]; exists {
			return errors.New("uniauth: callback query contains a reserved parameter")
		}
	}
	return nil
}

func allowedScheme(u *url.URL, allowLoopbackHTTP bool) bool {
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" || !allowLoopbackHTTP {
		return false
	}
	if strings.EqualFold(u.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
