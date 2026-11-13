package providers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const microsoftTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"
const microsoftIssuer = "https://login.microsoftonline.com/" + microsoftTenant + "/v2.0"
const microsoftEnrichmentTimeout = 3 * time.Second

type Microsoft struct {
	oidcProvider
	profileURL        string
	photoURL          string
	enrichmentTimeout time.Duration
}

func NewMicrosoft(ctx context.Context, config Config) (*Microsoft, error) {
	return newMicrosoft(ctx, config, microsoftIssuer, "https://login.microsoftonline.com/consumers/discovery/v2.0/keys", oauth2.Endpoint{
		AuthURL:   "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize",
		TokenURL:  "https://login.microsoftonline.com/consumers/oauth2/v2.0/token",
		AuthStyle: oauth2.AuthStyleInParams,
	}, "https://graph.microsoft.com/v1.0/me", "https://graph.microsoft.com/v1.0/me/photo/$value")
}

func newMicrosoft(ctx context.Context, config Config, issuer, jwksURL string, endpoint oauth2.Endpoint, profileURL, photoURL string) (*Microsoft, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	client := boundedClient(config.HTTPClient)
	keySet := oidc.NewRemoteKeySet(oidc.ClientContext(ctx, client), jwksURL)
	return &Microsoft{
		oidcProvider: oidcProvider{
			oauth: oauth2.Config{ClientID: config.ClientID, ClientSecret: config.ClientSecret, RedirectURL: config.RedirectURL,
				Endpoint: endpoint, Scopes: []string{"openid", "profile", "email", "https://graph.microsoft.com/User.Read"}},
			client:   client,
			verifier: oidc.NewVerifier(issuer, keySet, &oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: []string{oidc.RS256}}),
		},
		profileURL: profileURL, photoURL: photoURL, enrichmentTimeout: microsoftEnrichmentTimeout,
	}, nil
}

func (*Microsoft) Name() string { return "microsoft" }
func (p *Microsoft) AuthorizationURL(request AuthorizationRequest) (string, error) {
	return p.authorization(request, oauth2.SetAuthURLParam("prompt", "select_account"))
}

func (p *Microsoft) Exchange(ctx context.Context, request ExchangeRequest) (Identity, error) {
	token, identity, err := p.authenticate(ctx, request)
	if err != nil {
		return Identity{}, err
	}
	enrichmentCtx, cancel := context.WithTimeout(ctx, p.enrichmentTimeout)
	defer cancel()
	identity, unavailable := p.enrich(enrichmentCtx, token.AccessToken, identity)
	if len(unavailable) != 0 && ctx.Err() == nil {
		warnMicrosoftEnrichment(ctx, unavailable)
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, failure("exchange", err)
	}
	return identity, nil
}

func (p *Microsoft) authenticate(ctx context.Context, request ExchangeRequest) (*oauth2.Token, Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, p.client.Timeout)
	defer cancel()
	token, _, claims, err := p.exchange(ctx, request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			err = errors.Join(err, contextErr)
		}
		return nil, Identity{}, err
	}
	fields, err := jsonObject(claims)
	if err != nil {
		return nil, Identity{}, failure("identity verification", err)
	}
	tenant, err := nonemptyString(fields["tid"])
	if err != nil || tenant != microsoftTenant {
		return nil, Identity{}, failure("identity verification", errors.New("personal Microsoft tenant is required"))
	}
	objectID, err := nonemptyString(fields["oid"])
	if err != nil {
		return nil, Identity{}, failure("identity verification", err)
	}
	var tokenProfile struct {
		Email *string `json:"email"`
		Name  *string `json:"name"`
	}
	if err := json.Unmarshal(claims, &tokenProfile); err != nil {
		return nil, Identity{}, failure("identity normalization", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, Identity{}, failure("identity verification", err)
	}
	return token, Identity{Provider: p.Name(), ID: "msft:" + objectID + "@" + microsoftTenant,
		Email: nilIfEmpty(tokenProfile.Email), Name: nilIfEmpty(tokenProfile.Name),
		Origin: map[string]json.RawMessage{"id_token_claims": claims}}, nil
}
