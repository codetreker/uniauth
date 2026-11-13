package console

import (
	"context"
	"testing"

	"github.com/codetreker/uniauth/internal/apps"
)

func TestOAuthMicrosoftSparseProfilePreservesAccountAuthorityAndOwnership(t *testing.T) {
	for _, mode := range []string{"profile-token", "profile-empty"} {
		t.Run(mode, func(t *testing.T) {
			e := newTestConsole(t, nil)
			e.login(t)
			accountID := e.account.ID
			createdResponse := e.request("POST", "/console/api/apps", createAppBody, nil)
			requireStatus(t, createdResponse, 201)
			created := decodeResponse[apps.CreatedApp](t, createdResponse)
			link := e.startFlow(t, "microsoft", "/console/api/identities/link")
			location := e.gatewayCallback(t, link, "microsoft", "profile-full")
			if response := e.consoleCallback(t, location, link.pending, e.cookie); response.Header().Get("Location") != "/console/" {
				t.Fatalf("Microsoft identity was not linked: %v", response.Header())
			}
			requireStatus(t, e.request("POST", "/console/api/logout", "", nil), 204)
			e.cookie, e.csrf = nil, ""
			full := e.startFlow(t, "microsoft", "/console/api/login")
			location = e.gatewayCallback(t, full, "microsoft", "profile-full")
			e.acceptLogin(t, e.consoleCallback(t, location, full.pending))
			if e.account.ID != accountID || !e.account.IsAdmin || e.account.Email == nil || *e.account.Email != "graph@example.test" || e.account.AvatarURL == nil {
				t.Fatalf("complete Microsoft profile did not resolve the linked owner: %+v", e.account)
			}
			requireStatus(t, e.request("POST", "/console/api/logout", "", nil), 204)
			e.cookie, e.csrf = nil, ""
			partial := e.startFlow(t, "microsoft", "/console/api/login")
			location = e.gatewayCallback(t, partial, "microsoft", mode)
			e.acceptLogin(t, e.consoleCallback(t, location, partial.pending))
			if e.account.ID != accountID || !e.account.IsAdmin || e.account.AvatarURL != nil {
				t.Fatalf("partial profile changed account authority or retained stale avatar: %+v", e.account)
			}
			if mode == "profile-empty" {
				if e.account.Email != nil || e.account.Name != nil {
					t.Fatalf("missing profile retained stale attributes: %+v", e.account)
				}
			} else if e.account.Email == nil || *e.account.Email != "token@example.test" || e.account.Name == nil || *e.account.Name != "Token Name" {
				t.Fatalf("token attributes did not refresh the account: %+v", e.account)
			}
			owned := e.request("GET", "/console/api/apps/"+created.App.ID, "", nil)
			requireStatus(t, owned, 200)
			if decodeResponse[apps.App](t, owned).ID != created.App.ID {
				t.Fatal("existing App ownership changed")
			}
			identities, err := e.store.ListIdentities(context.Background(), accountID)
			if err != nil || len(identities) != 2 {
				t.Fatalf("partial login created another identity: %+v %v", identities, err)
			}
			for _, identity := range identities {
				if identity.Provider == "microsoft" && identity.EmailVerified != nil {
					t.Fatal("partial Microsoft login invented email verification")
				}
			}
		})
	}
}
