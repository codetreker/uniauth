package server

import (
	"strings"
	"testing"
)

func environmentForTest() map[string]string {
	return map[string]string{
		"UNIAUTH_PUBLIC_URL": "https://auth.example.com", "UNIAUTH_INITIAL_ADMIN_EMAIL": "admin@example.com",
		"UNIAUTH_GITHUB_CLIENT_ID": "client", "UNIAUTH_GITHUB_CLIENT_SECRET": "secret",
	}
}

func TestLoadConfig(t *testing.T) {
	values := environmentForTest()
	getenv := func(name string) string { return values[name] }
	cfg, err := LoadConfig(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.DatabasePath != "uniauth.db" || cfg.AllowLoopbackHTTP || cfg.InitialAdminEmail != values["UNIAUTH_INITIAL_ADMIN_EMAIL"] {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	values["UNIAUTH_PUBLIC_URL"] = "http://localhost:8080"
	values["UNIAUTH_ALLOW_LOOPBACK_HTTP"] = "true"
	values["UNIAUTH_LISTEN_ADDR"] = "127.0.0.1:8080"
	values["UNIAUTH_DATABASE_PATH"] = "data/apps.db"
	cfg, err = LoadConfig(getenv)
	if err != nil || !cfg.AllowLoopbackHTTP || cfg.GitHub.ClientID != "client" || cfg.ListenAddr != "127.0.0.1:8080" || cfg.DatabasePath != "data/apps.db" {
		t.Fatalf("explicit config: %+v %v", cfg, err)
	}
}

func TestRejectInvalidConfiguration(t *testing.T) {
	cases := []struct{ name, value string }{
		{"UNIAUTH_PUBLIC_URL", ""}, {"UNIAUTH_PUBLIC_URL", "http://auth.example.com"}, {"UNIAUTH_PUBLIC_URL", "https://auth.example.com/path"},
		{"UNIAUTH_PUBLIC_URL", "https://auth.example.com?"}, {"UNIAUTH_PUBLIC_URL", "https://auth.example.com#"}, {"UNIAUTH_PUBLIC_URL", "https://user@auth.example.com"}, {"UNIAUTH_PUBLIC_URL", ":"},
		{"UNIAUTH_LISTEN_ADDR", "8080"}, {"UNIAUTH_ALLOW_LOOPBACK_HTTP", "yes"}, {"UNIAUTH_GITHUB_CLIENT_ID", ""},
		{"UNIAUTH_GOOGLE_CLIENT_SECRET", "secret"}, {"UNIAUTH_MICROSOFT_CLIENT_ID", "client"},
		{"UNIAUTH_GITHUB_CLIENT_ID", " client"}, {"UNIAUTH_GITHUB_CLIENT_SECRET", "secret "},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			values := environmentForTest()
			values[tc.name] = tc.value
			if _, err := LoadConfig(func(name string) string { return values[name] }); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	cfg := Config{PublicURL: "https://auth.example.com", ListenAddr: ":8080", InitialAdminEmail: "admin@example.com"}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_PATH") {
		t.Fatalf("empty database path: %v", err)
	}
}

func TestInitialAdministratorEmailConfigurationContract(t *testing.T) {
	maximum := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, tc := range []struct {
		name, email string
		valid       bool
	}{
		{"ordinary", "sychenjianjun@gmail.com", true}, {"plus address", "name+console@example.com", true},
		{"case preserved", "Admin@Example.com", true}, {"254 bytes", maximum, true},
		{"empty", "", false}, {"255 bytes", maximum + "e", false}, {"missing domain", "admin", false},
		{"display name", "Admin <admin@example.com>", false}, {"address brackets", "<admin@example.com>", false},
		{"leading whitespace", " admin@example.com", false}, {"trailing whitespace", "admin@example.com ", false},
		{"LF", "admin@example.com\n", false}, {"CR", "admin@example.com\r", false},
		{"quoted whitespace", "\"admin name\"@example.com", false}, {"unicode whitespace", "admin\u00a0@example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := environmentForTest()
			values["UNIAUTH_INITIAL_ADMIN_EMAIL"] = tc.email
			cfg := Config{PublicURL: values["UNIAUTH_PUBLIC_URL"], ListenAddr: ":8080", DatabasePath: "apps.db", InitialAdminEmail: tc.email}
			directErr := cfg.validate()
			loaded, loadErr := LoadConfig(func(name string) string { return values[name] })
			if tc.valid {
				if directErr != nil || loadErr != nil || loaded.InitialAdminEmail != tc.email {
					t.Fatalf("valid email changed or rejected: direct=%v environment=%v", directErr, loadErr)
				}
				return
			}
			for _, err := range []error{directErr, loadErr} {
				if err == nil || !strings.Contains(err.Error(), "UNIAUTH_INITIAL_ADMIN_EMAIL") {
					t.Fatalf("invalid email accepted: %v", err)
				}
				if tc.email != "" && strings.Contains(err.Error(), tc.email) {
					t.Fatal("configuration diagnostic disclosed email")
				}
			}
		})
	}
}

func TestProviderConfigurationAvailability(t *testing.T) {
	for _, provider := range []string{"MICROSOFT", "GOOGLE", "GITHUB"} {
		t.Run(provider, func(t *testing.T) {
			values := environmentForTest()
			delete(values, "UNIAUTH_GITHUB_CLIENT_ID")
			delete(values, "UNIAUTH_GITHUB_CLIENT_SECRET")
			if _, err := LoadConfig(func(name string) string { return values[name] }); err == nil || !strings.Contains(err.Error(), "provider") {
				t.Fatalf("provider-free deployment accepted: %v", err)
			}
			values["UNIAUTH_"+provider+"_CLIENT_ID"] = "client"
			values["UNIAUTH_"+provider+"_CLIENT_SECRET"] = "secret"
			if _, err := LoadConfig(func(name string) string { return values[name] }); err != nil {
				t.Fatal(err)
			}
		})
	}
}
