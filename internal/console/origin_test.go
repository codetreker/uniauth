package console

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codetreker/uniauth/internal/apps"
)

func TestBrowserOriginFromConfiguredURL(t *testing.T) {
	tests := []struct {
		publicURL string
		origin    string
		loopback  bool
	}{
		{"https://auth.example:443", "https://auth.example", false},
		{"https://AUTH.example", "https://auth.example", false},
		{"https://AUTH.example:00443/", "https://auth.example", false},
		{"https://AUTH.example:08443", "https://auth.example:8443", false},
		{"https://AUTH.example:04430", "https://auth.example:4430", false},
		{"https://[2001:0DB8:0:0:0:0:0:1]:443", "https://[2001:db8::1]", false},
		{"https://[2001:0DB8:0:0:0:0:0:1]:08443", "https://[2001:db8::1]:8443", false},
		{"https://[::ffff:127.0.0.1]:443", "https://[::ffff:7f00:1]", false},
		{"https://BÜCHER.example:443", "https://xn--bcher-kva.example", false},
		{"https://auth.example:", "https://auth.example", false},
		{"https://127.0.0.1:443", "https://127.0.0.1", false},
		{"https://AUTH.example:00000", "https://auth.example:0", false},
		{"http://LOCALHOST:00080", "http://localhost", true},
		{"http://[0:0:0:0:0:0:0:1]:08080", "http://[::1]:8080", true},
	}
	for _, tt := range tests {
		t.Run(tt.publicURL, func(t *testing.T) {
			e := newTestConsole(t, func(cfg *Config) { cfg.PublicURL = tt.publicURL; cfg.AllowLoopbackHTTP = tt.loopback })
			response := e.request("POST", "/console/api/login", `{"password":"correct horse battery staple"}`, map[string]string{"Origin": tt.origin})
			requireStatus(t, response, http.StatusOK)
			e.cookie = response.Result().Cookies()[0]
			e.csrf = decodeResponse[map[string]string](t, response)["csrf_token"]
			response = e.request("POST", "/console/api/apps", appBody, map[string]string{"Origin": tt.origin})
			requireStatus(t, response, http.StatusCreated)
			created := decodeResponse[apps.CreatedApp](t, response)
			path := "/console/api/apps/" + created.App.ID
			response = e.request("PUT", path, strings.Replace(appBody, `"enabled":true`, `"enabled":false`, 1), map[string]string{"Origin": tt.origin})
			requireStatus(t, response, http.StatusOK)
			requireStatus(t, e.request("DELETE", path, "", map[string]string{"Origin": tt.origin}), http.StatusNoContent)
			wrongOrigins := []string{"null", tt.origin + "/", tt.origin + ".evil.example", "https://evil.example", "HTTPS://AUTH.EXAMPLE"}
			if tt.publicURL != tt.origin && !strings.HasSuffix(tt.publicURL, "/") {
				wrongOrigins = append(wrongOrigins, tt.publicURL)
			}
			for _, origin := range wrongOrigins {
				requireStatus(t, e.request("POST", "/console/api/login", `{"password":"correct horse battery staple"}`, map[string]string{"Origin": origin}), http.StatusForbidden)
				requireStatus(t, e.request("POST", "/console/api/apps", appBody, map[string]string{"Origin": origin}), http.StatusForbidden)
			}
			requireStatus(t, e.request("POST", "/console/api/apps", appBody, map[string]string{"Origin": tt.origin, "X-CSRF-Token": "invalid"}), http.StatusForbidden)
			for _, port := range []string{":8443", ":4430"} {
				if strings.HasSuffix(tt.origin, port) {
					requireStatus(t, e.request("POST", "/console/api/login", `{"password":"correct horse battery staple"}`, map[string]string{"Origin": strings.TrimSuffix(tt.origin, port)}), http.StatusForbidden)
				}
			}
		})
	}
}

func TestInvalidConfiguredBrowserOriginFailsConstruction(t *testing.T) {
	e := newTestConsole(t, nil)
	for _, publicURL := range []string{"https://auth.example:65536", "https://auth.example:999999999999999999999", "https://[fe80::1%25eth0]", "https://[invalid:address]", "https://[127.0.0.1]", "https://[v1.fe80]", "https://invalid\u200d.example", "https://127.000.000.001", "https://0x7f000001", "https://2130706433", "https://127.1", "https://127.0.0.1.", "https://auth.123", "https://auth.0x"} {
		t.Run(publicURL, func(t *testing.T) {
			cfg := e.handler.config
			cfg.PublicURL = publicURL
			if _, err := New(e.store, nil, cfg); err == nil {
				t.Fatalf("browser-invalid public URL was accepted: %s", publicURL)
			}
		})
	}
}
