package providers

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"

	"github.com/go-jose/go-jose/v4"
)

const microsoftIssuerPrefix = "https://login.microsoftonline.com/"
const microsoftIssuerTemplate = microsoftIssuerPrefix + "{tenantid}/v2.0"

var microsoftTenantPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type microsoftKey struct {
	key    jose.JSONWebKey
	issuer string
}
type microsoftSnapshot struct {
	keys       []microsoftKey
	generation uint64
}
type microsoftFlight struct {
	done      chan struct{}
	snapshot  microsoftSnapshot
	err       error
	abandoned bool
}
type microsoftKeySet struct {
	client   *http.Client
	url      string
	mu       sync.Mutex
	snapshot microsoftSnapshot
	flight   *microsoftFlight
}

func microsoftVerificationFailure(category string, err error) error {
	return &microsoftVerifyError{category: category, cause: err}
}

func (s *microsoftKeySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jws, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(jws.Signatures) != 1 {
		if err == nil {
			err = errors.New("one signature is required")
		}
		return nil, microsoftVerificationFailure("signature", err)
	}
	fields, err := jsonObject(jws.UnsafePayloadWithoutVerification())
	if err != nil {
		return nil, microsoftVerificationFailure("tenant_claim", err)
	}
	tenant, err := nonemptyString(fields["tid"])
	if err != nil || !microsoftTenantPattern.MatchString(tenant) {
		if err == nil {
			err = errors.New("invalid tenant claim")
		}
		return nil, microsoftVerificationFailure("tenant_claim", err)
	}
	issuer, err := nonemptyString(fields["iss"])
	if err != nil || issuer != microsoftIssuerPrefix+tenant+"/v2.0" {
		if err == nil {
			err = errors.New("invalid token issuer")
		}
		return nil, microsoftVerificationFailure("token_issuer", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, microsoftVerificationFailure("key_fetch", err)
	}
	s.mu.Lock()
	snapshot := s.snapshot
	s.mu.Unlock()
	if payload, _ := verifyMicrosoftSnapshot(snapshot, jws, issuer); payload != nil {
		return payload, nil
	}
	led := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, microsoftVerificationFailure("key_fetch", err)
		}
		s.mu.Lock()
		if s.snapshot.generation != snapshot.generation {
			snapshot = s.snapshot
			s.mu.Unlock()
			break
		}
		flight := s.flight
		if flight == nil {
			if led {
				s.mu.Unlock()
				return nil, microsoftVerificationFailure("key_fetch", errors.New("refresh abandoned"))
			}
			flight = &microsoftFlight{done: make(chan struct{})}
			s.flight = flight
			led = true
			s.mu.Unlock()
			keys, fetchErr := s.fetch(ctx)
			s.mu.Lock()
			if fetchErr == nil {
				s.snapshot = microsoftSnapshot{keys: keys, generation: s.snapshot.generation + 1}
				flight.snapshot = s.snapshot
			} else if ctx.Err() != nil {
				flight.abandoned = true
			} else {
				flight.err = fetchErr
			}
			s.flight = nil
			close(flight.done)
			s.mu.Unlock()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, microsoftVerificationFailure("key_fetch", errors.Join(ctxErr, fetchErr))
			}
		} else {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, microsoftVerificationFailure("key_fetch", ctx.Err())
			case <-flight.done:
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, microsoftVerificationFailure("key_fetch", err)
		}
		if flight.abandoned {
			continue
		}
		if flight.err != nil {
			return nil, microsoftVerificationFailure("key_fetch", flight.err)
		}
		snapshot = flight.snapshot
		break
	}
	payload, category := verifyMicrosoftSnapshot(snapshot, jws, issuer)
	if payload == nil {
		return nil, microsoftVerificationFailure(category, errors.New("no eligible key verified signature"))
	}
	return payload, nil
}

func verifyMicrosoftSnapshot(snapshot microsoftSnapshot, jws *jose.JSONWebSignature, issuer string) ([]byte, string) {
	category := "key_issuer"
	kid := jws.Signatures[0].Header.KeyID
	for _, entry := range snapshot.keys {
		if kid != "" && entry.key.KeyID != kid {
			continue
		}
		if entry.issuer != microsoftIssuerTemplate && entry.issuer != issuer {
			continue
		}
		category = "signature"
		if payload, err := jws.Verify(&entry.key); err == nil {
			return payload, category
		}
	}
	return nil, category
}

func (s *microsoftKeySet) fetch(ctx context.Context) ([]microsoftKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "uniauth")
	response, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, statusError{status: response.StatusCode}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	keys := make([]microsoftKey, 0, len(envelope.Keys))
	var decodeErrors []error
	for _, raw := range envelope.Keys {
		var key jose.JSONWebKey
		var metadata struct {
			Issuer string `json:"issuer"`
		}
		if err := json.Unmarshal(raw, &key); err != nil {
			decodeErrors = append(decodeErrors, err)
			continue
		}
		if err := json.Unmarshal(raw, &metadata); err != nil {
			decodeErrors = append(decodeErrors, err)
			continue
		}
		if _, ok := key.Key.(*rsa.PublicKey); !ok || !key.Valid() || (key.Use != "" && key.Use != "sig") || (key.Algorithm != "" && key.Algorithm != "RS256") {
			continue
		}
		if metadata.Issuer != microsoftIssuerTemplate {
			const suffix = "/v2.0"
			if len(metadata.Issuer) != len(microsoftIssuerPrefix)+36+len(suffix) || metadata.Issuer[:len(microsoftIssuerPrefix)] != microsoftIssuerPrefix || metadata.Issuer[len(metadata.Issuer)-len(suffix):] != suffix || !microsoftTenantPattern.MatchString(metadata.Issuer[len(microsoftIssuerPrefix):len(metadata.Issuer)-len(suffix)]) {
				continue
			}
		}
		keys = append(keys, microsoftKey{key: key, issuer: metadata.Issuer})
	}
	if len(keys) == 0 {
		return nil, errors.Join(append([]error{errors.New("JWKS has no usable issuer-scoped signing keys")}, decodeErrors...)...)
	}
	return keys, nil
}
