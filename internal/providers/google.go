package providers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Google struct {
	oidcProvider
	userinfoURL string
}

func NewGoogle(ctx context.Context, config Config) (*Google, error) {
	return newGoogle(ctx, config, "https://accounts.google.com")
}

func newGoogle(ctx context.Context, config Config, issuer string) (*Google, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	client := boundedClient(config.HTTPClient)
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), issuer)
	if err != nil {
		return nil, failure("discovery", err)
	}
	var metadata struct {
		UserinfoURL string `json:"userinfo_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, failure("discovery", err)
	}
	if metadata.UserinfoURL == "" {
		return nil, failure("discovery", errors.New("userinfo endpoint is required"))
	}
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return &Google{oidcProvider: oidcProvider{
		oauth: oauth2.Config{ClientID: config.ClientID, ClientSecret: config.ClientSecret, RedirectURL: config.RedirectURL,
			Endpoint: endpoint, Scopes: []string{"openid", "profile", "email"}},
		client: client, verifier: provider.Verifier(&oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: []string{oidc.RS256}}),
	}, userinfoURL: metadata.UserinfoURL}, nil
}

func (*Google) Name() string { return "google" }
func (p *Google) AuthorizationURL(request AuthorizationRequest) (string, error) {
	return p.authorization(request)
}

func (p *Google) Exchange(ctx context.Context, request ExchangeRequest) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, p.client.Timeout)
	defer cancel()
	token, idToken, claims, err := p.exchange(ctx, request)
	if err != nil {
		return Identity{}, err
	}
	userinfo, _, _, err := fetch(ctx, p.client, p.userinfoURL, token.AccessToken)
	if err != nil {
		return Identity{}, failure("userinfo retrieval", err)
	}
	fields, err := jsonObject(userinfo)
	if err != nil {
		return Identity{}, failure("userinfo retrieval", err)
	}
	subject, err := nonemptyString(fields["sub"])
	if err != nil || subject != idToken.Subject {
		return Identity{}, failure("identity verification", errors.New("userinfo subject does not match ID token"))
	}
	var profile struct {
		Email         *string `json:"email"`
		EmailVerified *bool   `json:"email_verified"`
		Name          *string `json:"name"`
		Picture       *string `json:"picture"`
	}
	if err := json.Unmarshal(userinfo, &profile); err != nil {
		return Identity{}, failure("identity normalization", err)
	}
	return Identity{Provider: p.Name(), ID: p.Name() + ":" + idToken.Subject,
		Email: nilIfEmpty(profile.Email), EmailVerified: profile.EmailVerified,
		Name: nilIfEmpty(profile.Name), AvatarURL: nilIfEmpty(profile.Picture),
		Origin: map[string]json.RawMessage{"id_token_claims": claims, "userinfo": userinfo}}, nil
}
