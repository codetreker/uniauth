package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oidcProvider struct {
	oauth    oauth2.Config
	client   *http.Client
	verifier *oidc.IDTokenVerifier
}

func (p *oidcProvider) authorization(request AuthorizationRequest, options ...oauth2.AuthCodeOption) (string, error) {
	if err := validateAuthorization(request, true); err != nil {
		return "", err
	}
	options = append(options, oauth2.S256ChallengeOption(request.CodeVerifier), oauth2.SetAuthURLParam("nonce", request.Nonce))
	return p.oauth.AuthCodeURL(request.State, options...), nil
}

func (p *oidcProvider) exchange(ctx context.Context, request ExchangeRequest) (*oauth2.Token, *oidc.IDToken, json.RawMessage, error) {
	if err := validateExchange(request, true); err != nil {
		return nil, nil, nil, failure("exchange", err)
	}
	token, err := observe(ctx, "token_exchange", func(ctx context.Context) (*oauth2.Token, error) {
		token, err := p.oauth.Exchange(oidc.ClientContext(ctx, p.client), request.Code, oauth2.VerifierOption(request.CodeVerifier))
		if err != nil {
			return nil, err
		}
		if token.AccessToken == "" || (token.TokenType != "Bearer" && token.TokenType != "bearer") {
			return nil, errors.New("bearer access token is required")
		}
		return token, nil
	})
	if err != nil {
		return nil, nil, nil, failure("token exchange", err)
	}
	verified, err := observe(ctx, "identity_verification", func(ctx context.Context) (verifiedToken, error) {
		return p.verify(ctx, token, request.ExpectedNonce)
	})
	if err != nil {
		return nil, nil, nil, failure("identity verification", err)
	}
	return token, verified.token, verified.claims, nil
}

type verifiedToken struct {
	token  *oidc.IDToken
	claims json.RawMessage
}

func (p *oidcProvider) verify(ctx context.Context, token *oauth2.Token, expectedNonce string) (verifiedToken, error) {
	rawToken, ok := token.Extra("id_token").(string)
	if !ok || rawToken == "" {
		return verifiedToken{}, errors.New("ID token is required")
	}
	idToken, err := p.verifier.Verify(ctx, rawToken)
	if err != nil {
		return verifiedToken{}, err
	}
	if idToken.Subject == "" || idToken.Nonce != expectedNonce {
		return verifiedToken{}, errors.New("subject or nonce is invalid")
	}
	var claims json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		return verifiedToken{}, err
	}
	fields, err := jsonObject(claims)
	if err != nil {
		return verifiedToken{}, err
	}
	var issuedAt int64
	if err := json.Unmarshal(fields["iat"], &issuedAt); err != nil || issuedAt <= 0 || idToken.IssuedAt.After(time.Now().Add(time.Minute)) || !idToken.Expiry.After(idToken.IssuedAt) {
		return verifiedToken{}, errors.New("issued-at claim is invalid")
	}
	if rawAZP, exists := fields["azp"]; exists {
		azp, err := nonemptyString(rawAZP)
		if err != nil || azp != p.oauth.ClientID {
			return verifiedToken{}, errors.New("authorized party does not match client")
		}
	} else if len(idToken.Audience) > 1 {
		return verifiedToken{}, errors.New("multiple audiences require an authorized party")
	}
	if idToken.AccessTokenHash != "" {
		if err := idToken.VerifyAccessToken(token.AccessToken); err != nil {
			return verifiedToken{}, err
		}
	}
	return verifiedToken{token: idToken, claims: claims}, nil
}
