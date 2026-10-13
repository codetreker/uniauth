package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const microsoftTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"
const microsoftIssuer = "https://login.microsoftonline.com/" + microsoftTenant + "/v2.0"

type Microsoft struct {
	oidcProvider
	profileURL string
	photoURL   string
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
		profileURL: profileURL, photoURL: photoURL,
	}, nil
}

func (*Microsoft) Name() string { return "microsoft" }
func (p *Microsoft) AuthorizationURL(request AuthorizationRequest) (string, error) {
	return p.authorization(request)
}

func (p *Microsoft) Exchange(ctx context.Context, request ExchangeRequest) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, p.client.Timeout)
	defer cancel()
	token, _, claims, err := p.exchange(ctx, request)
	if err != nil {
		return Identity{}, err
	}
	fields, err := jsonObject(claims)
	if err != nil {
		return Identity{}, failure("identity verification", err)
	}
	tenant, err := nonemptyString(fields["tid"])
	if err != nil || tenant != microsoftTenant {
		return Identity{}, failure("identity verification", errors.New("personal Microsoft tenant is required"))
	}
	objectID, err := nonemptyString(fields["oid"])
	if err != nil {
		return Identity{}, failure("identity verification", err)
	}
	profile, _, _, err := fetch(ctx, p.client, p.profileURL, token.AccessToken)
	if err != nil {
		return Identity{}, failure("profile retrieval", err)
	}
	if _, err := jsonObject(profile); err != nil {
		return Identity{}, failure("profile retrieval", err)
	}
	var graphProfile struct {
		Mail        *string `json:"mail"`
		DisplayName *string `json:"displayName"`
	}
	if err := json.Unmarshal(profile, &graphProfile); err != nil {
		return Identity{}, failure("identity normalization", err)
	}
	var tokenProfile struct {
		Email *string `json:"email"`
		Name  *string `json:"name"`
	}
	if err := json.Unmarshal(claims, &tokenProfile); err != nil {
		return Identity{}, failure("identity normalization", err)
	}
	email := nilIfEmpty(graphProfile.Mail)
	if email == nil {
		email = nilIfEmpty(tokenProfile.Email)
	}
	name := nilIfEmpty(graphProfile.DisplayName)
	if name == nil {
		name = nilIfEmpty(tokenProfile.Name)
	}
	photo, contentType, status, err := fetchWithAccept(ctx, p.client, p.photoURL, token.AccessToken, "image/*")
	encodedPhoto := json.RawMessage("null")
	var avatarURL *string
	if err != nil && status != http.StatusNotFound {
		return Identity{}, failure("photo retrieval", err)
	}
	if err == nil {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || len(mediaType) < 6 || mediaType[:6] != "image/" || len(photo) == 0 {
			return Identity{}, failure("photo retrieval", errors.New("provider photo has invalid content"))
		}
		base64Photo := base64.StdEncoding.EncodeToString(photo)
		dataURL := "data:" + mediaType + ";base64," + base64Photo
		avatarURL = &dataURL
		encodedPhoto, err = json.Marshal(struct {
			ContentType string `json:"content_type"`
			DataBase64  string `json:"data_base64"`
		}{ContentType: contentType, DataBase64: base64Photo})
		if err != nil {
			return Identity{}, failure("photo encoding", err)
		}
	}
	return Identity{Provider: p.Name(), ID: p.Name() + ":" + microsoftTenant + ":" + objectID,
		Email: email, Name: name, AvatarURL: avatarURL,
		Origin: map[string]json.RawMessage{"id_token_claims": claims, "profile": profile, "photo": encodedPhoto}}, nil
}
