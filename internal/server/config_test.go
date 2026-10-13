package server

import (
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	valid := map[string]string{"UNIAUTH_PUBLIC_URL": "https://auth.example.com", "UNIAUTH_ADMIN_PASSWORD": "  $直接-password  "}
	getenv := func(name string) string { return valid[name] }
	cfg, err := LoadConfig(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.DatabasePath != "uniauth.db" || cfg.AllowLoopbackHTTP || cfg.AdminPassword != valid["UNIAUTH_ADMIN_PASSWORD"] {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	valid["UNIAUTH_PUBLIC_URL"] = "http://localhost:8080"
	valid["UNIAUTH_ALLOW_LOOPBACK_HTTP"] = "true"
	valid["UNIAUTH_LISTEN_ADDR"] = "127.0.0.1:8080"
	valid["UNIAUTH_DATABASE_PATH"] = "data/apps.db"
	valid["UNIAUTH_GITHUB_CLIENT_ID"] = "client"
	valid["UNIAUTH_GITHUB_CLIENT_SECRET"] = "secret"
	cfg, err = LoadConfig(getenv)
	if err != nil || !cfg.AllowLoopbackHTTP || cfg.GitHub.ClientID != "client" || cfg.ListenAddr != "127.0.0.1:8080" || cfg.DatabasePath != "data/apps.db" {
		t.Fatalf("explicit config: %+v %v", cfg, err)
	}
}

func TestRejectInvalidConfiguration(t *testing.T) {
	cases := []struct{ name, value string }{
		{"UNIAUTH_PUBLIC_URL", ""}, {"UNIAUTH_PUBLIC_URL", "http://auth.example.com"}, {"UNIAUTH_PUBLIC_URL", "https://auth.example.com/path"}, {"UNIAUTH_PUBLIC_URL", "https://auth.example.com?"}, {"UNIAUTH_PUBLIC_URL", "https://auth.example.com#"}, {"UNIAUTH_PUBLIC_URL", "https://user@auth.example.com"}, {"UNIAUTH_PUBLIC_URL", ":"},
		{"UNIAUTH_ADMIN_PASSWORD", ""}, {"UNIAUTH_ADMIN_PASSWORD", strings.Repeat("x", 1025)}, {"UNIAUTH_LISTEN_ADDR", "8080"}, {"UNIAUTH_ALLOW_LOOPBACK_HTTP", "yes"}, {"UNIAUTH_GITHUB_CLIENT_ID", "client"}, {"UNIAUTH_GOOGLE_CLIENT_SECRET", "secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			values := map[string]string{"UNIAUTH_PUBLIC_URL": "https://auth.example.com", "UNIAUTH_ADMIN_PASSWORD": "  $直接-password  ", tc.name: tc.value}
			_, err := LoadConfig(func(name string) string { return values[name] })
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	cfg := Config{PublicURL: "https://auth.example.com", ListenAddr: ":8080", AdminPassword: "password"}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_PATH") {
		t.Fatalf("empty database path: %v", err)
	}
	cfg.DatabasePath = "apps.db"
	cfg.Google = Credentials{" client", "secret"}
	if err := cfg.validate(); err == nil {
		t.Fatal("whitespace client ID accepted")
	}
}

func TestAdministratorPasswordConfigurationContract(t *testing.T) {
	cases := []struct {
		name     string
		password string
		valid    bool
	}{
		{"one byte", " ", true},
		{"1024 bytes", strings.Repeat("界", 341) + "$", true},
		{"literal password", "  $直接-password  ", true},
		{"LF", "password\n", false},
		{"CR", "password\r", false},
		{"CRLF", "password\r\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{PublicURL: "https://auth.example.com", ListenAddr: ":8080", DatabasePath: "apps.db", AdminPassword: tc.password}
			directErr := cfg.validate()
			values := map[string]string{"UNIAUTH_PUBLIC_URL": cfg.PublicURL, "UNIAUTH_ADMIN_PASSWORD": tc.password}
			loaded, loadErr := LoadConfig(func(name string) string { return values[name] })
			if tc.valid {
				if directErr != nil || loadErr != nil {
					t.Fatalf("usable password rejected: direct=%v environment=%v", directErr, loadErr)
				}
				if loaded.AdminPassword != tc.password {
					t.Fatal("configured password changed")
				}
			} else {
				for _, err := range []error{directErr, loadErr} {
					if err == nil || !strings.Contains(err.Error(), "UNIAUTH_ADMIN_PASSWORD") {
						t.Fatalf("multiline configuration did not fail with a password diagnostic: %v", err)
					}
					if strings.Contains(err.Error(), "password\r") || strings.Contains(err.Error(), "password\n") {
						t.Fatal("configuration diagnostic disclosed password")
					}
				}
			}
		})
	}
}
