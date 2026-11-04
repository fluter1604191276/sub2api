package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const openAIIQIdentityLock = 14020261106

var openAIIQLoopbackIPs = []string{"127.0.0.1", "::1"}

// EnsureIdentity creates a purpose-bound non-login identity when it is absent.
// It never searches for or adopts a user by email; only the singleton mapping
// establishes ownership of an identity.
func (r *openAIIQRepository) EnsureIdentity(ctx context.Context, groupIDs []int64) (*service.IQIdentity, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("OpenAI IQ identity repository is unavailable")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, openAIIQIdentityLock); err != nil {
		return nil, err
	}

	identity, err := ensureOpenAIIQIdentity(ctx, tx, groupIDs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return identity, nil
}

// SaveSettings atomically makes an enabled configuration and its dedicated
// identity grants visible together. Disabled settings intentionally do not
// create or resurrect an identity.
func (r *openAIIQRepository) SaveSettings(ctx context.Context, settings service.IQSettings) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("OpenAI IQ identity repository is unavailable")
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, openAIIQIdentityLock); err != nil {
		return err
	}
	if settings.Enabled {
		if _, err := ensureOpenAIIQIdentity(ctx, tx, settings.GroupIDs); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, service.SettingKeyOpenAIIQ, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureOpenAIIQIdentity(ctx context.Context, tx *sql.Tx, groupIDs []int64) (*service.IQIdentity, error) {
	identity, marker, err := loadOpenAIIQIdentity(ctx, tx)
	if err == sql.ErrNoRows {
		marker, err = randomOpenAIIQMarker()
		if err != nil {
			return nil, err
		}
		email := "openai-iq-" + marker + "@internal.invalid"
		passwordHash := "!openai-iq:" + marker
		identity = &service.IQIdentity{}
		err = tx.QueryRowContext(ctx, `
			INSERT INTO users (email, username, password_hash, role, balance, concurrency, status, notes, restrict_public_groups)
			VALUES ($1, 'openai-iq', $2, 'user', 0, 1, 'active', $3, true)
			RETURNING id, balance`, email, passwordHash, marker).Scan(&identity.UserID, &identity.Balance)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO openai_iq_identity (singleton, user_id, identity_marker) VALUES (true, $1, $2)`, identity.UserID, marker); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if err := validateOpenAIIQIdentity(ctx, tx, identity.UserID, marker); err != nil {
		return nil, err
	}

	if err := syncOpenAIIQAllowedGroups(ctx, tx, identity.UserID, groupIDs); err != nil {
		return nil, err
	}
	return identity, nil
}

// Identity returns the mapped identity after validating that it remains a
// purpose-bound account. A tampered mapping fails closed.
func (r *openAIIQRepository) Identity(ctx context.Context) (*service.IQIdentity, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("OpenAI IQ identity repository is unavailable")
	}

	identity, marker, err := loadOpenAIIQIdentity(ctx, r.db)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := validateOpenAIIQIdentity(ctx, r.db, identity.UserID, marker); err != nil {
		return nil, err
	}
	return identity, nil
}

// GroupKey returns the active, loopback-restricted key for a dedicated user
// and group. A missing key is created only after the identity and group grant
// have been validated inside the same transaction.
func (r *openAIIQRepository) GroupKey(ctx context.Context, userID, groupID int64) (*service.APIKey, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("OpenAI IQ identity repository is unavailable")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, openAIIQIdentityLock); err != nil {
		return nil, err
	}

	identity, marker, err := loadOpenAIIQIdentity(ctx, tx)
	if err != nil {
		return nil, identityLookupError(err)
	}
	if identity.UserID != userID {
		return nil, fmt.Errorf("OpenAI IQ identity user mismatch")
	}
	if err := validateOpenAIIQIdentity(ctx, tx, userID, marker); err != nil {
		return nil, err
	}
	if err := validateOpenAIIQGroupGrant(ctx, tx, userID, groupID); err != nil {
		return nil, err
	}

	key, err := loadOpenAIIQGroupKey(ctx, tx, userID, groupID)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return key, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	rawKey, err := randomOpenAIIQKey()
	if err != nil {
		return nil, err
	}
	whitelist, err := json.Marshal(openAIIQLoopbackIPs)
	if err != nil {
		return nil, err
	}
	key = &service.APIKey{UserID: userID, Key: rawKey, Status: service.StatusAPIKeyActive}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO api_keys (user_id, key, name, group_id, status, ip_whitelist)
		VALUES ($1, $2, $3, $4, 'active', $5::jsonb)
		RETURNING id`, userID, rawKey, "openai-iq-"+fmt.Sprint(groupID), groupID, string(whitelist)).Scan(&key.ID)
	if err != nil {
		return nil, err
	}
	groupIDCopy := groupID
	key.GroupID = &groupIDCopy
	key.IPWhitelist = append([]string(nil), openAIIQLoopbackIPs...)
	if _, err := tx.ExecContext(ctx, `INSERT INTO openai_iq_keys (group_id, key_id, user_id) VALUES ($1, $2, $3)`, groupID, key.ID, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return key, nil
}

type openAIIQQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func loadOpenAIIQIdentity(ctx context.Context, db openAIIQQueryer) (*service.IQIdentity, string, error) {
	identity := &service.IQIdentity{}
	var marker string
	err := db.QueryRowContext(ctx, `
		SELECT mapping.user_id, users.balance, mapping.identity_marker
		FROM openai_iq_identity mapping
		JOIN users ON users.id = mapping.user_id
		WHERE mapping.singleton = true`).Scan(&identity.UserID, &identity.Balance, &marker)
	return identity, marker, err
}

func validateOpenAIIQIdentity(ctx context.Context, db openAIIQQueryer, userID int64, marker string) error {
	var valid bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE id = $1
			  AND email = $2
			  AND password_hash = $3
			  AND role = 'user'
			  AND status = 'active'
			  AND notes = $4
			  AND restrict_public_groups = true
			  AND deleted_at IS NULL
		)`, userID, "openai-iq-"+marker+"@internal.invalid", "!openai-iq:"+marker, marker).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("OpenAI IQ identity validation failed")
	}
	return nil
}

func syncOpenAIIQAllowedGroups(ctx context.Context, db openAIIQQueryer, userID int64, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		_, err := db.ExecContext(ctx, `DELETE FROM user_allowed_groups WHERE user_id = $1`, userID)
		return err
	}
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups WHERE id = ANY($1) AND platform = 'openai' AND status = 'active' AND deleted_at IS NULL`, pq.Array(groupIDs)).Scan(&count)
	if err != nil {
		return err
	}
	if count != len(groupIDs) {
		return fmt.Errorf("OpenAI IQ groups must be active OpenAI groups")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM user_allowed_groups WHERE user_id = $1 AND NOT (group_id = ANY($2))`, userID, pq.Array(groupIDs)); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if _, err := db.ExecContext(ctx, `INSERT INTO user_allowed_groups (user_id, group_id) VALUES ($1, $2) ON CONFLICT (user_id, group_id) DO NOTHING`, userID, groupID); err != nil {
			return err
		}
	}
	return nil
}

func validateOpenAIIQGroupGrant(ctx context.Context, db openAIIQQueryer, userID, groupID int64) error {
	var granted bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM groups
			JOIN user_allowed_groups ON user_allowed_groups.group_id = groups.id
			WHERE groups.id = $1
			  AND groups.platform = 'openai'
			  AND groups.status = 'active'
			  AND groups.deleted_at IS NULL
			  AND user_allowed_groups.user_id = $2
		)`, groupID, userID).Scan(&granted)
	if err != nil {
		return err
	}
	if !granted {
		return fmt.Errorf("OpenAI IQ group is not granted to identity")
	}
	return nil
}

func loadOpenAIIQGroupKey(ctx context.Context, db openAIIQQueryer, userID, groupID int64) (*service.APIKey, error) {
	key := &service.APIKey{}
	var keyGroupID int64
	var whitelist []byte
	err := db.QueryRowContext(ctx, `
		SELECT keys.id, keys.user_id, keys.key, keys.status, keys.group_id, keys.ip_whitelist
		FROM openai_iq_keys mapping
		JOIN api_keys keys ON keys.id = mapping.key_id
		WHERE mapping.group_id = $1
		  AND mapping.user_id = $2
		  AND keys.user_id = $2
		  AND keys.group_id = $1
		  AND keys.status = 'active'
		  AND keys.deleted_at IS NULL`, groupID, userID).Scan(&key.ID, &key.UserID, &key.Key, &key.Status, &keyGroupID, &whitelist)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(whitelist, &key.IPWhitelist); err != nil || !sameLoopbackIPs(key.IPWhitelist) {
		return nil, fmt.Errorf("OpenAI IQ key validation failed")
	}
	key.GroupID = &keyGroupID
	return key, nil
}

func sameLoopbackIPs(actual []string) bool {
	return len(actual) == len(openAIIQLoopbackIPs) && actual[0] == openAIIQLoopbackIPs[0] && actual[1] == openAIIQLoopbackIPs[1]
}

func randomOpenAIIQMarker() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate OpenAI IQ identity marker: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func randomOpenAIIQKey() (string, error) {
	bytes := make([]byte, 30)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate OpenAI IQ API key: %w", err)
	}
	return "sk-" + hex.EncodeToString(bytes), nil
}

func identityLookupError(err error) error {
	if err == sql.ErrNoRows {
		return fmt.Errorf("OpenAI IQ identity has not been provisioned")
	}
	return err
}
