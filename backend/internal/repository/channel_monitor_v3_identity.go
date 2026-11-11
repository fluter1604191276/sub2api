package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const channelMonitorV3IdentityLock = 14520261105

func (r *channelMonitorV3Repository) EnsureProbeIdentity(ctx context.Context) (*service.ChannelMonitorV3Identity, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, channelMonitorV3IdentityLock); err != nil {
		return nil, err
	}
	identity, err := ensureChannelMonitorV3Identity(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return identity, nil
}

func ensureChannelMonitorV3Identity(ctx context.Context, tx *sql.Tx) (*service.ChannelMonitorV3Identity, error) {
	identity, marker, err := loadChannelMonitorV3Identity(ctx, tx)
	if err == sql.ErrNoRows {
		marker, err = randomOpenAIIQMarker()
		if err != nil {
			return nil, err
		}
		identity = &service.ChannelMonitorV3Identity{}
		err = tx.QueryRowContext(ctx, `INSERT INTO users (email,username,password_hash,role,balance,concurrency,status,notes,restrict_public_groups) VALUES ($1,'channel-monitor-v3',$2,'user',1,1,'active',$3,true) RETURNING id,balance`, "channel-monitor-v3-"+marker+"@internal.invalid", "!channel-monitor-v3:"+marker, marker).Scan(&identity.UserID, &identity.Balance)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_identity(singleton,user_id,identity_marker) VALUES(true,$1,$2)`, identity.UserID, marker)
	} else if err != nil {
		return nil, err
	} else if err = validateChannelMonitorV3Identity(ctx, tx, identity.UserID, marker); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return identity, nil
}

func (r *channelMonitorV3Repository) ProbeGroupKey(ctx context.Context, groupID int64) (*service.APIKey, error) {
	if groupID <= 0 {
		return nil, fmt.Errorf("invalid group id")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, channelMonitorV3IdentityLock); err != nil {
		return nil, err
	}
	identity, marker, err := loadChannelMonitorV3Identity(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = validateChannelMonitorV3Identity(ctx, tx, identity.UserID, marker); err != nil {
		return nil, err
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_monitor_v3_settings s JOIN channel_monitor_v3_groups c ON c.visible=true JOIN groups g ON g.id=c.group_id WHERE s.singleton=true AND s.enabled=true AND g.id=$1 AND g.status='active' AND g.deleted_at IS NULL AND c.probe_interval_seconds>0)`, groupID).Scan(&allowed)
	if err != nil || !allowed {
		return nil, fmt.Errorf("channel monitor v3 group is not probe eligible")
	}
	var key service.APIKey
	var whitelist []byte
	var keyGroup int64
	err = tx.QueryRowContext(ctx, `SELECT k.id,k.user_id,k.key,k.status,k.group_id,k.ip_whitelist FROM channel_monitor_v3_keys m JOIN api_keys k ON k.id=m.key_id WHERE m.group_id=$1 AND m.user_id=$2 AND k.status='active' AND k.deleted_at IS NULL`, groupID, identity.UserID).Scan(&key.ID, &key.UserID, &key.Key, &key.Status, &keyGroup, &whitelist)
	if err == sql.ErrNoRows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_allowed_groups(user_id,group_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, identity.UserID, groupID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_group_rate_multipliers(user_id,group_id,rate_multiplier) VALUES($1,$2,0.001) ON CONFLICT(user_id,group_id) DO UPDATE SET rate_multiplier=EXCLUDED.rate_multiplier`, identity.UserID, groupID); err != nil {
			return nil, err
		}
		raw, e := randomOpenAIIQKey()
		if e != nil {
			return nil, e
		}
		encoded, e := json.Marshal(openAIIQLoopbackIPs)
		if e != nil {
			return nil, e
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id,status,ip_whitelist) VALUES($1,$2,$3,$4,'active',$5::jsonb) RETURNING id`, identity.UserID, raw, "channel-monitor-v3-"+fmt.Sprint(groupID), groupID, string(encoded)).Scan(&key.ID)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_keys(group_id,key_id,user_id) VALUES($1,$2,$3)`, groupID, key.ID, identity.UserID); err != nil {
			return nil, err
		}
		key.UserID = identity.UserID
		key.Key = raw
		key.Status = service.StatusAPIKeyActive
		key.IPWhitelist = append([]string(nil), openAIIQLoopbackIPs...)
		keyGroup = groupID
	} else if err != nil {
		return nil, err
	}
	if key.UserID != identity.UserID || keyGroup != groupID || key.Key == "" {
		return nil, fmt.Errorf("channel monitor v3 key ownership validation failed")
	}
	key.GroupID = &keyGroup
	if len(key.IPWhitelist) == 0 {
		_ = json.Unmarshal(whitelist, &key.IPWhitelist)
	}
	if !sameLoopbackIPs(key.IPWhitelist) {
		return nil, fmt.Errorf("channel monitor v3 key validation failed")
	}
	var accessGranted bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_allowed_groups WHERE user_id=$1 AND group_id=$2)
		AND EXISTS(SELECT 1 FROM user_group_rate_multipliers WHERE user_id=$1 AND group_id=$2 AND rate_multiplier=0.001)`, identity.UserID, groupID).Scan(&accessGranted); err != nil {
		return nil, err
	}
	if !accessGranted {
		return nil, fmt.Errorf("channel monitor v3 group access or multiplier validation failed")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &key, nil
}

func loadChannelMonitorV3Identity(ctx context.Context, db openAIIQQueryer) (*service.ChannelMonitorV3Identity, string, error) {
	var out service.ChannelMonitorV3Identity
	var marker string
	err := db.QueryRowContext(ctx, `SELECT m.user_id,u.balance,m.identity_marker FROM channel_monitor_v3_identity m JOIN users u ON u.id=m.user_id WHERE m.singleton=true`).Scan(&out.UserID, &out.Balance, &marker)
	return &out, marker, err
}
func validateChannelMonitorV3Identity(ctx context.Context, db openAIIQQueryer, id int64, marker string) error {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND email=$2 AND password_hash=$3 AND role='user' AND status='active' AND notes=$4 AND restrict_public_groups=true AND deleted_at IS NULL)`, id, "channel-monitor-v3-"+marker+"@internal.invalid", "!channel-monitor-v3:"+marker, marker).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("channel monitor v3 identity validation failed")
	}
	return nil
}
