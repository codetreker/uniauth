package apps_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/providers"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validInput() apps.AppInput {
	return apps.AppInput{
		Name: "Product", Enabled: true,
		RedirectURIs: []string{"https://product.example/auth/callback?environment=test&return=%2Fhome"},
		Providers:    []string{"microsoft", "google", "github"},
	}
}

func validSecretInput() apps.SecretInput {
	return apps.SecretInput{Name: "Production backend", Description: "  Deployment note\nowner: platform\n  "}
}

func createInput(input apps.AppInput) apps.CreateAppInput {
	return apps.CreateAppInput{AppInput: input, InitialSecret: validSecretInput()}
}

func validCreateInput() apps.CreateAppInput {
	return createInput(validInput())
}

func openStore(t *testing.T, loopback bool) *apps.Store {
	t.Helper()
	store, err := apps.Open(t.Context(), apps.Config{Path: filepath.Join(t.TempDir(), "apps.sqlite"), AllowLoopbackHTTP: loopback})
	require.NoError(t, err)
	testOwnerID(t, store)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

var testStoreOwners sync.Map

func testOwnerID(t *testing.T, store *apps.Store) string {
	t.Helper()
	if id, ok := testStoreOwners.Load(store); ok {
		return id.(string)
	}
	require.NoError(t, store.InitializePlatform(t.Context(), "admin@example.test"))
	account, err := store.ResolveAccount(t.Context(), providers.Identity{Provider: "google", ID: "store-fixture-owner"})
	require.NoError(t, err)
	id, loaded := testStoreOwners.LoadOrStore(store, account.ID)
	if !loaded {
		t.Cleanup(func() { testStoreOwners.Delete(store) })
	}
	return id.(string)
}

func TestRegistryPersistenceAndCredentialLifecycle(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "apps.sqlite")
	store, err := apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	secretBytes, err := base64.RawURLEncoding.DecodeString(created.Secret)
	require.NoError(t, err)
	require.Len(t, secretBytes, 32)
	require.NotEmpty(t, created.SecretID)
	assert.WithinDuration(t, time.Now(), created.App.CreatedAt, time.Second)
	assert.Equal(t, created.App.CreatedAt, created.App.UpdatedAt)
	require.NoError(t, store.Close())
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(contents), created.Secret)

	store, err = apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	stored, err := store.Get(ctx, created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, created.App, stored)
	listed, err := store.List(ctx, testOwnerID(t, store))
	require.NoError(t, err)
	assert.Equal(t, []apps.App{stored}, listed)
	authenticated, err := store.Authenticate(ctx, stored.ID, created.Secret)
	require.NoError(t, err)
	assert.Equal(t, stored, authenticated)

	updatedInput := validInput()
	updatedInput.Name = "Renamed Product"
	updatedInput.Enabled = false
	updatedInput.Providers = []string{"google"}
	updated, err := store.Update(ctx, testOwnerID(t, store), stored.ID, updatedInput)
	require.NoError(t, err)
	assert.Equal(t, stored.CreatedAt, updated.CreatedAt)
	assert.GreaterOrEqual(t, updated.UpdatedAt.UnixNano(), stored.UpdatedAt.UnixNano())
	_, err = store.Authenticate(ctx, stored.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	updatedInput.Enabled = true
	_, err = store.Update(ctx, testOwnerID(t, store), stored.ID, updatedInput)
	require.NoError(t, err)

	issued, err := store.CreateSecret(ctx, testOwnerID(t, store), stored.ID, validSecretInput())
	require.NoError(t, err)
	assert.NotEqual(t, created.Secret, issued.Secret)
	for _, secret := range []string{created.Secret, issued.Secret} {
		_, err := store.Authenticate(ctx, stored.ID, secret)
		require.NoError(t, err)
	}
	metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), stored.ID)
	require.NoError(t, err)
	require.Len(t, metadata, 2)
	for _, secret := range metadata {
		assert.Equal(t, validSecretInput().Name, secret.Name)
		assert.Equal(t, validSecretInput().Description, secret.Description)
	}
	assert.Equal(t, validSecretInput().Name, issued.Name)
	assert.Equal(t, validSecretInput().Description, issued.Description)
	serialized, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), created.Secret)
	assert.NotContains(t, string(serialized), issued.Secret)
	assert.NotContains(t, string(serialized), "digest")
	assert.NotContains(t, string(serialized), "revoked_at")

	require.NoError(t, store.RevokeSecret(ctx, testOwnerID(t, store), stored.ID, created.SecretID))
	metadata, err = store.ListSecrets(ctx, testOwnerID(t, store), stored.ID)
	require.NoError(t, err)
	require.NotNil(t, metadata[0].RevokedAt)
	revoked := *metadata[0].RevokedAt
	require.NoError(t, store.RevokeSecret(ctx, testOwnerID(t, store), stored.ID, created.SecretID))
	metadata, err = store.ListSecrets(ctx, testOwnerID(t, store), stored.ID)
	require.NoError(t, err)
	assert.Equal(t, revoked, *metadata[0].RevokedAt)
	_, err = store.Authenticate(ctx, stored.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	_, err = store.Authenticate(ctx, stored.ID, issued.Secret)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, testOwnerID(t, store), stored.ID))
	_, err = store.Get(ctx, stored.ID)
	require.ErrorIs(t, err, apps.ErrNotFound)
	_, err = store.ListSecrets(ctx, testOwnerID(t, store), stored.ID)
	require.ErrorIs(t, err, apps.ErrNotFound)
	_, err = store.Authenticate(ctx, stored.ID, issued.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	listed, err = store.List(ctx, testOwnerID(t, store))
	require.NoError(t, err)
	assert.Empty(t, listed)
	assert.NotNil(t, listed)
}

func TestAppIsolationAndMissingRecords(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	first, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	second, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	_, err = store.Authenticate(ctx, first.App.ID, second.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	_, err = store.Authenticate(ctx, second.App.ID, first.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	_, err = store.Authenticate(ctx, first.App.ID, "")
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	require.ErrorIs(t, store.RevokeSecret(ctx, testOwnerID(t, store), second.App.ID, first.SecretID), apps.ErrNotFound)
	_, err = store.Authenticate(ctx, first.App.ID, first.Secret)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, testOwnerID(t, store), first.App.ID))
	_, err = store.Authenticate(ctx, second.App.ID, second.Secret)
	require.NoError(t, err)
	for _, operation := range []func() error{
		func() error { _, err := store.Get(ctx, "missing"); return err },
		func() error { _, err := store.Update(ctx, testOwnerID(t, store), "missing", validInput()); return err },
		func() error { return store.Delete(ctx, testOwnerID(t, store), "missing") },
		func() error {
			_, err := store.CreateSecret(ctx, testOwnerID(t, store), "missing", validSecretInput())
			return err
		},
		func() error { _, err := store.ListSecrets(ctx, testOwnerID(t, store), "missing"); return err },
		func() error {
			_, err := store.UpdateSecret(ctx, testOwnerID(t, store), "missing", second.SecretID, validSecretInput())
			return err
		},
		func() error {
			_, err := store.UpdateSecret(ctx, testOwnerID(t, store), second.App.ID, "missing", validSecretInput())
			return err
		},
		func() error { return store.RevokeSecret(ctx, testOwnerID(t, store), "missing", second.SecretID) },
	} {
		require.ErrorIs(t, operation(), apps.ErrNotFound)
	}
}

func TestInputValidationAndExactCallbackPreservation(t *testing.T) {
	store := openStore(t, false)
	invalid := map[string]func(*apps.AppInput){
		"empty name":          func(i *apps.AppInput) { i.Name = "" },
		"whitespace name":     func(i *apps.AppInput) { i.Name = " Product " },
		"long name":           func(i *apps.AppInput) { i.Name = strings.Repeat("a", 129) },
		"invalid UTF8 name":   func(i *apps.AppInput) { i.Name = string([]byte{0xff}) },
		"no redirects":        func(i *apps.AppInput) { i.RedirectURIs = nil },
		"duplicate redirects": func(i *apps.AppInput) { i.RedirectURIs = append(i.RedirectURIs, i.RedirectURIs[0]) },
		"too many redirects":  func(i *apps.AppInput) { i.RedirectURIs = make([]string, 17) },
		"no providers":        func(i *apps.AppInput) { i.Providers = nil },
		"duplicate providers": func(i *apps.AppInput) { i.Providers = []string{"google", "google"} },
		"unknown provider":    func(i *apps.AppInput) { i.Providers = []string{"enterprise"} },
		"wrong provider case": func(i *apps.AppInput) { i.Providers = []string{"Google"} },
		"too many providers":  func(i *apps.AppInput) { i.Providers = []string{"google", "github", "microsoft", "google"} },
	}
	for name, modify := range invalid {
		t.Run(name, func(t *testing.T) {
			input := validInput()
			modify(&input)
			_, err := store.Create(t.Context(), testOwnerID(t, store), createInput(input))
			require.ErrorIs(t, err, apps.ErrInvalid)
			_, err = store.Update(t.Context(), testOwnerID(t, store), "missing", input)
			require.ErrorIs(t, err, apps.ErrInvalid)
		})
	}
	for _, uri := range []string{
		"", "https://" + strings.Repeat("a", 2048), "/relative", "http://product.example/callback",
		"http://localhost:8080/callback", "ftp://product.example/callback", "https://name:password@product.example/callback",
		"https://product.example/callback#fragment", "https://product.example/callback#", "https://:443/callback",
		"https://product.example/callback?code=existing", "https://product.example/callback?state=existing",
		"https://product.example/callback?error=existing", "https://product.example/callback?%63ode=existing",
		"https://product.example/callback?x=%zz", "https://product.example/callback?x=a;b",
		"https://product.example/%zz", "https:product.example/callback",
		"https://product.example/" + string([]byte{0xff}),
	} {
		t.Run(uri, func(t *testing.T) {
			input := validInput()
			input.RedirectURIs = []string{uri}
			_, err := store.Create(t.Context(), testOwnerID(t, store), createInput(input))
			require.ErrorIs(t, err, apps.ErrInvalid)
		})
	}
	input := validInput()
	created, err := store.Create(t.Context(), testOwnerID(t, store), createInput(input))
	require.NoError(t, err)
	assert.Equal(t, input.RedirectURIs, created.App.RedirectURIs)
	invalidUpdate := validInput()
	invalidUpdate.RedirectURIs = []string{"https://product.example/callback?code=reserved"}
	_, err = store.Update(t.Context(), testOwnerID(t, store), created.App.ID, invalidUpdate)
	require.ErrorIs(t, err, apps.ErrInvalid)
	input.RedirectURIs[0], input.Providers[0] = "changed", "changed"
	stored, err := store.Get(t.Context(), created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, created.App, stored)

	loopbackStore := openStore(t, true)
	for _, uri := range []string{"http://localhost:8080/callback", "http://127.0.0.1:8080/callback", "http://127.2.3.4/callback", "http://[::1]:8080/callback", "https://product.example/callback"} {
		input := validInput()
		input.RedirectURIs = []string{uri}
		_, err := loopbackStore.Create(t.Context(), testOwnerID(t, loopbackStore), createInput(input))
		require.NoError(t, err)
	}
	for _, uri := range []string{"http://localhost.example/callback", "http://192.168.1.1/callback", "http://product.example/callback"} {
		input := validInput()
		input.RedirectURIs = []string{uri}
		_, err := loopbackStore.Create(t.Context(), testOwnerID(t, loopbackStore), createInput(input))
		require.ErrorIs(t, err, apps.ErrInvalid)
	}
}

func TestConcurrentRotationAndRevocation(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	const workers = 16
	var wg sync.WaitGroup
	issued := make(chan apps.IssuedSecret, workers)
	failures := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			secret, err := store.CreateSecret(ctx, testOwnerID(t, store), created.App.ID, validSecretInput())
			if err != nil {
				failures <- err
				return
			}
			issued <- secret
		})
	}
	wg.Wait()
	close(issued)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	secrets := make([]apps.IssuedSecret, 0, workers)
	seen := make(map[string]bool)
	for secret := range issued {
		assert.False(t, seen[secret.Secret])
		seen[secret.Secret] = true
		secrets = append(secrets, secret)
		_, err := store.Authenticate(ctx, created.App.ID, secret.Secret)
		require.NoError(t, err)
	}
	require.Len(t, secrets, workers)
	for _, secret := range secrets {
		wg.Go(func() {
			assert.NoError(t, store.RevokeSecret(ctx, testOwnerID(t, store), created.App.ID, secret.ID))
		})
	}
	wg.Wait()
	for _, secret := range secrets {
		_, err := store.Authenticate(ctx, created.App.ID, secret.Secret)
		require.ErrorIs(t, err, apps.ErrUnauthorized)
	}
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.NoError(t, err)
	metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	require.Len(t, metadata, workers+1)
}

func TestResetSecretReplacesOnlySelectedCredential(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	otherInput := apps.SecretInput{Name: "Backup backend", Description: "Independent credential"}
	other, err := store.CreateSecret(ctx, testOwnerID(t, store), created.App.ID, otherInput)
	require.NoError(t, err)
	secondApp, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	updatedInput := apps.SecretInput{Name: "Renamed primary", Description: "  Updated rotation memo\n第二行  "}
	updated, err := store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID, updatedInput)
	require.NoError(t, err)
	require.Equal(t, updatedInput.Name, updated.Name)
	require.Equal(t, updatedInput.Description, updated.Description)

	for _, target := range []struct{ appID, secretID string }{
		{secondApp.App.ID, created.SecretID},
		{created.App.ID, "missing"},
		{"missing", created.SecretID},
	} {
		_, err := store.ResetSecret(ctx, testOwnerID(t, store), target.appID, target.secretID)
		require.ErrorIs(t, err, apps.ErrNotFound)
	}
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.NoError(t, err)

	replacement, err := store.ResetSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID)
	require.NoError(t, err)
	require.NotEqual(t, created.SecretID, replacement.ID)
	require.NotEqual(t, created.Secret, replacement.Secret)
	require.Equal(t, updatedInput.Name, replacement.Name)
	require.Equal(t, updatedInput.Description, replacement.Description)
	decoded, err := base64.RawURLEncoding.DecodeString(replacement.Secret)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	for _, credential := range []string{replacement.Secret, other.Secret} {
		_, err = store.Authenticate(ctx, created.App.ID, credential)
		require.NoError(t, err)
	}
	_, err = store.Authenticate(ctx, secondApp.App.ID, secondApp.Secret)
	require.NoError(t, err)

	metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	require.Len(t, metadata, 3)
	states := make(map[string]apps.Secret, len(metadata))
	for _, secret := range metadata {
		states[secret.ID] = secret
	}
	require.NotNil(t, states[created.SecretID].RevokedAt)
	require.Equal(t, updatedInput.Name, states[created.SecretID].Name)
	require.Equal(t, updatedInput.Description, states[created.SecretID].Description)
	require.Nil(t, states[other.ID].RevokedAt)
	require.Equal(t, otherInput.Name, states[other.ID].Name)
	require.Equal(t, otherInput.Description, states[other.ID].Description)
	require.Nil(t, states[replacement.ID].RevokedAt)
	require.Equal(t, updatedInput.Name, states[replacement.ID].Name)
	require.Equal(t, updatedInput.Description, states[replacement.ID].Description)
	_, err = store.ResetSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID)
	require.ErrorIs(t, err, apps.ErrRevoked)
	require.NoError(t, store.RevokeSecret(ctx, testOwnerID(t, store), created.App.ID, other.ID))
	_, err = store.ResetSecret(ctx, testOwnerID(t, store), created.App.ID, other.ID)
	require.ErrorIs(t, err, apps.ErrRevoked)
	metadata, err = store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	require.Len(t, metadata, 3)
}

func TestConcurrentResetSecretIssuesOneReplacement(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	start := make(chan struct{})
	type result struct {
		issued apps.IssuedSecret
		err    error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			issued, err := store.ResetSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID)
			results <- result{issued, err}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	var replacement apps.IssuedSecret
	var successes, conflicts int
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			replacement = result.issued
		case errors.Is(result.err, apps.ErrRevoked):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent reset result: %v", result.err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	require.Equal(t, validSecretInput().Name, replacement.Name)
	require.Equal(t, validSecretInput().Description, replacement.Description)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	_, err = store.Authenticate(ctx, created.App.ID, replacement.Secret)
	require.NoError(t, err)
	metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	require.Len(t, metadata, 2)
}

func TestDeleteOrdersConcurrentSecretCreation(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	const workers = 16
	start := make(chan struct{})
	issued := make(chan apps.IssuedSecret, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			secret, err := store.CreateSecret(ctx, testOwnerID(t, store), created.App.ID, validSecretInput())
			if err != nil {
				assert.ErrorIs(t, err, apps.ErrNotFound)
				return
			}
			issued <- secret
		})
	}
	wg.Go(func() {
		<-start
		assert.NoError(t, store.Delete(ctx, testOwnerID(t, store), created.App.ID))
	})
	close(start)
	wg.Wait()
	close(issued)
	for secret := range issued {
		_, err := store.Authenticate(ctx, created.App.ID, secret.Secret)
		require.ErrorIs(t, err, apps.ErrUnauthorized)
		require.ErrorIs(t, store.RevokeSecret(ctx, testOwnerID(t, store), created.App.ID, secret.ID), apps.ErrNotFound)
	}
	_, err = store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.ErrorIs(t, err, apps.ErrNotFound)
}

func TestDatabaseErrorsRetainCauses(t *testing.T) {
	_, err := apps.Open(t.Context(), apps.Config{})
	require.ErrorIs(t, err, apps.ErrInvalid)
	_, err = apps.Open(t.Context(), apps.Config{Path: filepath.Join(t.TempDir(), "missing", "apps.sqlite")})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = apps.Open(ctx, apps.Config{Path: filepath.Join(t.TempDir(), "apps.sqlite")})
	require.ErrorIs(t, err, context.Canceled)
	store := openStore(t, false)
	for _, operation := range []func() error{
		func() error { _, err := store.Create(ctx, testOwnerID(t, store), validCreateInput()); return err },
		func() error { _, err := store.Get(ctx, "missing"); return err },
		func() error { _, err := store.List(ctx, testOwnerID(t, store)); return err },
		func() error { _, err := store.Update(ctx, testOwnerID(t, store), "missing", validInput()); return err },
		func() error { return store.Delete(ctx, testOwnerID(t, store), "missing") },
		func() error {
			_, err := store.CreateSecret(ctx, testOwnerID(t, store), "missing", validSecretInput())
			return err
		},
		func() error { _, err := store.ListSecrets(ctx, testOwnerID(t, store), "missing"); return err },
		func() error {
			_, err := store.UpdateSecret(ctx, testOwnerID(t, store), "missing", "missing", validSecretInput())
			return err
		},
		func() error { return store.RevokeSecret(ctx, testOwnerID(t, store), "missing", "missing") },
		func() error {
			_, err := store.ResetSecret(ctx, testOwnerID(t, store), "missing", "missing")
			return err
		},
		func() error { _, err := store.Authenticate(ctx, "missing", "secret"); return err },
	} {
		require.ErrorIs(t, operation(), context.Canceled)
	}
}

func TestSQLiteWriteFailuresRollbackRegistryAndCredentials(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "apps.sqlite")
	store, err := apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)

	testCases := []struct {
		name      string
		operation string
		table     string
		invoke    func() error
	}{
		{"app creation", "INSERT", "apps", func() error { _, err := store.Create(ctx, testOwnerID(t, store), validCreateInput()); return err }},
		{"initial credential creation", "INSERT", "app_secrets", func() error { _, err := store.Create(ctx, testOwnerID(t, store), validCreateInput()); return err }},
		{"credential rotation", "INSERT", "app_secrets", func() error {
			_, err := store.CreateSecret(ctx, testOwnerID(t, store), created.App.ID, validSecretInput())
			return err
		}},
		{"credential reset insert", "INSERT", "app_secrets", func() error {
			_, err := store.ResetSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID)
			return err
		}},
		{"credential reset revoke", "UPDATE", "app_secrets", func() error {
			_, err := store.ResetSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID)
			return err
		}},
		{"app update", "UPDATE", "apps", func() error {
			input := validInput()
			input.Name, input.Enabled = "Rejected update", false
			_, err := store.Update(ctx, testOwnerID(t, store), created.App.ID, input)
			return err
		}},
		{"app deletion", "DELETE", "apps", func() error { return store.Delete(ctx, testOwnerID(t, store), created.App.ID) }},
		{"credential deletion", "DELETE", "app_secrets", func() error { return store.Delete(ctx, testOwnerID(t, store), created.App.ID) }},
		{"credential revocation", "UPDATE", "app_secrets", func() error { return store.RevokeSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID) }},
		{"credential metadata", "UPDATE", "app_secrets", func() error {
			_, err := store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID, apps.SecretInput{Name: "Rejected metadata", Description: "Rejected"})
			return err
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := db.ExecContext(ctx, "CREATE TRIGGER fail_write BEFORE "+testCase.operation+" ON "+testCase.table+" BEGIN SELECT RAISE(ABORT, 'blocked registry write'); END")
			require.NoError(t, err)
			err = testCase.invoke()
			require.ErrorContains(t, err, "blocked registry write")
			var sqliteErr interface{ Code() int }
			require.ErrorAs(t, err, &sqliteErr)
			_, err = db.ExecContext(ctx, "DROP TRIGGER fail_write")
			require.NoError(t, err)
			stored, err := store.List(ctx, testOwnerID(t, store))
			require.NoError(t, err)
			assert.Equal(t, []apps.App{created.App}, stored)
			metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
			require.NoError(t, err)
			require.Len(t, metadata, 1)
			assert.Equal(t, created.SecretID, metadata[0].ID)
			assert.Equal(t, validSecretInput().Name, metadata[0].Name)
			assert.Equal(t, validSecretInput().Description, metadata[0].Description)
			assert.Nil(t, metadata[0].RevokedAt)
			_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
			require.NoError(t, err)
		})
	}
}

func TestSecretMetadataUpdatesPreserveCredentialsAndLifecycle(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "apps.sqlite")
	store, err := apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	other, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	initial := metadata[0]
	input := apps.SecretInput{Name: "Production $密钥", Description: "  owner: platform\r\n部署用途\n\t"}
	updated, err := store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, initial.ID, input)
	require.NoError(t, err)
	assert.Equal(t, input.Name, updated.Name)
	assert.Equal(t, input.Description, updated.Description)
	assert.Equal(t, initial.ID, updated.ID)
	assert.Equal(t, initial.CreatedAt, updated.CreatedAt)
	assert.Nil(t, updated.RevokedAt)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.NoError(t, err)
	app, err := store.Get(ctx, created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, created.App, app)
	_, err = store.UpdateSecret(ctx, testOwnerID(t, store), other.App.ID, initial.ID, validSecretInput())
	require.ErrorIs(t, err, apps.ErrNotFound)
	metadata, err = store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, []apps.Secret{updated}, metadata)
	require.NoError(t, store.RevokeSecret(ctx, testOwnerID(t, store), created.App.ID, initial.ID))
	metadata, err = store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	revoked := metadata[0]
	updated, err = store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, initial.ID, apps.SecretInput{Name: "Previous deployment", Description: ""})
	require.NoError(t, err)
	assert.Equal(t, revoked.CreatedAt, updated.CreatedAt)
	assert.Equal(t, revoked.RevokedAt, updated.RevokedAt)
	assert.Empty(t, updated.Description)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	require.NoError(t, store.Close())
	store, err = apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	metadata, err = store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, []apps.Secret{updated}, metadata)
	app, err = store.Get(ctx, created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, created.App, app)
	require.NoError(t, store.Delete(ctx, testOwnerID(t, store), created.App.ID))
	_, err = store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, initial.ID, validSecretInput())
	require.ErrorIs(t, err, apps.ErrNotFound)
}

func TestSecretMetadataValidationIsAtomicAndPreservesExactValues(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	invalid := map[string]apps.SecretInput{
		"empty name":                 {Name: ""},
		"surrounding whitespace":     {Name: " Production "},
		"carriage return":            {Name: "Production\rbackend"},
		"line feed":                  {Name: "Production\nbackend"},
		"long name":                  {Name: strings.Repeat("x", 129)},
		"multibyte long name":        {Name: strings.Repeat("密", 43)},
		"invalid UTF8 name":          {Name: string([]byte{0xff})},
		"long description":           {Name: "Production", Description: strings.Repeat("x", 4097)},
		"multibyte long description": {Name: "Production", Description: strings.Repeat("密", 1366)},
		"invalid UTF8 description":   {Name: "Production", Description: string([]byte{0xff})},
	}
	for name, input := range invalid {
		t.Run(name, func(t *testing.T) {
			creation := validCreateInput()
			creation.InitialSecret = input
			_, err := store.Create(ctx, testOwnerID(t, store), creation)
			require.ErrorIs(t, err, apps.ErrInvalid)
			_, err = store.CreateSecret(ctx, testOwnerID(t, store), created.App.ID, input)
			require.ErrorIs(t, err, apps.ErrInvalid)
			_, err = store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID, input)
			require.ErrorIs(t, err, apps.ErrInvalid)
			listed, err := store.List(ctx, testOwnerID(t, store))
			require.NoError(t, err)
			assert.Equal(t, []apps.App{created.App}, listed)
			metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
			require.NoError(t, err)
			require.Len(t, metadata, 1)
			assert.Equal(t, validSecretInput().Name, metadata[0].Name)
			assert.Equal(t, validSecretInput().Description, metadata[0].Description)
		})
	}
	boundary := apps.SecretInput{Name: strings.Repeat("密", 42) + "ab", Description: strings.Repeat("密", 1365) + " "}
	issued, err := store.CreateSecret(ctx, testOwnerID(t, store), created.App.ID, boundary)
	require.NoError(t, err)
	assert.Equal(t, boundary.Name, issued.Name)
	assert.Equal(t, boundary.Description, issued.Description)
	updated, err := store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID, boundary)
	require.NoError(t, err)
	assert.Equal(t, boundary.Name, updated.Name)
	assert.Equal(t, boundary.Description, updated.Description)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.NoError(t, err)
	_, err = store.Authenticate(ctx, created.App.ID, issued.Secret)
	require.NoError(t, err)
}

func TestPublicPlatformMigrationResetsOldRegistry(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "apps.sqlite")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	migrationProvider, err := goose.NewProvider(goose.DialectSQLite3, db, os.DirFS("migrations"), goose.WithDisableGlobalRegistry(true))
	require.NoError(t, err)
	_, err = migrationProvider.UpTo(ctx, 2)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO apps (id,name,enabled,redirect_uris,providers,created_at,updated_at) VALUES ('existing-app','Old app',1,'["https://product.example/callback"]','["google"]',123,456)`)
	require.NoError(t, err)
	digest := sha256.Sum256([]byte("old-secret"))
	_, err = db.ExecContext(ctx, `INSERT INTO app_secrets (id,app_id,name,description,digest,created_at) VALUES ('old-secret','existing-app','Old secret','Old note',?,123)`, digest[:])
	require.NoError(t, err)
	require.NoError(t, db.Close())
	store, err := apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	_, err = store.Get(ctx, "existing-app")
	require.ErrorIs(t, err, apps.ErrNotFound)
	_, err = store.Authenticate(ctx, "existing-app", "old-secret")
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	ownerID := testOwnerID(t, store)
	listed, err := store.List(ctx, ownerID)
	require.NoError(t, err)
	require.Empty(t, listed)
	created, err := store.Create(ctx, ownerID, validCreateInput())
	require.NoError(t, err)
	require.NoError(t, store.Close())
	store, err = apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	persisted, err := store.GetOwned(ctx, ownerID, created.App.ID)
	require.NoError(t, err)
	require.Equal(t, created.App, persisted)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.NoError(t, err)
	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_secrets WHERE id = 'old-secret'").Scan(&count))
	require.Zero(t, count)
}

func TestSecretMetadataReadFailureRollsBackWrite(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "apps.sqlite")
	store, err := apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `CREATE TRIGGER corrupt_metadata AFTER UPDATE OF name, description ON app_secrets BEGIN UPDATE app_secrets SET created_at = 'unreadable' WHERE id = NEW.id; END`)
	require.NoError(t, err)
	_, err = store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID, apps.SecretInput{Name: "Uncommitted name"})
	require.ErrorContains(t, err, "read app credential metadata")
	metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	assert.Equal(t, created.App.CreatedAt, metadata[0].CreatedAt)
	assert.Equal(t, validSecretInput().Name, metadata[0].Name)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "DROP TRIGGER corrupt_metadata")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE app_secrets SET created_at = 'unreadable' WHERE id = ?", created.SecretID)
	require.NoError(t, err)
	_, err = store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.ErrorContains(t, err, "read app credential metadata")
}

func TestConcurrentMetadataUpdatesAndRevocation(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, testOwnerID(t, store), validCreateInput())
	require.NoError(t, err)
	const workers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			metadata, err := store.UpdateSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID, validSecretInput())
			assert.NoError(t, err)
			assert.Equal(t, created.App.CreatedAt, metadata.CreatedAt)
		})
	}
	wg.Go(func() {
		<-start
		assert.NoError(t, store.RevokeSecret(ctx, testOwnerID(t, store), created.App.ID, created.SecretID))
	})
	close(start)
	wg.Wait()
	metadata, err := store.ListSecrets(ctx, testOwnerID(t, store), created.App.ID)
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.NotNil(t, metadata[0].RevokedAt)
	assert.Equal(t, created.App.CreatedAt, metadata[0].CreatedAt)
	assert.Equal(t, validSecretInput().Name, metadata[0].Name)
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	app, err := store.Get(ctx, created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, created.App, app)
}
