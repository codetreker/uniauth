package sdkintegration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/providers"
	uniauth "github.com/codetreker/uniauth/sdk/go"
	"github.com/go-jose/go-jose/v4"
)

const (
	personalTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"
	organizationA  = "11111111-2222-3333-4444-555555555555"
	organizationB  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	objectID       = "shared-object"
	issuerTemplate = "https://login.microsoftonline.com/{tenantid}/v2.0"
)

type microsoftTransport struct {
	key *rsa.PrivateKey
}

func (m microsoftTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	switch r.URL.String() {
	case "https://login.microsoftonline.com/common/discovery/v2.0/keys":
		data, err := json.Marshal(jose.JSONWebKey{Key: &m.key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"})
		if err != nil {
			return nil, err
		}
		var key map[string]any
		if err := json.Unmarshal(data, &key); err != nil {
			return nil, err
		}
		key["issuer"] = issuerTemplate
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"keys": []any{key}}); err != nil {
			return nil, err
		}
	case "https://login.microsoftonline.com/common/oauth2/v2.0/token":
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		parts := strings.SplitN(r.Form.Get("code"), ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			return nil, fmt.Errorf("invalid fixture code")
		}
		tenant, nonce := parts[0], parts[1]
		claims, err := json.Marshal(map[string]any{
			"iss": "https://login.microsoftonline.com/" + tenant + "/v2.0", "tid": tenant,
			"oid": objectID, "sub": "subject", "aud": "client-id", "nonce": nonce,
			"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"unknown": map[string]any{"nested": []any{nil, false}},
		})
		if err != nil {
			return nil, err
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: m.key, KeyID: "fixture"}}, nil)
		if err != nil {
			return nil, err
		}
		signed, err := signer.Sign(claims)
		if err != nil {
			return nil, err
		}
		token, err := signed.CompactSerialize()
		if err != nil {
			return nil, err
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture", "token_type": "Bearer", "id_token": token}); err != nil {
			return nil, err
		}
	case "https://graph.microsoft.com/v1.0/me", "https://graph.microsoft.com/v1.0/me/photo/$value":
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		return nil, fmt.Errorf("unexpected upstream request: %s", r.URL)
	}
	return w.Result(), nil
}

func newMicrosoftGateway(t *testing.T) *gateway {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := providers.NewMicrosoft(t.Context(), providers.Config{
		ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://gateway.example/auth/callback/microsoft",
		HTTPClient: &http.Client{Transport: microsoftTransport{key: key}, Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	return newGatewayWithProvider(t, provider)
}

func TestGoSDKMicrosoftTenantsAgainstGateway(t *testing.T) {
	g := newMicrosoftGateway(t)
	client := g.client(t, g.app)
	for _, tenant := range []string{personalTenant, organizationA, organizationB} {
		t.Run(tenant, func(t *testing.T) {
			identity, err := client.Retrieve(t.Context(), g.loginProvider(t, client, uniauth.Microsoft, tenant))
			if err != nil {
				t.Fatal(err)
			}
			if identity.Provider != uniauth.Microsoft || identity.ID != "msft:"+objectID+"@"+tenant || identity.Email != nil || identity.EmailVerified != nil || identity.Name != nil || identity.AvatarURL != nil {
				t.Fatalf("unexpected normalized identity: %#v", identity)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(identity.RawJSON, &wire); err != nil || len(wire) != 7 {
				t.Fatalf("invalid identity envelope: %s (%v)", identity.RawJSON, err)
			}
			var claims map[string]json.RawMessage
			if err := json.Unmarshal(identity.Origin["id_token_claims"], &claims); err != nil || string(claims["tid"]) != `"`+tenant+`"` || string(claims["unknown"]) != `{"nested":[null,false]}` {
				t.Fatalf("unexpected claims: %s (%v)", identity.Origin["id_token_claims"], err)
			}
		})
	}
}

func TestJavaScriptSDKMicrosoftTenantsAgainstGateway(t *testing.T) {
	g := newMicrosoftGateway(t)
	payload, err := json.Marshal(map[string]any{"baseURL": g.server.URL, "app": g.app, "redirectURI": productRedirect, "tenants": []string{personalTenant, organizationA, organizationB}, "objectID": objectID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "integration-microsoft.mjs")
	command.Stdin = bytes.NewReader(payload)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatalf("JavaScript Microsoft integration: %v", err)
	}
}
