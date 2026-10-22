package apps

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

func validateInput(input AppInput) error {
	if input.Name == "" || input.Name != strings.TrimSpace(input.Name) || !utf8.ValidString(input.Name) || len(input.Name) > 128 {
		return fmt.Errorf("%w: name must contain 1 to 128 bytes without surrounding whitespace", ErrInvalid)
	}
	if len(input.RedirectURIs) == 0 || len(input.RedirectURIs) > 16 {
		return fmt.Errorf("%w: one to sixteen redirect URIs are required", ErrInvalid)
	}
	seen := make(map[string]bool, len(input.RedirectURIs))
	for _, uri := range input.RedirectURIs {
		if seen[uri] {
			return fmt.Errorf("%w: duplicate redirect URI", ErrInvalid)
		}
		seen[uri] = true
		if err := validateRedirectURI(uri); err != nil {
			return err
		}
	}
	if len(input.Providers) == 0 || len(input.Providers) > 3 {
		return fmt.Errorf("%w: one to three providers are required", ErrInvalid)
	}
	seen = make(map[string]bool, len(input.Providers))
	for _, provider := range input.Providers {
		switch provider {
		case "microsoft", "google", "github":
		default:
			return fmt.Errorf("%w: unknown provider", ErrInvalid)
		}
		if seen[provider] {
			return fmt.Errorf("%w: duplicate provider", ErrInvalid)
		}
		seen[provider] = true
	}
	return nil
}

func validateSecretInput(input SecretInput) error {
	if input.Name == "" || input.Name != strings.TrimSpace(input.Name) || !utf8.ValidString(input.Name) || len(input.Name) > 128 || strings.ContainsAny(input.Name, "\r\n") {
		return fmt.Errorf("%w: secret name must contain 1 to 128 UTF-8 bytes without surrounding whitespace or line breaks", ErrInvalid)
	}
	if !utf8.ValidString(input.Description) || len(input.Description) > 4096 {
		return fmt.Errorf("%w: secret description must contain at most 4096 UTF-8 bytes", ErrInvalid)
	}
	return nil
}

func validateRedirectURI(raw string) error {
	if raw == "" || len(raw) > 2048 || !utf8.ValidString(raw) {
		return fmt.Errorf("%w: redirect URI must contain 1 to 2048 UTF-8 bytes", ErrInvalid)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: parse redirect URI: %w", ErrInvalid, err)
	}
	if u.Hostname() == "" || u.Opaque != "" || u.User != nil || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: redirect URI requires a host and excludes credentials and fragments", ErrInvalid)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%w: redirect URI requires HTTP or HTTPS", ErrInvalid)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("%w: parse redirect query: %w", ErrInvalid, err)
	}
	for _, reserved := range []string{"code", "state", "error"} {
		if _, exists := query[reserved]; exists {
			return fmt.Errorf("%w: reserved redirect query parameter", ErrInvalid)
		}
	}
	return nil
}
