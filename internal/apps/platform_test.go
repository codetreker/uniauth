package apps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/codetreker/uniauth/internal/providers"
)

func platformPointer[T any](value T) *T { return &value }

func platformIdentity(provider, id, email string, verified *bool) providers.Identity {
	return providers.Identity{Provider: provider, ID: id, Email: platformPointer(email), EmailVerified: verified, Name: platformPointer("Name " + id), AvatarURL: platformPointer("https://example.com/avatar/" + id), Origin: map[string]json.RawMessage{"profile": json.RawMessage(`{"private":"must-not-be-persisted"}`)}}
}

func platformOpen(t *testing.T, email string) *Store {
	t.Helper()
	store, err := Open(context.Background(), Config{Path: filepath.Join(t.TempDir(), "platform.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close platform store: %v", err)
		}
	})
	if email != "" {
		if err := store.InitializePlatform(context.Background(), email); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func platformAccount(t *testing.T, store *Store, identity providers.Identity) Account {
	t.Helper()
	account, err := store.ResolveAccount(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func platformInput(name string) CreateAppInput {
	return CreateAppInput{AppInput: AppInput{Name: name, Enabled: true, RedirectURIs: []string{"https://example.com/callback"}, Providers: []string{"google", "github"}}, InitialSecret: SecretInput{Name: "Backend", Description: "Product server"}}
}

func platformCreate(t *testing.T, store *Store, owner, name string) CreatedApp {
	t.Helper()
	created, err := store.Create(context.Background(), owner, platformInput(name))
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func platformExec(t *testing.T, store *Store, statement string, args ...any) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func platformCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPlatformStableIdentityAndProfile(t *testing.T) {
	ctx := context.Background()
	store := platformOpen(t, "admin@example.com")
	if got := platformCount(t, store, "accounts"); got != 0 {
		t.Fatalf("configuration seeded %d accounts", got)
	}
	identity := platformIdentity("google", "stable-id", "shared@example.com", platformPointer(true))
	first := platformAccount(t, store, identity)
	identity.Email = platformPointer("changed@example.com")
	identity.Name, identity.AvatarURL = nil, nil
	refreshed := platformAccount(t, store, identity)
	if first.ID == "" || first.ID != refreshed.ID || first.CreatedAt != refreshed.CreatedAt || refreshed.IsAdmin || *refreshed.Email != "changed@example.com" || refreshed.Name != nil || refreshed.AvatarURL != nil {
		t.Fatalf("stable identity did not preserve account and exact refreshed profile: %#v -> %#v", first, refreshed)
	}
	for _, other := range []providers.Identity{
		platformIdentity("github", "stable-id", "changed@example.com", platformPointer(true)),
		platformIdentity("google", "different-id", "changed@example.com", platformPointer(true)),
	} {
		if account := platformAccount(t, store, other); account.ID == first.ID {
			t.Fatalf("merged independent identity by email: %#v", other)
		}
	}
	identities, err := store.ListIdentities(ctx, first.ID)
	if err != nil || len(identities) != 1 || identities[0].ID != identity.ID || identities[0].Name != nil || *identities[0].Email != "changed@example.com" {
		t.Fatalf("identity profile = %#v, %v", identities, err)
	}
	account, err := store.Account(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(account, refreshed) {
		t.Fatalf("stored account = %#v, %v", account, err)
	}
	for _, value := range []any{account, identities} {
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte("origin")) || bytes.Contains(encoded, []byte("must-not-be-persisted")) {
			t.Fatalf("platform profile leaked origin: %s, %v", encoded, err)
		}
	}
	var origins int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND lower(sql) LIKE '%origin%'").Scan(&origins); err != nil || origins != 0 {
		t.Fatalf("platform tables persist raw origin: %d, %v", origins, err)
	}
	if _, err := store.Account(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account = %v", err)
	}
	if linked, err := store.ListIdentities(ctx, "missing"); err != nil || linked == nil || len(linked) != 0 {
		t.Fatalf("missing account identities = %#v, %v", linked, err)
	}
}

func TestPlatformBootstrapVerifiedExactEmail(t *testing.T) {
	cases := []struct {
		name, provider, email string
		verified              *bool
		admin                 bool
	}{
		{"google", "google", "ADMIN@Example.com", platformPointer(true), true},
		{"github", "github", "admin@example.com", platformPointer(true), true},
		{"microsoft-unknown", "microsoft", "admin@example.com", nil, false},
		{"microsoft-asserted", "microsoft", "admin@example.com", platformPointer(true), false},
		{"unverified", "google", "admin@example.com", platformPointer(false), false},
		{"unknown", "github", "admin@example.com", nil, false},
		{"different", "google", "other@example.com", platformPointer(true), false},
		{"whitespace", "google", " admin@example.com ", platformPointer(true), false},
		{"no-email", "google", "", platformPointer(true), false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := platformOpen(t, "Admin@example.com")
			identity := platformIdentity(test.provider, "claimant", test.email, test.verified)
			if test.name == "no-email" {
				identity.Email = nil
			}
			account := platformAccount(t, store, identity)
			if account.IsAdmin != test.admin {
				t.Fatalf("administrator = %v, want %v", account.IsAdmin, test.admin)
			}
			other := platformAccount(t, store, platformIdentity("google", "other", "admin@example.com", platformPointer(true)))
			if other.IsAdmin == test.admin {
				t.Fatalf("bootstrap should be consumed exactly once: first=%v second=%v", account.IsAdmin, other.IsAdmin)
			}
		})
	}
}

func TestPlatformBootstrapConfigurationPendingAndFrozen(t *testing.T) {
	ctx := context.Background()
	store := platformOpen(t, "before@example.com")
	if err := store.InitializePlatform(ctx, "after@example.com"); err != nil {
		t.Fatal(err)
	}
	before := platformAccount(t, store, platformIdentity("google", "before", "before@example.com", platformPointer(true)))
	admin := platformAccount(t, store, platformIdentity("github", "after", "after@example.com", platformPointer(true)))
	if before.IsAdmin || !admin.IsAdmin {
		t.Fatalf("pending configuration did not select actual claimant: %#v %#v", before, admin)
	}
	if err := store.InitializePlatform(ctx, "replacement@example.com"); err != nil {
		t.Fatal(err)
	}
	replacement := platformAccount(t, store, platformIdentity("google", "replacement", "replacement@example.com", platformPointer(true)))
	adminNow, err := store.Account(ctx, admin.ID)
	if err != nil || !adminNow.IsAdmin || replacement.IsAdmin {
		t.Fatalf("configuration reassigned claimed authority: %#v %#v %v", adminNow, replacement, err)
	}
	platformExec(t, store, "UPDATE accounts SET is_admin = 0 WHERE id = ?", admin.ID)
	if _, err := store.ListUsers(ctx, admin.ID, "", 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("administrator listing relied on stale authority: %v", err)
	}
}

func TestPlatformIdentityValidation(t *testing.T) {
	ctx := context.Background()
	store := platformOpen(t, "admin@example.com")
	owner := platformAccount(t, store, platformIdentity("google", "owner", "owner@example.com", nil))
	for _, identity := range []providers.Identity{
		{Provider: "unknown", ID: "id"}, {Provider: "google", ID: ""},
		{Provider: "github", ID: strings.Repeat("x", 513)}, {Provider: "microsoft", ID: string([]byte{0xff})},
	} {
		if _, err := store.ResolveAccount(ctx, identity); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ResolveAccount(%#v) = %v", identity, err)
		}
		if err := store.LinkIdentity(ctx, owner.ID, identity); !errors.Is(err, ErrInvalid) {
			t.Fatalf("LinkIdentity(%#v) = %v", identity, err)
		}
	}
	for _, email := range []string{"", "no-at-sign", "Admin <admin@example.com>", " admin@example.com", "admin@example.com ", "admin\t@example.com", string([]byte{0xff}) + "@example.com", strings.Repeat("x", 245) + "@example.com"} {
		if err := store.InitializePlatform(ctx, email); !errors.Is(err, ErrInvalid) {
			t.Fatalf("InitializePlatform(%q) = %v", email, err)
		}
	}
	if _, err := store.Create(ctx, "missing", platformInput("App")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown owner created an App: %v", err)
	}
	if err := store.LinkIdentity(ctx, "missing", platformIdentity("github", "new", "new@example.com", nil)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown account linked identity: %v", err)
	}
	if platformCount(t, store, "accounts") != 1 || platformCount(t, store, "account_identities") != 1 || platformCount(t, store, "apps") != 0 {
		t.Fatal("rejected operations created persistent records")
	}
}

func TestPlatformLinkingAndClaim(t *testing.T) {
	ctx := context.Background()
	store := platformOpen(t, "admin@example.com")
	owner := platformAccount(t, store, platformIdentity("microsoft", "ms-owner", "admin@example.com", nil))
	otherIdentity := platformIdentity("github", "already-owned", "same@example.com", platformPointer(true))
	other := platformAccount(t, store, otherIdentity)
	if err := store.LinkIdentity(ctx, owner.ID, otherIdentity); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("identity transferred from another account: %v", err)
	}
	if actual := platformAccount(t, store, otherIdentity); actual.ID != other.ID {
		t.Fatal("conflicting link changed identity owner")
	}
	claim := platformIdentity("google", "verified-admin", "ADMIN@example.com", platformPointer(true))
	if err := store.LinkIdentity(ctx, owner.ID, claim); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Account(ctx, owner.ID)
	if err != nil || !claimed.IsAdmin || !reflect.DeepEqual(claimed.Name, owner.Name) {
		t.Fatalf("explicit link should claim existing account without replacing profile: %#v, %v", claimed, err)
	}
	linked, err := store.ListIdentities(ctx, owner.ID)
	if err != nil || len(linked) != 2 {
		t.Fatalf("linked identities = %#v, %v", linked, err)
	}
	firstLinkedAt := linked[1].LinkedAt
	claim.Email, claim.Name, claim.EmailVerified = nil, nil, platformPointer(false)
	if err := store.LinkIdentity(ctx, owner.ID, claim); err != nil {
		t.Fatal(err)
	}
	linked, err = store.ListIdentities(ctx, owner.ID)
	if err != nil || len(linked) != 2 || linked[1].LinkedAt != firstLinkedAt || linked[1].Email != nil || linked[1].Name != nil || *linked[1].EmailVerified {
		t.Fatalf("repeat link did not refresh nullable fields in place: %#v, %v", linked, err)
	}
	if actual := platformAccount(t, store, claim); actual.ID != owner.ID || !actual.IsAdmin || actual.Email != nil || actual.Name != nil {
		t.Fatalf("linked sign-in did not resolve same account/profile: %#v", actual)
	}
}

func TestPlatformConcurrentIdentityAndAdministratorClaims(t *testing.T) {
	for _, mode := range []string{"same-identity", "administrator"} {
		t.Run(mode, func(t *testing.T) {
			store := platformOpen(t, "admin@example.com")
			const workers = 16
			results := make(chan Account, workers)
			errorsSeen := make(chan error, workers)
			start := make(chan struct{})
			var group sync.WaitGroup
			for i := range workers {
				group.Add(1)
				go func() {
					defer group.Done()
					<-start
					id := "one"
					if mode == "administrator" {
						id = fmt.Sprintf("claim-%d", i)
					}
					account, err := store.ResolveAccount(context.Background(), platformIdentity("google", id, "admin@example.com", platformPointer(true)))
					if err != nil {
						errorsSeen <- err
					} else {
						results <- account
					}
				}()
			}
			close(start)
			group.Wait()
			close(errorsSeen)
			close(results)
			for err := range errorsSeen {
				t.Fatal(err)
			}
			ids := map[string]bool{}
			adminIDs := map[string]bool{}
			for account := range results {
				ids[account.ID] = true
				if account.IsAdmin {
					adminIDs[account.ID] = true
				}
			}
			want := 1
			if mode == "administrator" {
				want = workers
			}
			if len(ids) != want || len(adminIDs) != 1 || platformCount(t, store, "accounts") != want || platformCount(t, store, "account_identities") != want {
				t.Fatalf("concurrent %s produced %d accounts and %d administrators", mode, len(ids), len(adminIDs))
			}
		})
	}
}

func TestPlatformConcurrentLinkHasOneOwner(t *testing.T) {
	store := platformOpen(t, "admin@example.com")
	owners := []Account{
		platformAccount(t, store, platformIdentity("google", "one", "one@example.com", nil)),
		platformAccount(t, store, platformIdentity("google", "two", "two@example.com", nil)),
	}
	identity := platformIdentity("github", "contested", "admin@example.com", platformPointer(true))
	results := make(chan error, len(owners))
	start := make(chan struct{})
	var group sync.WaitGroup
	for _, owner := range owners {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- store.LinkIdentity(context.Background(), owner.ID, identity)
		}()
	}
	close(start)
	group.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrIdentityConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	resolved := platformAccount(t, store, identity)
	if success != 1 || conflicts != 1 || !resolved.IsAdmin || !slices.Contains([]string{owners[0].ID, owners[1].ID}, resolved.ID) || platformCount(t, store, "accounts") != 2 {
		t.Fatalf("contested link = success %d conflict %d resolved %#v", success, conflicts, resolved)
	}
}

func TestPlatformOwnerAndSystemIsolation(t *testing.T) {
	ctx := context.Background()
	store := platformOpen(t, "admin@example.com")
	owner := platformAccount(t, store, platformIdentity("google", "owner", "owner@example.com", nil))
	admin := platformAccount(t, store, platformIdentity("github", "admin", "admin@example.com", platformPointer(true)))
	victim := platformCreate(t, store, owner.ID, "Private App")
	console, err := store.ProvisionConsole(ctx, "https://auth.example.com/console/auth/callback", []string{"google", "github"})
	if err != nil {
		t.Fatal(err)
	}
	input := platformInput("Unauthorized change")
	operations := []struct {
		name string
		run  func(string, string, string) error
	}{
		{"get", func(actor, app, secret string) error { _, err := store.GetOwned(ctx, actor, app); return err }},
		{"update", func(actor, app, secret string) error {
			_, err := store.Update(ctx, actor, app, input.AppInput)
			return err
		}},
		{"delete", func(actor, app, secret string) error { return store.Delete(ctx, actor, app) }},
		{"list-secrets", func(actor, app, secret string) error { _, err := store.ListSecrets(ctx, actor, app); return err }},
		{"create-secret", func(actor, app, secret string) error {
			_, err := store.CreateSecret(ctx, actor, app, input.InitialSecret)
			return err
		}},
		{"update-secret", func(actor, app, secret string) error {
			_, err := store.UpdateSecret(ctx, actor, app, secret, input.InitialSecret)
			return err
		}},
		{"revoke-secret", func(actor, app, secret string) error { return store.RevokeSecret(ctx, actor, app, secret) }},
		{"reset-secret", func(actor, app, secret string) error {
			_, err := store.ResetSecret(ctx, actor, app, secret)
			return err
		}},
	}
	for _, target := range []struct{ name, actor, app, secret string }{
		{"administrator-foreign", admin.ID, victim.App.ID, victim.SecretID},
		{"unknown-foreign", "missing", victim.App.ID, victim.SecretID},
		{"administrator-system", admin.ID, console.App.ID, console.SecretID},
		{"system-owner", "console", console.App.ID, console.SecretID},
	} {
		for _, operation := range operations {
			t.Run(target.name+"/"+operation.name, func(t *testing.T) {
				if err := operation.run(target.actor, target.app, target.secret); !errors.Is(err, ErrNotFound) {
					t.Fatalf("unauthorized management = %v, want not found", err)
				}
			})
		}
	}
	for _, app := range []CreatedApp{victim, console} {
		if actual, err := store.Get(ctx, app.App.ID); err != nil || !reflect.DeepEqual(actual, app.App) {
			t.Fatalf("unauthorized management changed App: %#v, %v", actual, err)
		}
		if _, err := store.Authenticate(ctx, app.App.ID, app.Secret); err != nil {
			t.Fatalf("unauthorized management changed credential: %v", err)
		}
	}
	for _, actor := range []string{admin.ID, "console", "missing"} {
		apps, err := store.List(ctx, actor)
		if err != nil || apps == nil || len(apps) != 0 {
			t.Fatalf("App list leaked foreign/system App for %q: %#v, %v", actor, apps, err)
		}
	}
	if owned, err := store.List(ctx, owner.ID); err != nil || len(owned) != 1 || owned[0].ID != victim.App.ID {
		t.Fatalf("owner cannot see own App: %#v, %v", owned, err)
	}
	if err := store.RevokeSecret(ctx, owner.ID, victim.App.ID, victim.SecretID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResetSecret(ctx, admin.ID, victim.App.ID, victim.SecretID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign reset leaked revoked state: %v", err)
	}
	if _, err := store.ResetSecret(ctx, owner.ID, victim.App.ID, victim.SecretID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("owner lost revoked state: %v", err)
	}
}

func TestPlatformUserSummaryPaginationAndPrivacy(t *testing.T) {
	ctx := context.Background()
	store := platformOpen(t, "admin@example.com")
	admin := platformAccount(t, store, platformIdentity("google", "admin", "admin@example.com", platformPointer(true)))
	users := []Account{admin}
	counts := map[string]int{admin.ID: 0}
	for i := range 4 {
		account := platformAccount(t, store, platformIdentity("github", fmt.Sprintf("user-%d", i), fmt.Sprintf("user%d@example.com", i), nil))
		users = append(users, account)
		counts[account.ID] = i
		for j := range i {
			platformCreate(t, store, account.ID, fmt.Sprintf("App %d", j))
		}
	}
	platformExec(t, store, "UPDATE accounts SET created_at = ?", admin.CreatedAt.UnixNano())
	if _, err := store.ProvisionConsole(ctx, "https://auth.example.com/console/auth/callback", []string{"google"}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cursor := ""
	for range 3 {
		page, err := store.ListUsers(ctx, admin.ID, cursor, 2)
		if err != nil || len(page.Users) == 0 || len(page.Users) > 2 {
			t.Fatalf("page = %#v, %v", page, err)
		}
		for _, user := range page.Users {
			if seen[user.ID] || user.AppCount != counts[user.ID] {
				t.Fatalf("duplicated/wrong user count: %#v", user)
			}
			seen[user.ID] = true
			encoded, err := json.Marshal(user)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(fields))
			for key := range fields {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			if !reflect.DeepEqual(keys, []string{"app_count", "created_at", "email", "id", "name"}) || bytes.Contains(encoded, []byte("Private App")) || bytes.Contains(encoded, []byte("must-not-be-persisted")) {
				t.Fatalf("administrator summary exposed additional details: %s", encoded)
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != len(users) {
		t.Fatalf("cursor skipped users sharing creation time: %d of %d", len(seen), len(users))
	}
	for _, actor := range []string{users[1].ID, "missing"} {
		if _, err := store.ListUsers(ctx, actor, "", 1); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin listed users: %v", err)
		}
	}
	for _, cursor := range []string{"%", base64.RawURLEncoding.EncodeToString([]byte("not JSON")), base64.RawURLEncoding.EncodeToString([]byte(`{"id":"","created_at":1}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"id":"id","created_at":0}`))} {
		if _, err := store.ListUsers(ctx, admin.ID, cursor, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("malformed cursor %q = %v", cursor, err)
		}
	}
	for _, limit := range []int{-1, 101} {
		if _, err := store.ListUsers(ctx, admin.ID, "", limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid limit %d = %v", limit, err)
		}
	}
}

func TestPlatformUserListDefaultLimit(t *testing.T) {
	ctx := context.Background()
	store := platformOpen(t, "admin@example.com")
	admin := platformAccount(t, store, platformIdentity("google", "admin", "admin@example.com", platformPointer(true)))
	for i := range 51 {
		platformAccount(t, store, platformIdentity("github", fmt.Sprintf("user-%d", i), "same@example.com", nil))
	}
	page, err := store.ListUsers(ctx, admin.ID, "", 0)
	if err != nil || len(page.Users) != 50 || page.NextCursor == nil {
		t.Fatalf("default page = %#v, %v", page, err)
	}
	next, err := store.ListUsers(ctx, admin.ID, *page.NextCursor, 0)
	if err != nil || len(next.Users) != 2 || next.NextCursor != nil {
		t.Fatalf("final default page = %#v, %v", next, err)
	}
	all, err := store.ListUsers(ctx, admin.ID, "", 100)
	if err != nil || len(all.Users) != 52 || all.NextCursor != nil {
		t.Fatalf("maximum page = %#v, %v", all, err)
	}
}

func TestPlatformRestartAndConsoleProvisioning(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "platform.sqlite")
	store, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if err := store.InitializePlatform(ctx, "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	identity := platformIdentity("google", "admin", "admin@example.com", platformPointer(true))
	admin := platformAccount(t, store, identity)
	linked := platformIdentity("github", "bound", "other@example.com", platformPointer(true))
	if err := store.LinkIdentity(ctx, admin.ID, linked); err != nil {
		t.Fatal(err)
	}
	product := platformCreate(t, store, admin.ID, "Product")
	before, err := store.ProvisionConsole(ctx, "https://old.example.com/console/auth/callback", []string{"google"})
	if err != nil {
		t.Fatal(err)
	}
	platformExec(t, store, "UPDATE apps SET enabled = 0, name = 'Changed' WHERE id = ?", before.App.ID)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitializePlatform(ctx, "new-admin@example.com"); err != nil {
		t.Fatal(err)
	}
	after, err := store.ProvisionConsole(ctx, "https://new.example.com/console/auth/callback", []string{"microsoft", "google", "github"})
	if err != nil {
		t.Fatal(err)
	}
	if before.App.ID != after.App.ID || before.App.CreatedAt != after.App.CreatedAt || before.Secret == after.Secret || before.SecretID == after.SecretID || !after.App.Enabled || after.App.Name != "UniAuth Console" || !reflect.DeepEqual(after.App.RedirectURIs, []string{"https://new.example.com/console/auth/callback"}) || !reflect.DeepEqual(after.App.Providers, []string{"microsoft", "google", "github"}) {
		t.Fatalf("restart provisioning = %#v -> %#v", before, after)
	}
	if _, err := store.Authenticate(ctx, after.App.ID, before.Secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("previous process credential remains usable: %v", err)
	}
	if _, err := store.Authenticate(ctx, after.App.ID, after.Secret); err != nil {
		t.Fatal(err)
	}
	var count int
	var digest []byte
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*), digest FROM app_secrets WHERE app_id = ?", after.App.ID).Scan(&count, &digest); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte(after.Secret))
	if count != 1 || !bytes.Equal(digest, expected[:]) || bytes.Contains(digest, []byte(after.Secret)) {
		t.Fatalf("Console credential persistence = %d rows, digest %x", count, digest)
	}
	if restored := platformAccount(t, store, linked); restored.ID != admin.ID || !restored.IsAdmin || restored.CreatedAt != admin.CreatedAt {
		t.Fatalf("restart lost account or link: %#v", restored)
	}
	if replacement := platformAccount(t, store, platformIdentity("github", "replacement", "new-admin@example.com", platformPointer(true))); replacement.IsAdmin {
		t.Fatal("restart config reassigned administrator")
	}
	if owned, err := store.GetOwned(ctx, admin.ID, product.App.ID); err != nil || !reflect.DeepEqual(owned, product.App) {
		t.Fatalf("restart lost product ownership: %#v, %v", owned, err)
	}
	if _, err := store.Authenticate(ctx, product.App.ID, product.Secret); err != nil {
		t.Fatalf("Console rotation changed product credential: %v", err)
	}
}

func TestPlatformAccountAndLinkRollback(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"new-identity", "administrator-grant", "profile-refresh", "link-claim", "repeat-link-refresh"} {
		t.Run(mode, func(t *testing.T) {
			store := platformOpen(t, "admin@example.com")
			identity := platformIdentity("google", "new", "admin@example.com", platformPointer(true))
			var owner Account
			var original providers.Identity
			if mode == "profile-refresh" || mode == "link-claim" || mode == "repeat-link-refresh" {
				original = platformIdentity("microsoft", "original", "original@example.com", nil)
				owner = platformAccount(t, store, original)
			}
			var statement string
			switch mode {
			case "new-identity":
				statement = "CREATE TRIGGER refuse_write BEFORE INSERT ON account_identities BEGIN SELECT RAISE(ABORT, 'blocked identity'); END"
			case "administrator-grant", "link-claim":
				statement = "CREATE TRIGGER refuse_write BEFORE UPDATE OF is_admin ON accounts BEGIN SELECT RAISE(ABORT, 'blocked grant'); END"
			case "profile-refresh", "repeat-link-refresh":
				statement = "CREATE TRIGGER refuse_write BEFORE UPDATE ON account_identities BEGIN SELECT RAISE(ABORT, 'blocked refresh'); END"
			}
			platformExec(t, store, statement)
			var err error
			switch mode {
			case "link-claim":
				err = store.LinkIdentity(ctx, owner.ID, identity)
			case "profile-refresh":
				modified := original
				modified.Email, modified.Name = platformPointer("changed@example.com"), nil
				_, err = store.ResolveAccount(ctx, modified)
			case "repeat-link-refresh":
				modified := original
				modified.Email, modified.Name = platformPointer("changed@example.com"), nil
				err = store.LinkIdentity(ctx, owner.ID, modified)
			default:
				_, err = store.ResolveAccount(ctx, identity)
			}
			var cause interface{ Code() int }
			if err == nil || !strings.Contains(err.Error(), "blocked") || !errors.As(err, &cause) {
				t.Fatalf("database failure was not propagated with original cause: %v", err)
			}
			want := 0
			if owner.ID != "" {
				want = 1
				stored, readErr := store.Account(ctx, owner.ID)
				if readErr != nil || !reflect.DeepEqual(stored, owner) {
					t.Fatalf("failed operation changed existing account: %#v, %v", stored, readErr)
				}
				linked, readErr := store.ListIdentities(ctx, owner.ID)
				if readErr != nil || len(linked) != 1 || *linked[0].Email != *original.Email || *linked[0].Name != *original.Name {
					t.Fatalf("failed operation changed existing identities: %#v, %v", linked, readErr)
				}
			}
			if platformCount(t, store, "accounts") != want || platformCount(t, store, "account_identities") != want {
				t.Fatal("failed operation left partial account/link rows")
			}
			var claimed sql.NullString
			if err := store.db.QueryRowContext(ctx, "SELECT claimed_account_id FROM platform_state WHERE id = 1").Scan(&claimed); err != nil || claimed.Valid {
				t.Fatalf("failed transaction consumed bootstrap: %#v, %v", claimed, err)
			}
			platformExec(t, store, "DROP TRIGGER refuse_write")
			if mode == "link-claim" {
				if err := store.LinkIdentity(ctx, owner.ID, identity); err != nil {
					t.Fatalf("retry after rollback could not claim: %v", err)
				}
			} else if mode == "new-identity" || mode == "administrator-grant" {
				if account := platformAccount(t, store, identity); !account.IsAdmin {
					t.Fatal("rollback did not retain administrator claim opportunity")
				}
			}
		})
	}
}

func TestPlatformConsoleProvisionRollback(t *testing.T) {
	ctx := context.Background()
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-%t", existing), func(t *testing.T) {
			store := platformOpen(t, "admin@example.com")
			var previous CreatedApp
			if existing {
				var err error
				previous, err = store.ProvisionConsole(ctx, "https://before.example.com/callback", []string{"google"})
				if err != nil {
					t.Fatal(err)
				}
			}
			platformExec(t, store, "CREATE TRIGGER refuse_secret BEFORE INSERT ON app_secrets BEGIN SELECT RAISE(ABORT, 'blocked credential'); END")
			if _, err := store.ProvisionConsole(ctx, "https://after.example.com/callback", []string{"github"}); err == nil || !strings.Contains(err.Error(), "blocked credential") {
				t.Fatalf("failed provisioning = %v", err)
			}
			if existing {
				actual, err := store.Get(ctx, previous.App.ID)
				if err != nil || !reflect.DeepEqual(actual, previous.App) {
					t.Fatalf("failed provisioning changed registered callback/providers: %#v, %v", actual, err)
				}
				if _, err := store.Authenticate(ctx, previous.App.ID, previous.Secret); err != nil {
					t.Fatalf("failed provisioning destroyed working credential: %v", err)
				}
				if platformCount(t, store, "app_secrets") != 1 {
					t.Fatal("failed rotation changed credential count")
				}
			} else {
				var recorded sql.NullString
				if err := store.db.QueryRowContext(ctx, "SELECT console_app_id FROM platform_state WHERE id = 1").Scan(&recorded); err != nil || recorded.Valid || platformCount(t, store, "apps") != 0 || platformCount(t, store, "app_secrets") != 0 {
					t.Fatalf("failed initial provisioning left partial registration: %#v, %v", recorded, err)
				}
			}
			platformExec(t, store, "DROP TRIGGER refuse_secret")
			if _, err := store.ProvisionConsole(ctx, "https://after.example.com/callback", []string{"github"}); err != nil {
				t.Fatalf("provisioning after rollback failed: %v", err)
			}
		})
	}
}

func TestPlatformStorageContextAndLifecycleErrors(t *testing.T) {
	store := platformOpen(t, "admin@example.com")
	admin := platformAccount(t, store, platformIdentity("google", "admin", "admin@example.com", platformPointer(true)))
	identity := platformIdentity("github", "new", "new@example.com", nil)
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("live storage health = %v", err)
	}
	operations := []struct {
		name string
		run  func(context.Context) error
	}{
		{"ping", store.Ping},
		{"initialize", func(ctx context.Context) error { return store.InitializePlatform(ctx, "admin@example.com") }},
		{"provision", func(ctx context.Context) error {
			_, err := store.ProvisionConsole(ctx, "https://auth.example.com/callback", []string{"google"})
			return err
		}},
		{"resolve", func(ctx context.Context) error { _, err := store.ResolveAccount(ctx, identity); return err }},
		{"link", func(ctx context.Context) error { return store.LinkIdentity(ctx, admin.ID, identity) }},
		{"account", func(ctx context.Context) error { _, err := store.Account(ctx, admin.ID); return err }},
		{"identities", func(ctx context.Context) error { _, err := store.ListIdentities(ctx, admin.ID); return err }},
		{"users", func(ctx context.Context) error { _, err := store.ListUsers(ctx, admin.ID, "", 1); return err }},
		{"owned-app", func(ctx context.Context) error { _, err := store.GetOwned(ctx, admin.ID, "missing"); return err }},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range operations {
		t.Run("canceled/"+operation.name, func(t *testing.T) {
			if err := operation.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation lost original cause: %v", err)
			}
		})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		t.Run("closed/"+operation.name, func(t *testing.T) {
			if err := operation.run(context.Background()); err == nil || !strings.Contains(err.Error(), "database is closed") || errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
				t.Fatalf("closed storage failure concealed as caller error: %v", err)
			}
		})
	}
}

func TestPlatformCorruptProfilesSurfaceReadErrors(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"account", "identity", "users"} {
		t.Run(mode, func(t *testing.T) {
			store := platformOpen(t, "admin@example.com")
			admin := platformAccount(t, store, platformIdentity("google", "admin", "admin@example.com", platformPointer(true)))
			var err error
			switch mode {
			case "account":
				platformExec(t, store, "UPDATE accounts SET created_at = 'corrupt' WHERE id = ?", admin.ID)
				_, err = store.Account(ctx, admin.ID)
			case "identity":
				platformExec(t, store, "UPDATE account_identities SET linked_at = 'corrupt' WHERE account_id = ?", admin.ID)
				_, err = store.ListIdentities(ctx, admin.ID)
			case "users":
				platformExec(t, store, "UPDATE accounts SET created_at = 'corrupt' WHERE id = ?", admin.ID)
				_, err = store.ListUsers(ctx, admin.ID, "", 1)
			}
			if err == nil || !strings.Contains(err.Error(), "corrupt") || errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
				t.Fatalf("corrupt profile was concealed or fabricated: %v", err)
			}
		})
	}
}

func TestPlatformSchemaFailuresPropagate(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"resolve-lookup", "resolve-create", "resolve-refresh", "link-account", "link-lookup", "list-authority", "claim"} {
		t.Run(mode, func(t *testing.T) {
			store := platformOpen(t, "admin@example.com")
			identity := platformIdentity("google", "owner", "owner@example.com", nil)
			owner := platformAccount(t, store, identity)
			var err error
			switch mode {
			case "resolve-lookup":
				platformExec(t, store, "DROP TABLE account_identities")
				_, err = store.ResolveAccount(ctx, identity)
			case "resolve-create":
				platformExec(t, store, "DROP TABLE accounts")
				_, err = store.ResolveAccount(ctx, platformIdentity("github", "new", "new@example.com", nil))
			case "resolve-refresh":
				platformExec(t, store, "DROP TABLE accounts")
				_, err = store.ResolveAccount(ctx, identity)
			case "link-account":
				platformExec(t, store, "DROP TABLE accounts")
				err = store.LinkIdentity(ctx, owner.ID, platformIdentity("github", "new", "new@example.com", nil))
			case "link-lookup":
				platformExec(t, store, "DROP TABLE account_identities")
				err = store.LinkIdentity(ctx, owner.ID, platformIdentity("github", "new", "new@example.com", nil))
			case "list-authority":
				platformExec(t, store, "DROP TABLE accounts")
				_, err = store.ListUsers(ctx, owner.ID, "", 1)
			case "claim":
				platformExec(t, store, "DROP TABLE platform_state")
				_, err = store.ResolveAccount(ctx, platformIdentity("google", "claimant", "admin@example.com", platformPointer(true)))
			}
			var cause interface{ Code() int }
			if err == nil || !errors.As(err, &cause) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
				t.Fatalf("schema error concealed/lost cause: %v", err)
			}
		})
	}
}

func TestPlatformProvisionConfigurationAndDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("invalid-configuration", func(t *testing.T) {
		store := platformOpen(t, "admin@example.com")
		if _, err := store.ProvisionConsole(ctx, "http://auth.example.com/callback", []string{"google"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("insecure Console callback = %v", err)
		}
		if _, err := store.ProvisionConsole(ctx, "https://auth.example.com/callback", []string{"unknown"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsupported Console provider = %v", err)
		}
	})
	t.Run("requires-initialization", func(t *testing.T) {
		store := platformOpen(t, "")
		if _, err := store.ProvisionConsole(ctx, "https://auth.example.com/callback", []string{"google"}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("uninitialized platform provision = %v", err)
		}
	})
	for _, mode := range []string{"create-app", "record-app", "reconcile", "missing-system-app", "replace-secret", "read-app"} {
		t.Run(mode, func(t *testing.T) {
			store := platformOpen(t, "admin@example.com")
			if mode == "reconcile" || mode == "missing-system-app" || mode == "replace-secret" {
				created, err := store.ProvisionConsole(ctx, "https://before.example.com/callback", []string{"google"})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing-system-app" {
					platformExec(t, store, "DELETE FROM apps WHERE id = ?", created.App.ID)
				}
			}
			switch mode {
			case "create-app":
				platformExec(t, store, "CREATE TRIGGER refuse_write BEFORE INSERT ON apps BEGIN SELECT RAISE(ABORT, 'blocked registration'); END")
			case "record-app":
				platformExec(t, store, "CREATE TRIGGER refuse_write BEFORE UPDATE OF console_app_id ON platform_state BEGIN SELECT RAISE(ABORT, 'blocked state'); END")
			case "reconcile":
				platformExec(t, store, "CREATE TRIGGER refuse_write BEFORE UPDATE ON apps BEGIN SELECT RAISE(ABORT, 'blocked reconciliation'); END")
			case "replace-secret":
				platformExec(t, store, "CREATE TRIGGER refuse_write BEFORE DELETE ON app_secrets BEGIN SELECT RAISE(ABORT, 'blocked replacement'); END")
			case "read-app":
				platformExec(t, store, "CREATE TRIGGER corrupt_write AFTER INSERT ON apps BEGIN UPDATE apps SET redirect_uris = 'corrupt' WHERE id = NEW.id; END")
			}
			_, err := store.ProvisionConsole(ctx, "https://after.example.com/callback", []string{"github"})
			if mode == "missing-system-app" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("broken system registration = %v", err)
				}
			} else if err == nil || (!strings.Contains(err.Error(), "blocked") && !strings.Contains(err.Error(), "decode app redirect")) {
				t.Fatalf("storage failure concealed during provisioning: %v", err)
			}
		})
	}
}
