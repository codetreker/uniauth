package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func TestProviderNormalizedIdentity(t *testing.T) {
	cases := []struct {
		name, provider string
		mutate         func(*fixture)
		want           string
	}{
		{
			name: "Google UserInfo is canonical", provider: "google",
			mutate: func(f *fixture) {
				f.claims["email"] = "token@example.test"
				f.claims["name"] = "Token Name"
				f.claims["email_verified"] = false
				f.userinfo = `{"sub":"user-123","email":"profile@example.test","email_verified":true,"name":"Profile Name","picture":"https://images.example/google.png","unknown":{"nil":null,"boolean":false}}`
			},
			want: `{"email":"profile@example.test","email_verified":true,"name":"Profile Name","avatar_url":"https://images.example/google.png"}`,
		},
		{
			name: "Google false is preserved", provider: "google",
			mutate: func(f *fixture) {
				f.userinfo = `{"sub":"user-123","email":"profile@example.test","email_verified":false}`
			},
			want: `{"email":"profile@example.test","email_verified":false,"name":null,"avatar_url":null}`,
		},
		{
			name: "Google missing fields", provider: "google",
			mutate: func(f *fixture) {
				f.claims["email"] = "token@example.test"
				f.claims["name"] = "Token Name"
				f.userinfo = `{"sub":"user-123"}`
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":null}`,
		},
		{
			name: "Google null fields", provider: "google",
			mutate: func(f *fixture) {
				f.userinfo = `{"sub":"user-123","email":null,"email_verified":null,"name":null,"picture":null}`
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":null}`,
		},
		{
			name: "Google empty strings", provider: "google",
			mutate: func(f *fixture) {
				f.userinfo = `{"sub":"user-123","email":"","name":"","picture":"","email_verified":false}`
			},
			want: `{"email":null,"email_verified":false,"name":null,"avatar_url":null}`,
		},
		{
			name: "Microsoft Graph takes precedence", provider: "microsoft",
			mutate: func(f *fixture) {
				f.profile = `{"id":"graph-id","mail":"graph@example.test","displayName":"Graph Name","unknown":false}`
				f.claims["email"] = "token@example.test"
				f.claims["name"] = "Token Name"
				f.claims["email_verified"] = true
			},
			want: `{"email":"graph@example.test","email_verified":null,"name":"Graph Name","avatar_url":"data:image/jpeg;base64,AAECAw=="}`,
		},
		{
			name: "Microsoft token fallback", provider: "microsoft",
			mutate: func(f *fixture) {
				f.profile = `{"id":"graph-id","mail":null,"displayName":""}`
				f.claims["email"] = "token@example.test"
				f.claims["name"] = "Token Name"
				f.status["/photo"] = 404
			},
			want: `{"email":"token@example.test","email_verified":null,"name":"Token Name","avatar_url":null}`,
		},
		{
			name: "Microsoft missing fields", provider: "microsoft",
			mutate: func(f *fixture) {
				f.profile = `{}`
				f.claims["preferred_username"] = "username@example.test"
				f.status["/photo"] = 404
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":null}`,
		},
		{
			name: "Microsoft null and empty fields", provider: "microsoft",
			mutate: func(f *fixture) {
				f.profile = `{"mail":"","displayName":null}`
				f.claims["email"] = nil
				f.claims["name"] = ""
				f.status["/photo"] = 404
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":null}`,
		},
		{
			name: "Microsoft parsed photo media type", provider: "microsoft",
			mutate: func(f *fixture) {
				f.photoType = "image/jpeg; custom=parameter"
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":"data:image/jpeg;base64,AAECAw=="}`,
		},
		{
			name: "GitHub primary email takes precedence", provider: "github",
			mutate: func(f *fixture) {
				f.profile = `{"id":123,"email":"public@example.test","name":"Profile Name","avatar_url":"https://images.example/github.png"}`
				f.emails = `[{"email":"public@example.test","verified":false,"primary":false},{"email":"primary@example.test","verified":true,"primary":true}]`
			},
			want: `{"email":"primary@example.test","email_verified":true,"name":"Profile Name","avatar_url":"https://images.example/github.png"}`,
		},
		{
			name: "GitHub public email exact verification", provider: "github",
			mutate: func(f *fixture) {
				f.profile = `{"id":123,"email":"public@example.test"}`
				f.emails = `[{"email":"another@example.test","verified":true,"primary":false},{"email":"public@example.test","verified":false,"primary":false}]`
			},
			want: `{"email":"public@example.test","email_verified":false,"name":null,"avatar_url":null}`,
		},
		{
			name: "GitHub public email unknown verification", provider: "github",
			mutate: func(f *fixture) {
				f.profile = `{"id":123,"email":"public@example.test"}`
				f.emails = `[{"email":"another@example.test","verified":true}]`
			},
			want: `{"email":"public@example.test","email_verified":null,"name":null,"avatar_url":null}`,
		},
		{
			name: "GitHub no arbitrary non-primary email", provider: "github",
			mutate: func(f *fixture) {
				f.profile = `{"id":123}`
				f.emails = `[{"email":"another@example.test","verified":true,"primary":false}]`
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":null}`,
		},
		{
			name: "GitHub empty primary uses public email", provider: "github",
			mutate: func(f *fixture) {
				f.profile = `{"id":123,"email":"public@example.test","name":"","avatar_url":""}`
				f.emails = `[{"email":"","verified":true,"primary":true},{"email":"public@example.test","verified":false}]`
			},
			want: `{"email":"public@example.test","email_verified":false,"name":null,"avatar_url":null}`,
		},
		{
			name: "GitHub null fields", provider: "github",
			mutate: func(f *fixture) {
				f.profile = `{"id":123,"email":null,"name":null,"avatar_url":null}`
				f.emails = `[{"email":null,"verified":null,"primary":null}]`
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":null}`,
		},
		{
			name: "GitHub empty fields", provider: "github",
			mutate: func(f *fixture) {
				f.profile = `{"id":123,"email":"","name":"","avatar_url":""}`
				f.emails = `[{"email":"","verified":true,"primary":true}]`
			},
			want: `{"email":null,"email_verified":null,"name":null,"avatar_url":null}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.mutate(f)
			identity, err := f.provider(t, tc.provider).Exchange(context.Background(), exchangeRequest())
			if err != nil {
				t.Fatal(err)
			}
			serialized, err := json.Marshal(identity)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(serialized, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 7 || fields["provider"] == nil || fields["id"] == nil || fields["origin"] == nil {
				t.Fatalf("identity contract: %s", serialized)
			}
			var want map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			for field, value := range want {
				if string(fields[field]) != string(value) {
					t.Errorf("%s: got %s want %s", field, fields[field], value)
				}
			}
			wantID := map[string]string{"google": "google:user-123", "microsoft": "microsoft:" + microsoftTenant + ":object-123", "github": "github:123"}[tc.provider]
			if identity.Provider != tc.provider || identity.ID != wantID {
				t.Fatalf("stable identity changed: %+v", identity)
			}
			switch tc.provider {
			case "google":
				if string(identity.Origin["userinfo"]) != f.userinfo {
					t.Fatal("Google UserInfo origin changed")
				}
			case "microsoft":
				if string(identity.Origin["profile"]) != f.profile {
					t.Fatal("Microsoft Graph origin changed")
				}
				if f.status["/photo"] == 0 {
					var photo struct {
						ContentType string `json:"content_type"`
						DataBase64  string `json:"data_base64"`
					}
					if err := json.Unmarshal(identity.Origin["photo"], &photo); err != nil {
						t.Fatal(err)
					}
					if photo.ContentType != f.photoType || photo.DataBase64 != base64.StdEncoding.EncodeToString(f.photo) {
						t.Fatalf("photo origin changed: %+v", photo)
					}
				}
			case "github":
				if string(identity.Origin["profile"]) != f.profile || string(identity.Origin["emails"]) != f.emails {
					t.Fatal("GitHub origin changed")
				}
			}
		})
	}
}

func TestMalformedNormalizedFieldsPreserveCause(t *testing.T) {
	cases := []struct {
		provider, source, field string
	}{
		{"google", "userinfo", "email"},
		{"google", "userinfo", "email_verified"},
		{"google", "userinfo", "name"},
		{"google", "userinfo", "picture"},
		{"microsoft", "profile", "mail"},
		{"microsoft", "profile", "displayName"},
		{"microsoft", "claims", "email"},
		{"microsoft", "claims", "name"},
		{"github", "profile", "email"},
		{"github", "profile", "name"},
		{"github", "profile", "avatar_url"},
		{"github", "emails", "email"},
		{"github", "emails", "verified"},
		{"github", "emails", "primary"},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.source+"/"+tc.field, func(t *testing.T) {
			f := newFixture(t)
			f.profile = `{"id":123,"mail":"graph@example.test","displayName":"Graph Name"}`
			if tc.source == "claims" {
				f.claims[tc.field] = []string{"invalid"}
			} else {
				var document string
				switch tc.source {
				case "userinfo":
					document = f.userinfo
				case "profile":
					document = f.profile
				case "emails":
					document = `{"email":"secondary@example.test","primary":false,"verified":true}`
				}
				var fields map[string]any
				if err := json.Unmarshal([]byte(document), &fields); err != nil {
					t.Fatal(err)
				}
				fields[tc.field] = []string{"invalid"}
				mutated, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				switch tc.source {
				case "userinfo":
					f.userinfo = string(mutated)
				case "profile":
					f.profile = string(mutated)
				case "emails":
					f.emails = `[{"email":"primary@example.test","primary":true,"verified":true},` + string(mutated) + `]`
				}
			}
			_, err := f.provider(t, tc.provider).Exchange(context.Background(), exchangeRequest())
			var cause *json.UnmarshalTypeError
			if !errors.As(err, &cause) || err.Error() != "provider identity normalization failed" {
				t.Fatalf("malformed %s did not retain JSON cause: %v", tc.field, err)
			}
		})
	}
}

func TestGitHubLargeNumericIDIsExact(t *testing.T) {
	f := newFixture(t)
	f.profile = `{"id":9223372036854775807,"login":"renamed","email":"changed@example.test","name":"Changed Name"}`
	identity, err := f.provider(t, "github").Exchange(context.Background(), exchangeRequest())
	if err != nil || identity.ID != "github:9223372036854775807" {
		t.Fatalf("numeric identity changed: %+v, %v", identity, err)
	}
}
