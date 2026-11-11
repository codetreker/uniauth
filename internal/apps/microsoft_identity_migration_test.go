package apps

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/codetreker/uniauth/internal/providers"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

const migrationMicrosoftTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"

func oldMicrosoftID(objectID string) string {
	return "microsoft:" + migrationMicrosoftTenant + ":" + objectID
}

func currentMicrosoftID(objectID string) string {
	return "msft:" + objectID + "@" + migrationMicrosoftTenant
}

func platformBeforeMicrosoftMigration(t *testing.T) (string, *Store, *goose.Provider) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "platform.sqlite")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	migrations, err := goose.NewProvider(goose.DialectSQLite3, db, os.DirFS("migrations"), goose.WithDisableGlobalRegistry(true))
	require.NoError(t, err)
	_, err = migrations.UpTo(t.Context(), 3)
	require.NoError(t, err)
	store := &Store{db: db}
	require.NoError(t, store.InitializePlatform(t.Context(), "admin@example.test"))
	return path, store, migrations
}

func TestMicrosoftIdentityMigrationPreservesAccountsAndOwnership(t *testing.T) {
	path, before, _ := platformBeforeMicrosoftMigration(t)
	admin := platformAccount(t, before, platformIdentity("google", "google:admin", "admin@example.test", platformPointer(true)))
	require.True(t, admin.IsAdmin)
	require.NoError(t, before.LinkIdentity(t.Context(), admin.ID, platformIdentity("microsoft", oldMicrosoftID("admin-object"), "admin@example.test", nil)))
	member := platformAccount(t, before, platformIdentity("microsoft", oldMicrosoftID("member-object"), "member@example.test", nil))
	unrelated := platformAccount(t, before, providers.Identity{Provider: "google", ID: oldMicrosoftID("unrelated")})
	canonical := platformAccount(t, before, providers.Identity{Provider: "microsoft", ID: currentMicrosoftID("already-current")})
	adminApp := platformCreate(t, before, admin.ID, "Admin product")
	memberApp := platformCreate(t, before, member.ID, "Member product")
	adminLinks, err := before.ListIdentities(t.Context(), admin.ID)
	require.NoError(t, err)
	memberLinks, err := before.ListIdentities(t.Context(), member.ID)
	require.NoError(t, err)
	require.NoError(t, before.Close())

	store, err := Open(t.Context(), Config{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	for _, account := range []Account{admin, member, unrelated, canonical} {
		stored, err := store.Account(t.Context(), account.ID)
		require.NoError(t, err)
		require.Equal(t, account, stored)
	}
	for i := range adminLinks {
		if adminLinks[i].Provider == "microsoft" {
			adminLinks[i].ID = currentMicrosoftID("admin-object")
		}
	}
	memberLinks[0].ID = currentMicrosoftID("member-object")
	for accountID, expected := range map[string][]LinkedIdentity{admin.ID: adminLinks, member.ID: memberLinks} {
		actual, err := store.ListIdentities(t.Context(), accountID)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	for accountID, app := range map[string]CreatedApp{admin.ID: adminApp, member.ID: memberApp} {
		owned, err := store.GetOwned(t.Context(), accountID, app.App.ID)
		require.NoError(t, err)
		require.Equal(t, app.App, owned)
		authenticated, err := store.Authenticate(t.Context(), app.App.ID, app.Secret)
		require.NoError(t, err)
		require.Equal(t, app.App, authenticated)
	}
	var claimedAdmin string
	require.NoError(t, store.db.QueryRowContext(t.Context(), "SELECT claimed_account_id FROM platform_state WHERE id = 1").Scan(&claimedAdmin))
	require.Equal(t, admin.ID, claimedAdmin)
	for _, test := range []struct{ objectID, accountID string }{{"admin-object", admin.ID}, {"member-object", member.ID}, {"already-current", canonical.ID}} {
		resolved := platformAccount(t, store, providers.Identity{Provider: "microsoft", ID: currentMicrosoftID(test.objectID)})
		require.Equal(t, test.accountID, resolved.ID)
	}
	require.Equal(t, 4, platformCount(t, store, "accounts"))
	links, err := store.ListIdentities(t.Context(), unrelated.ID)
	require.NoError(t, err)
	require.Equal(t, oldMicrosoftID("unrelated"), links[0].ID)
	require.NoError(t, store.Close())

	store, err = Open(t.Context(), Config{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.Equal(t, 4, platformCount(t, store, "accounts"))
	links, err = store.ListIdentities(t.Context(), member.ID)
	require.NoError(t, err)
	require.Equal(t, currentMicrosoftID("member-object"), links[0].ID)
}

func TestMicrosoftIdentityMigrationRejectsCollisionsAtomically(t *testing.T) {
	for _, sameAccount := range []bool{false, true} {
		name := "different accounts"
		if sameAccount {
			name = "same account"
		}
		t.Run(name, func(t *testing.T) {
			path, before, _ := platformBeforeMicrosoftMigration(t)
			first := platformAccount(t, before, providers.Identity{Provider: "microsoft", ID: oldMicrosoftID("conflict")})
			other := first
			canonical := providers.Identity{Provider: "microsoft", ID: currentMicrosoftID("conflict")}
			if sameAccount {
				require.NoError(t, before.LinkIdentity(t.Context(), first.ID, canonical))
			} else {
				other = platformAccount(t, before, canonical)
			}
			independent := platformAccount(t, before, providers.Identity{Provider: "microsoft", ID: oldMicrosoftID("independent")})
			app := platformCreate(t, before, first.ID, "Preserved product")
			accountCount := platformCount(t, before, "accounts")
			linkCount := platformCount(t, before, "account_identities")
			require.NoError(t, before.Close())

			failed, err := Open(t.Context(), Config{Path: path})
			require.ErrorContains(t, err, "migrate app database")
			require.ErrorContains(t, err, "UNIQUE constraint failed")
			require.Nil(t, failed)
			db, err := sql.Open("sqlite", path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			preserved := &Store{db: db}
			require.Equal(t, accountCount, platformCount(t, preserved, "accounts"))
			require.Equal(t, linkCount, platformCount(t, preserved, "account_identities"))
			for id, accountID := range map[string]string{oldMicrosoftID("conflict"): first.ID, currentMicrosoftID("conflict"): other.ID, oldMicrosoftID("independent"): independent.ID} {
				var actual string
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT account_id FROM account_identities WHERE provider = 'microsoft' AND provider_id = ?", id).Scan(&actual))
				require.Equal(t, accountID, actual)
			}
			var version int64
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1").Scan(&version))
			require.EqualValues(t, 3, version)
			owned, err := preserved.GetOwned(t.Context(), first.ID, app.App.ID)
			require.NoError(t, err)
			require.Equal(t, app.App, owned)
		})
	}
}

func TestMicrosoftIdentityMigrationRollbackPreservesLink(t *testing.T) {
	_, store, migrations := platformBeforeMicrosoftMigration(t)
	account := platformAccount(t, store, providers.Identity{Provider: "microsoft", ID: oldMicrosoftID("object:with@separators")})
	before, err := store.ListIdentities(t.Context(), account.ID)
	require.NoError(t, err)
	_, err = migrations.Up(t.Context())
	require.NoError(t, err)
	current, err := store.ListIdentities(t.Context(), account.ID)
	require.NoError(t, err)
	require.Equal(t, currentMicrosoftID("object:with@separators"), current[0].ID)
	_, err = migrations.Down(t.Context())
	require.NoError(t, err)
	rolledBack, err := store.ListIdentities(t.Context(), account.ID)
	require.NoError(t, err)
	require.Equal(t, before, rolledBack)
}
