package apps_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
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

func openStore(t *testing.T, loopback bool) *apps.Store {
	t.Helper()
	store, err := apps.Open(t.Context(), apps.Config{Path: filepath.Join(t.TempDir(), "apps.sqlite"), AllowLoopbackHTTP: loopback})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func TestRegistryPersistenceAndCredentialLifecycle(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "apps.sqlite")
	store, err := apps.Open(ctx, apps.Config{Path: path})
	require.NoError(t, err)
	created, err := store.Create(ctx, validInput())
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
	listed, err := store.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []apps.App{stored}, listed)
	authenticated, err := store.Authenticate(ctx, stored.ID, created.Secret)
	require.NoError(t, err)
	assert.Equal(t, stored, authenticated)

	updatedInput := validInput()
	updatedInput.Name = "Renamed Product"
	updatedInput.Enabled = false
	updatedInput.Providers = []string{"google"}
	updated, err := store.Update(ctx, stored.ID, updatedInput)
	require.NoError(t, err)
	assert.Equal(t, stored.CreatedAt, updated.CreatedAt)
	assert.GreaterOrEqual(t, updated.UpdatedAt.UnixNano(), stored.UpdatedAt.UnixNano())
	_, err = store.Authenticate(ctx, stored.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	updatedInput.Enabled = true
	_, err = store.Update(ctx, stored.ID, updatedInput)
	require.NoError(t, err)

	issued, err := store.CreateSecret(ctx, stored.ID)
	require.NoError(t, err)
	assert.NotEqual(t, created.Secret, issued.Secret)
	for _, secret := range []string{created.Secret, issued.Secret} {
		_, err := store.Authenticate(ctx, stored.ID, secret)
		require.NoError(t, err)
	}
	metadata, err := store.ListSecrets(ctx, stored.ID)
	require.NoError(t, err)
	require.Len(t, metadata, 2)
	serialized, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), created.Secret)
	assert.NotContains(t, string(serialized), issued.Secret)
	assert.NotContains(t, string(serialized), "digest")
	assert.NotContains(t, string(serialized), "revoked_at")

	require.NoError(t, store.RevokeSecret(ctx, stored.ID, created.SecretID))
	metadata, err = store.ListSecrets(ctx, stored.ID)
	require.NoError(t, err)
	require.NotNil(t, metadata[0].RevokedAt)
	revoked := *metadata[0].RevokedAt
	require.NoError(t, store.RevokeSecret(ctx, stored.ID, created.SecretID))
	metadata, err = store.ListSecrets(ctx, stored.ID)
	require.NoError(t, err)
	assert.Equal(t, revoked, *metadata[0].RevokedAt)
	_, err = store.Authenticate(ctx, stored.ID, created.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	_, err = store.Authenticate(ctx, stored.ID, issued.Secret)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, stored.ID))
	_, err = store.Get(ctx, stored.ID)
	require.ErrorIs(t, err, apps.ErrNotFound)
	_, err = store.ListSecrets(ctx, stored.ID)
	require.ErrorIs(t, err, apps.ErrNotFound)
	_, err = store.Authenticate(ctx, stored.ID, issued.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	listed, err = store.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, listed)
	assert.NotNil(t, listed)
}

func TestAppIsolationAndMissingRecords(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	first, err := store.Create(ctx, validInput())
	require.NoError(t, err)
	second, err := store.Create(ctx, validInput())
	require.NoError(t, err)
	_, err = store.Authenticate(ctx, first.App.ID, second.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	_, err = store.Authenticate(ctx, second.App.ID, first.Secret)
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	_, err = store.Authenticate(ctx, first.App.ID, "")
	require.ErrorIs(t, err, apps.ErrUnauthorized)
	require.ErrorIs(t, store.RevokeSecret(ctx, second.App.ID, first.SecretID), apps.ErrNotFound)
	_, err = store.Authenticate(ctx, first.App.ID, first.Secret)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, first.App.ID))
	_, err = store.Authenticate(ctx, second.App.ID, second.Secret)
	require.NoError(t, err)
	for _, operation := range []func() error{
		func() error { _, err := store.Get(ctx, "missing"); return err },
		func() error { _, err := store.Update(ctx, "missing", validInput()); return err },
		func() error { return store.Delete(ctx, "missing") },
		func() error { _, err := store.CreateSecret(ctx, "missing"); return err },
		func() error { _, err := store.ListSecrets(ctx, "missing"); return err },
		func() error { return store.RevokeSecret(ctx, "missing", second.SecretID) },
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
			_, err := store.Create(t.Context(), input)
			require.ErrorIs(t, err, apps.ErrInvalid)
			_, err = store.Update(t.Context(), "missing", input)
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
			_, err := store.Create(t.Context(), input)
			require.ErrorIs(t, err, apps.ErrInvalid)
		})
	}
	input := validInput()
	created, err := store.Create(t.Context(), input)
	require.NoError(t, err)
	assert.Equal(t, input.RedirectURIs, created.App.RedirectURIs)
	invalidUpdate := validInput()
	invalidUpdate.RedirectURIs = []string{"https://product.example/callback?code=reserved"}
	_, err = store.Update(t.Context(), created.App.ID, invalidUpdate)
	require.ErrorIs(t, err, apps.ErrInvalid)
	input.RedirectURIs[0], input.Providers[0] = "changed", "changed"
	stored, err := store.Get(t.Context(), created.App.ID)
	require.NoError(t, err)
	assert.Equal(t, created.App, stored)

	loopbackStore := openStore(t, true)
	for _, uri := range []string{"http://localhost:8080/callback", "http://127.0.0.1:8080/callback", "http://127.2.3.4/callback", "http://[::1]:8080/callback", "https://product.example/callback"} {
		input := validInput()
		input.RedirectURIs = []string{uri}
		_, err := loopbackStore.Create(t.Context(), input)
		require.NoError(t, err)
	}
	for _, uri := range []string{"http://localhost.example/callback", "http://192.168.1.1/callback", "http://product.example/callback"} {
		input := validInput()
		input.RedirectURIs = []string{uri}
		_, err := loopbackStore.Create(t.Context(), input)
		require.ErrorIs(t, err, apps.ErrInvalid)
	}
}

func TestConcurrentRotationAndRevocation(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, validInput())
	require.NoError(t, err)
	const workers = 16
	var wg sync.WaitGroup
	issued := make(chan apps.IssuedSecret, workers)
	failures := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			secret, err := store.CreateSecret(ctx, created.App.ID)
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
			assert.NoError(t, store.RevokeSecret(ctx, created.App.ID, secret.ID))
		})
	}
	wg.Wait()
	for _, secret := range secrets {
		_, err := store.Authenticate(ctx, created.App.ID, secret.Secret)
		require.ErrorIs(t, err, apps.ErrUnauthorized)
	}
	_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
	require.NoError(t, err)
	metadata, err := store.ListSecrets(ctx, created.App.ID)
	require.NoError(t, err)
	require.Len(t, metadata, workers+1)
}

func TestDeleteOrdersConcurrentSecretCreation(t *testing.T) {
	store, ctx := openStore(t, false), t.Context()
	created, err := store.Create(ctx, validInput())
	require.NoError(t, err)
	const workers = 16
	start := make(chan struct{})
	issued := make(chan apps.IssuedSecret, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			secret, err := store.CreateSecret(ctx, created.App.ID)
			if err != nil {
				assert.ErrorIs(t, err, apps.ErrNotFound)
				return
			}
			issued <- secret
		})
	}
	wg.Go(func() {
		<-start
		assert.NoError(t, store.Delete(ctx, created.App.ID))
	})
	close(start)
	wg.Wait()
	close(issued)
	for secret := range issued {
		_, err := store.Authenticate(ctx, created.App.ID, secret.Secret)
		require.ErrorIs(t, err, apps.ErrUnauthorized)
		require.ErrorIs(t, store.RevokeSecret(ctx, created.App.ID, secret.ID), apps.ErrNotFound)
	}
	_, err = store.ListSecrets(ctx, created.App.ID)
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
		func() error { _, err := store.Create(ctx, validInput()); return err },
		func() error { _, err := store.Get(ctx, "missing"); return err },
		func() error { _, err := store.List(ctx); return err },
		func() error { _, err := store.Update(ctx, "missing", validInput()); return err },
		func() error { return store.Delete(ctx, "missing") },
		func() error { _, err := store.CreateSecret(ctx, "missing"); return err },
		func() error { _, err := store.ListSecrets(ctx, "missing"); return err },
		func() error { return store.RevokeSecret(ctx, "missing", "missing") },
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
	created, err := store.Create(ctx, validInput())
	require.NoError(t, err)

	testCases := []struct {
		name      string
		operation string
		table     string
		invoke    func() error
	}{
		{"app creation", "INSERT", "apps", func() error { _, err := store.Create(ctx, validInput()); return err }},
		{"initial credential creation", "INSERT", "app_secrets", func() error { _, err := store.Create(ctx, validInput()); return err }},
		{"credential rotation", "INSERT", "app_secrets", func() error { _, err := store.CreateSecret(ctx, created.App.ID); return err }},
		{"app update", "UPDATE", "apps", func() error {
			input := validInput()
			input.Name, input.Enabled = "Rejected update", false
			_, err := store.Update(ctx, created.App.ID, input)
			return err
		}},
		{"app deletion", "DELETE", "apps", func() error { return store.Delete(ctx, created.App.ID) }},
		{"credential deletion", "DELETE", "app_secrets", func() error { return store.Delete(ctx, created.App.ID) }},
		{"credential revocation", "UPDATE", "app_secrets", func() error { return store.RevokeSecret(ctx, created.App.ID, created.SecretID) }},
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
			stored, err := store.List(ctx)
			require.NoError(t, err)
			assert.Equal(t, []apps.App{created.App}, stored)
			metadata, err := store.ListSecrets(ctx, created.App.ID)
			require.NoError(t, err)
			require.Len(t, metadata, 1)
			assert.Equal(t, created.SecretID, metadata[0].ID)
			assert.Nil(t, metadata[0].RevokedAt)
			_, err = store.Authenticate(ctx, created.App.ID, created.Secret)
			require.NoError(t, err)
		})
	}
}
