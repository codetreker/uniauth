package providers

import (
	"context"
	"errors"

	"github.com/coreos/go-oidc/v3/oidc"
)

type microsoftVerifyError struct {
	category string
	cause    error
}

func (e *microsoftVerifyError) Error() string { return "Microsoft verification failed: " + e.category }
func (e *microsoftVerifyError) Unwrap() error { return e.cause }

type microsoftVerifier struct {
	keys     *microsoftKeySet
	clientID string
}
type microsoftCallKeySet struct {
	keys *microsoftKeySet
	err  error
}

func (s *microsoftCallKeySet) VerifySignature(ctx context.Context, token string) ([]byte, error) {
	payload, err := s.keys.VerifySignature(ctx, token)
	s.err = err
	return payload, err
}
func (v *microsoftVerifier) Verify(ctx context.Context, raw string) (*oidc.IDToken, error) {
	call := &microsoftCallKeySet{keys: v.keys}
	verifier := oidc.NewVerifier("", call, &oidc.Config{ClientID: v.clientID, SupportedSigningAlgs: []string{oidc.RS256}, SkipIssuerCheck: true})
	token, err := verifier.Verify(ctx, raw)
	if err != nil {
		category := "oidc_claims"
		var cause *microsoftVerifyError
		if errors.As(call.err, &cause) {
			category = cause.category
			err = errors.Join(err, call.err)
		}
		if stage, ok := ctx.Value(stageKey{}).(*diagnosticStage); ok {
			stage.record("verification_category", category)
		}
	}
	return token, err
}
