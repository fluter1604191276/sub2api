//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIIQSaveSettingsPostgresCommitsIdentityAndSettingsTogether(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 2)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	_, err := integrationDB.ExecContext(ctx, `DELETE FROM openai_iq_identity`)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key = $1`, service.SettingKeyOpenAIIQ)
	require.NoError(t, err)
	var generatedUserID int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_keys WHERE user_id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM user_allowed_groups WHERE user_id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM api_keys WHERE user_id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_identity`)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM settings WHERE key = $1`, service.SettingKeyOpenAIIQ)
	})

	settings := openAIIQSettings(groupIDs)
	require.NoError(t, repo.SaveSettings(ctx, settings))
	identity, err := repo.Identity(ctx)
	require.NoError(t, err)
	require.NotNil(t, identity)
	generatedUserID = identity.UserID
	assertOpenAIIQSettingAndGrants(t, settings, identity.UserID, groupIDs)

	invalid := settings
	invalid.GroupIDs = []int64{groupIDs[0], 987654321}
	require.Error(t, repo.SaveSettings(ctx, invalid))
	assertOpenAIIQSettingAndGrants(t, settings, identity.UserID, groupIDs)

	updated := settings
	updated.GroupIDs = groupIDs[:1]
	updated.Question = "settings-atomic-update"
	require.NoError(t, repo.SaveSettings(ctx, updated))
	assertOpenAIIQSettingAndGrants(t, updated, identity.UserID, updated.GroupIDs)

	_, err = integrationDB.ExecContext(ctx, `DELETE FROM openai_iq_identity`)
	require.NoError(t, err)
	disabled := updated
	disabled.Enabled = false
	require.NoError(t, repo.SaveSettings(ctx, disabled))
	identity, err = repo.Identity(ctx)
	require.NoError(t, err)
	require.Nil(t, identity, "disabled save must not provision or restore an identity")
}

func assertOpenAIIQSettingAndGrants(t *testing.T, want service.IQSettings, userID int64, groupIDs []int64) {
	t.Helper()
	var raw string
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = $1`, service.SettingKeyOpenAIIQ).Scan(&raw))
	var got service.IQSettings
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Equal(t, want, got)

	rows, err := integrationDB.QueryContext(context.Background(), `SELECT group_id FROM user_allowed_groups WHERE user_id = $1 ORDER BY group_id`, userID)
	require.NoError(t, err)
	defer rows.Close()
	actual := make([]int64, 0, len(groupIDs))
	for rows.Next() {
		var groupID int64
		require.NoError(t, rows.Scan(&groupID))
		actual = append(actual, groupID)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, groupIDs, actual)
}

func TestOpenAIIQIdentityPostgresIsDedicatedAndFailsClosedWhenTampered(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 2)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)

	// This test owns the singleton mapping in the freshly migrated harness.
	_, err := integrationDB.ExecContext(ctx, `DELETE FROM openai_iq_identity`)
	require.NoError(t, err)

	var ordinaryUserID int64
	ordinaryEmail := fmt.Sprintf("ordinary-openai-iq-%d@example.invalid", groupIDs[0])
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role, balance, status)
		VALUES ($1, 'ordinary-hash', 'user', 7, 'active')
		RETURNING id`, ordinaryEmail).Scan(&ordinaryUserID))
	t.Cleanup(func() {
		var generatedUserID int64
		_ = integrationDB.QueryRowContext(context.Background(), `SELECT user_id FROM openai_iq_identity WHERE singleton = true`).Scan(&generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_keys WHERE user_id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM user_allowed_groups WHERE user_id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM api_keys WHERE user_id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_identity`)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, generatedUserID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, ordinaryUserID)
	})

	identity, err := repo.EnsureIdentity(ctx, groupIDs)
	require.NoError(t, err)
	require.NotEqual(t, ordinaryUserID, identity.UserID)
	require.Zero(t, identity.Balance)

	var email, passwordHash, role, status, notes string
	var restricted bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT email, password_hash, role, status, notes, restrict_public_groups
		FROM users WHERE id = $1`, identity.UserID).Scan(&email, &passwordHash, &role, &status, &notes, &restricted))
	require.Contains(t, email, "@internal.invalid")
	require.Equal(t, "user", role)
	require.Equal(t, "active", status)
	require.True(t, restricted)
	require.Equal(t, "!openai-iq:"+notes, passwordHash)

	repeated, err := repo.EnsureIdentity(ctx, groupIDs)
	require.NoError(t, err)
	require.Equal(t, identity.UserID, repeated.UserID)

	var ordinaryBalance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = $1`, ordinaryUserID).Scan(&ordinaryBalance))
	require.Equal(t, float64(7), ordinaryBalance, "ordinary user must not be adopted or modified")

	_, err = repo.EnsureIdentity(ctx, groupIDs[:1])
	require.NoError(t, err)
	var allowed []int64
	rows, err := integrationDB.QueryContext(ctx, `SELECT group_id FROM user_allowed_groups WHERE user_id = $1 ORDER BY group_id`, identity.UserID)
	require.NoError(t, err)
	for rows.Next() {
		var groupID int64
		require.NoError(t, rows.Scan(&groupID))
		allowed = append(allowed, groupID)
	}
	require.NoError(t, rows.Close())
	require.Equal(t, groupIDs[:1], allowed)

	key, err := repo.GroupKey(ctx, identity.UserID, groupIDs[0])
	require.NoError(t, err)
	require.NotEmpty(t, key.Key)
	require.True(t, key.IsActive())
	require.Equal(t, []string{"127.0.0.1", "::1"}, key.IPWhitelist)
	require.NotNil(t, key.GroupID)
	require.Equal(t, groupIDs[0], *key.GroupID)

	// The public identity DTO has no key field; raw key material remains internal.
	readOnly, err := repo.Identity(ctx)
	require.NoError(t, err)
	require.Equal(t, identity.UserID, readOnly.UserID)
	identityJSON, err := json.Marshal(readOnly)
	require.NoError(t, err)
	require.NotContains(t, string(identityJSON), key.Key)

	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT identity_marker FROM openai_iq_identity WHERE singleton = true`).Scan(&notes))
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, identity.UserID)
	require.NoError(t, err)
	_, err = repo.Identity(ctx)
	require.Error(t, err, "tampered role must fail closed")
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET role = 'user' WHERE id = $1`, identity.UserID)
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET user_id = $1 WHERE id = $2`, ordinaryUserID, key.ID)
	require.NoError(t, err)
	_, err = repo.GroupKey(ctx, identity.UserID, groupIDs[0])
	require.Error(t, err, "key mapped to another user must fail closed")
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET user_id = $1 WHERE id = $2`, identity.UserID, key.ID)
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET group_id = $1 WHERE id = $2`, groupIDs[1], key.ID)
	require.NoError(t, err)
	_, err = repo.GroupKey(ctx, identity.UserID, groupIDs[0])
	require.Error(t, err, "key mapped to another group must fail closed")
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET group_id = $1 WHERE id = $2`, groupIDs[0], key.ID)
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_iq_identity SET identity_marker = 'tampered' WHERE singleton = true`)
	require.NoError(t, err)
	_, err = repo.Identity(ctx)
	require.Error(t, err, "tampered identity marker must fail closed")
	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_iq_identity SET identity_marker = $1 WHERE singleton = true`, notes)
	require.NoError(t, err)

	_, err = repo.GroupKey(ctx, identity.UserID, groupIDs[1])
	require.Error(t, err, "deselected group must not get a key")

}
