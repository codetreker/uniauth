package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"golang.org/x/oauth2"
)

type GitHub struct {
	oauth      oauth2.Config
	client     *http.Client
	profileURL string
	emailsURL  string
}

func NewGitHub(config Config) (*GitHub, error) {
	return newGitHub(config, oauth2.Endpoint{AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", AuthStyle: oauth2.AuthStyleInParams}, "https://api.github.com/user", "https://api.github.com/user/emails")
}

func newGitHub(config Config, endpoint oauth2.Endpoint, profileURL, emailsURL string) (*GitHub, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &GitHub{
		oauth: oauth2.Config{ClientID: config.ClientID, ClientSecret: config.ClientSecret, RedirectURL: config.RedirectURL,
			Endpoint: endpoint, Scopes: []string{"read:user", "user:email"}},
		client: boundedClient(config.HTTPClient), profileURL: profileURL, emailsURL: emailsURL,
	}, nil
}

func (*GitHub) Name() string { return "github" }
func (p *GitHub) AuthorizationURL(request AuthorizationRequest) (string, error) {
	if err := validateAuthorization(request, false); err != nil {
		return "", err
	}
	return p.oauth.AuthCodeURL(request.State, oauth2.S256ChallengeOption(request.CodeVerifier)), nil
}

func (p *GitHub) Exchange(ctx context.Context, request ExchangeRequest) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, p.client.Timeout)
	defer cancel()
	if err := validateExchange(request, false); err != nil {
		return Identity{}, failure("exchange", err)
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
	token, err := p.oauth.Exchange(ctx, request.Code, oauth2.VerifierOption(request.CodeVerifier))
	if err != nil {
		return Identity{}, failure("token exchange", err)
	}
	if token.AccessToken == "" || (token.TokenType != "Bearer" && token.TokenType != "bearer") {
		return Identity{}, failure("token exchange", errors.New("bearer access token is required"))
	}
	profile, _, _, err := fetch(ctx, p.client, p.profileURL, token.AccessToken)
	if err != nil {
		return Identity{}, failure("profile retrieval", err)
	}
	fields, err := jsonObject(profile)
	if err != nil {
		return Identity{}, failure("profile retrieval", err)
	}
	var id int64
	if err := json.Unmarshal(fields["id"], &id); err != nil || id <= 0 {
		return Identity{}, failure("identity verification", errors.New("positive numeric GitHub ID is required"))
	}
	emails, err := p.emails(ctx, token.AccessToken)
	if err != nil {
		return Identity{}, failure("email retrieval", err)
	}
	return Identity{Provider: p.Name(), Subject: p.Name() + ":" + strconv.FormatInt(id, 10),
		Sources: map[string]json.RawMessage{"profile": profile, "emails": emails}}, nil
}

func (p *GitHub) emails(ctx context.Context, accessToken string) (json.RawMessage, error) {
	endpoint, err := url.Parse(p.emailsURL)
	if err != nil {
		return nil, err
	}
	var records []json.RawMessage
	totalBytes := 0
	for page := 1; ; page++ {
		query := endpoint.Query()
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = query.Encode()
		body, _, _, err := fetch(ctx, p.client, endpoint.String(), accessToken)
		if err != nil {
			return nil, err
		}
		totalBytes += len(body)
		if totalBytes > maxResponseBytes {
			return nil, errResponseTooLarge
		}
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, errors.New("provider email array is required")
		}
		for _, record := range batch {
			if _, err := jsonObject(record); err != nil {
				return nil, err
			}
		}
		if page == 1 && len(batch) < 100 {
			return body, nil
		}
		records = append(records, batch...)
		if len(batch) < 100 {
			return json.Marshal(records)
		}
	}
}
