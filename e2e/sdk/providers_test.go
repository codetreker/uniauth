package sdkintegration

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/codetreker/uniauth/internal/apps"
	uniauth "github.com/codetreker/uniauth/sdk/go"
)

func updateDiscoveryApp(t *testing.T, g *gateway, input apps.AppInput) {
	t.Helper()
	var err error
	g.app.App, err = g.store.Update(t.Context(), g.ownerID, g.app.App.ID, input)
	if err != nil {
		t.Fatal(err)
	}
}

func discoveryInput(g *gateway) apps.AppInput {
	return apps.AppInput{Name: g.app.App.Name, Enabled: g.app.App.Enabled, RedirectURIs: g.app.App.RedirectURIs, Providers: g.app.App.Providers}
}

func TestGoSDKProviderDiscoveryAgainstGateway(t *testing.T) {
	g := newGateway(t)
	client := g.client(t, g.app)
	available, err := client.Providers(t.Context(), productRedirect)
	if err != nil || !slices.Equal(available, []uniauth.Provider{uniauth.Google}) {
		t.Fatalf("configured App providers: %v %v", available, err)
	}
	// Configuration reads leave both the product state and one-time result untouched.
	code := g.login(t, client)
	for range 2 {
		if _, err := client.Providers(t.Context(), productRedirect); err != nil {
			t.Fatal(err)
		}
	}
	identity, err := client.Retrieve(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	assertIdentity(t, identity)

	input := discoveryInput(g)
	input.Providers = []string{"github", "microsoft"}
	updateDiscoveryApp(t, g, input)
	available, err = client.Providers(t.Context(), productRedirect)
	if err != nil || available == nil || len(available) != 0 {
		t.Fatalf("empty configured intersection: %v %v", available, err)
	}
	input.Providers = []string{"github", "google", "microsoft"}
	updateDiscoveryApp(t, g, input)
	available, err = client.Providers(t.Context(), productRedirect)
	if err != nil || !slices.Equal(available, []uniauth.Provider{uniauth.Google}) {
		t.Fatalf("unconfigured upstreams included: %v %v", available, err)
	}
	for _, callback := range []string{"http://product.example/callback?existing=%70reserved", "https://product.example/callback?existing=preserved", "http://product.example/callback?existing=changed"} {
		_, err = client.Providers(t.Context(), callback)
		assertAPIError(t, err, http.StatusBadRequest, "invalid_request")
	}
	incorrect := g.app
	incorrect.Secret = "incorrect-discovery-secret"
	_, err = g.client(t, incorrect).Providers(t.Context(), productRedirect)
	assertAPIError(t, err, http.StatusUnauthorized, "invalid_app")

	input.Enabled = false
	updateDiscoveryApp(t, g, input)
	_, err = client.Providers(t.Context(), productRedirect)
	assertAPIError(t, err, http.StatusUnauthorized, "invalid_app")
	input.Enabled = true
	updateDiscoveryApp(t, g, input)
	if _, err = client.Providers(t.Context(), productRedirect); err != nil {
		t.Fatal(err)
	}

	replacement, err := g.store.ResetSecret(t.Context(), g.ownerID, g.app.App.ID, g.app.SecretID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Providers(t.Context(), productRedirect)
	assertAPIError(t, err, http.StatusUnauthorized, "invalid_app")
	rotated := g.app
	rotated.Secret, rotated.SecretID = replacement.Secret, replacement.ID
	replacementClient := g.client(t, rotated)
	if _, err := replacementClient.Providers(t.Context(), productRedirect); err != nil {
		t.Fatal(err)
	}
	if err := g.store.RevokeSecret(t.Context(), g.ownerID, g.app.App.ID, replacement.ID); err != nil {
		t.Fatal(err)
	}
	_, err = replacementClient.Providers(t.Context(), productRedirect)
	assertAPIError(t, err, http.StatusUnauthorized, "invalid_app")
	if _, err := g.client(t, g.other).Providers(t.Context(), productRedirect); err != nil {
		t.Fatal("independent App credentials were affected")
	}
	if err := g.store.Delete(context.Background(), g.ownerID, g.other.App.ID); err != nil {
		t.Fatal(err)
	}
	_, err = g.client(t, g.other).Providers(t.Context(), productRedirect)
	assertAPIError(t, err, http.StatusUnauthorized, "invalid_app")
}
